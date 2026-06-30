package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type updateStatusJSON struct {
	Availability struct {
		Available     bool   `json:"available"`
		LatestVersion string `json:"latestVersion"`
	} `json:"availability"`
	Channel struct {
		Label string `json:"label"`
	} `json:"channel"`
	Update struct {
		InstallKind string `json:"installKind"`
	} `json:"update"`
}

// UpdatePanel 返回更新面板的 UI 组件
func UpdatePanel(w fyne.Window, logChan chan string) fyne.CanvasObject {
	statusLabel := widget.NewLabel("尚未检查")
	availableLabel := widget.NewLabel("")
	availableLabel.Hide()

	checkBtn := widget.NewButton("检查更新", nil)
	updateBtn := widget.NewButton("立即更新", nil)
	updateBtn.Hide()

	progressBar := widget.NewProgressBarInfinite()
	progressBar.Hide()

	// 禁用/启用按钮的辅助函数
	setBusy := func(busy bool) {
		if busy {
			checkBtn.Disable()
			updateBtn.Disable()
			progressBar.Show()
			progressBar.Start()
		} else {
			checkBtn.Enable()
			updateBtn.Enable()
			progressBar.Hide()
			progressBar.Stop()
		}
	}

	checkBtn.OnTapped = func() {
		setBusy(true)
		statusLabel.SetText("正在检查更新...")

		// 确保配置文件存在
		SaveConfig(LoadConfig())

		safeGo(w, logChan, "检查更新", func() {
			cmd := exec.Command("openclaw", "update", "status", "--json")
			output, err := cmd.Output()

			fyne.Do(func() {
				defer setBusy(false)

				if err != nil {
					safelog(logChan, fmt.Sprintf("[Update] 检查更新失败: %v", err))
					statusLabel.SetText(fmt.Sprintf("检查失败: %v", err))
					availableLabel.Hide()
					updateBtn.Hide()
					return
				}

				var status updateStatusJSON
				if err := json.Unmarshal(output, &status); err != nil {
					safelog(logChan, fmt.Sprintf("[Update] 解析结果失败: %v", err))
					statusLabel.SetText(fmt.Sprintf("解析结果失败: %v", err))
					availableLabel.Hide()
					updateBtn.Hide()
					return
				}

				if status.Availability.Available {
					statusLabel.SetText(fmt.Sprintf("当前版本: %s (%s通道)", status.Channel.Label, status.Update.InstallKind))
					availableLabel.SetText(fmt.Sprintf("🆕 发现新版本: %s", status.Availability.LatestVersion))
					availableLabel.Show()
					updateBtn.Show()
				} else {
					statusLabel.SetText(fmt.Sprintf("已是最新版本 (%s通道)", status.Channel.Label))
					availableLabel.Hide()
					updateBtn.Hide()
				}
			})
		})
	}

	updateBtn.OnTapped = func() {
		dialog.ShowConfirm("确认更新",
			fmt.Sprintf("检测到最新版本 %s，是否进行更新？\n\n更新期间请勿进行其他操作。", availableLabel.Text),
			func(confirmed bool) {
				if !confirmed {
					return
				}

				setBusy(true)

				safeGo(w, logChan, "执行更新", func() {
					defer fyne.Do(func() { setBusy(false) })

					statusLabel.SetText("正在更新 OpenClaw...")

					// --yes 跳过确认，--no-restart 不重启 gateway
					// 设置 npm 镜像以加速国内下载（openclaw update 内部走 npm）
					cmd := exec.Command("openclaw", "update", "--yes", "--no-restart")
					cmd.Env = append(os.Environ(),
						"npm_config_registry=https://registry.npmmirror.com")
					output, err := cmd.CombinedOutput()

					// 镜像失败则回退到默认源重试
					if err != nil {
						safelog(logChan, fmt.Sprintf("[Update] 镜像源失败: %v，回退到默认源重试...", err))
						cmd2 := exec.Command("openclaw", "update", "--yes", "--no-restart")
						output, err = cmd2.CombinedOutput()
					}

					fyne.Do(func() {
						if err != nil {
							safelog(logChan, fmt.Sprintf("[Update] 更新失败: %v\n%s", err, string(output)))
							statusLabel.SetText("更新失败，详情已记录到日志")
							dialog.ShowError(fmt.Errorf("更新失败: %v\n\n%s", err, string(output)), w)
						} else {
							safelog(logChan, "[Update] 更新完成")
							statusLabel.SetText("更新完成！")
							availableLabel.Hide()
							updateBtn.Hide()
							dialog.ShowInformation("更新完成",
								"OpenClaw 已成功更新到最新版本。\n\n建议重启 OpenClaw 服务以使更新生效。", w)
						}
					})
				})
			}, w)
	}

	return container.NewVBox(
		widget.NewLabel("OpenClaw 更新"),
		widget.NewSeparator(),
		checkBtn,
		statusLabel,
		availableLabel,
		updateBtn,
		progressBar,
	)
}
