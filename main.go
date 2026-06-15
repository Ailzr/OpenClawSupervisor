package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	a := app.NewWithID("com.openclaw.supervisor")
	w := a.NewWindow("OpenClaw 控制台")
	w.Resize(fyne.NewSize(800, 600))

	cfg := LoadConfig()
	log.Println(cfg.AutoStart)
	logChan := make(chan string, 100)
	supervisor := NewSupervisor(&cfg, logChan)

	// 1. 创建一个 Fyne 官方自带的、线程安全的字符串绑定器
	logBinding := binding.NewString()
	_ = logBinding.Set("")

	// 2. 创建一个纯文本 Label，并绑定这个数据源
	logLabel := widget.NewLabelWithData(logBinding)
	logLabel.Wrapping = fyne.TextWrapWord // 允许自动换行

	// 将 Label 塞入滚动容器
	logScroll := container.NewVScroll(logLabel)
	logScroll.SetMinSize(fyne.NewSize(800, 600))

	// 消费日志
	go func() {
		for text := range logChan {
			// 【防御拦截 1】如果 context 已经被 cancel 了（说明程序正在退出），
			// 立刻把残余日志倒进垃圾桶，绝对不再碰任何 UI 组件和数据绑定！
			if supervisor.ctx.Err() != nil {
				continue
			}

			// 拿到旧文本
			currentAll, _ := logBinding.Get()
			// 追加新文本
			if len(currentAll) > 100000 {
				currentAll = ""
			}
			_ = logBinding.Set(currentAll + text + "\n")

			// 驱动滚动条触底
			fyne.Do(func() {
				// 【防御拦截 2】在 UI 线程内部二次确认。
				// 如果滚动条组件已经或者正在被销毁，或者主窗口已经不可见，直接 return 闪人
				if logScroll == nil || supervisor.ctx.Err() != nil {
					return
				}

				// 只有在确定安全的情况下，才允许执行渲染层操作
				logScroll.ScrollToBottom()
			})
		}
	}()

	statusLabel := widget.NewLabel("当前状态: 未激活")
	status := false
	// 声明一个普通的互斥锁，不需要在 UI 线程里阻塞等待
	var btnMux sync.Mutex

	statusBtn := widget.NewButton("启动/关闭 OpenClaw", func() {
		// 1. 先用非阻塞的方式抢锁，防止用户疯狂连击按钮触发多线程撞车
		if !btnMux.TryLock() {
			return // 如果上一次操作还没完，直接无视本次点击
		}

		// 2. 核心：立刻开辟一个独立的后台后台协程去处理耗时的命令，解放 UI 线程
		go func() {
			// 无论如何，函数退出时必须释放锁
			defer btnMux.Unlock()

			if status {
				// 在后台默默执行 gateway stop 命令，卡 2 秒也绝不卡界面
				fyne.Do(func() {
					statusLabel.SetText("当前状态: 关闭中...")
				})
				supervisor.StopAutoStart()
				status = false

				// 耗时任务做完了，仅仅把“刷新界面”这一行代码安全地丢回 UI 线程
				fyne.Do(func() {
					statusLabel.SetText("当前状态: 已关闭")
				})
			} else {
				// 在后台默默激活保活和拉起命令
				fyne.Do(func() {
					statusLabel.SetText("当前状态: 启动中...")
				})
				supervisor.Start()
				status = true

				fyne.Do(func() {
					statusLabel.SetText("当前状态: 已启动")
				})
			}
		}()
	})

	controlTab := container.NewVBox(
		container.NewHBox(statusBtn),
		statusLabel,
		widget.NewLabel("运行日志:"),
		logScroll,
	)

	// 2. 配置标签页
	autoStartCheck := widget.NewCheck("开机自动启动", func(checked bool) {
		cfg.AutoStart = checked
		SaveConfig(cfg)
	})
	autoStartCheck.Checked = cfg.AutoStart

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(cfg.Port))

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(cfg.Interval))

	saveCfgBtn := widget.NewButton("保存配置", func() {
		p, _ := strconv.Atoi(portEntry.Text)
		i, _ := strconv.Atoi(intervalEntry.Text)
		cfg.Port = p
		cfg.Interval = i
		SaveConfig(cfg)
		logChan <- "[System] 配置已更新并持久化"
	})

	configTab := container.NewVBox(
		autoStartCheck,
		widget.NewForm(
			widget.NewFormItem("监测端口", portEntry),
			widget.NewFormItem("监测间隔 (秒)", intervalEntry),
		),
		saveCfgBtn,
	)

	// 标签页组装
	tabs := container.NewAppTabs(
		container.NewTabItem("监控面板", controlTab),
		container.NewTabItem("系统设置", configTab),
	)
	w.SetContent(tabs)

	a.Lifecycle().SetOnStopped(func() {
		log.Println("[Lifecycle] 检测到程序即将退出，开始执行清理...")
		fyne.DoAndWait(supervisor.Stop)
		log.Println("[Lifecycle] 清理完毕，程序安全退出。")
	})

	// 系统托盘管理
	if desk, ok := a.(desktop.App); ok {
		menu := fyne.NewMenu("控制台",
			fyne.NewMenuItem("显示主窗口", func() { w.Show() }),
		)
		desk.SetSystemTrayMenu(menu)

		// 1. 读取并创建通用的静态资源
		img, err := os.ReadFile("icon.png")
		if err == nil {
			resourceIconPng := fyne.NewStaticResource("LongXia", img)

			// 【换装 1】设置系统托盘图标
			desk.SetSystemTrayIcon(resourceIconPng)

			// 【换装 2】设置当前主窗口的左上角图标
			w.SetIcon(resourceIconPng)

			// 【换装 3】设置整个应用程序的默认图标（这会直接影响 Windows 任务栏的常驻图标）
			a.SetIcon(resourceIconPng)
		}
	}

	// 拦截关闭事件 -> 缩小到托盘
	w.SetCloseIntercept(func() {
		w.Hide()
		logChan <- "[System] 控制台已最小化至系统托盘"
	})

	// 初始化 IPC 防多开，传入被唤醒时的恢复逻辑
	initIPC(func() {
		w.Show()
		w.RequestFocus()
	})

	// 启动时读取上次持久化的运行状态
	if cfg.TargetStatus {
		statusLabel.SetText("当前状态: 启动中...")
		supervisor.Start()
		statusLabel.SetText("当前状态: 已启动")
		go func() {
			time.Sleep(300 * time.Millisecond)
			fyne.Do(w.Hide)
		}()
	}

	w.ShowAndRun()

}
