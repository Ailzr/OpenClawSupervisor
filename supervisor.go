package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"syscall"
	"time"
)

type Supervisor struct {
	cfg        *AppConfig
	ctx        context.Context
	cancel     context.CancelFunc
	logChan    chan string
	isLooping  bool
	currentCmd *exec.Cmd
}

func NewSupervisor(cfg *AppConfig, logChan chan string) *Supervisor {
	return &Supervisor{cfg: cfg, logChan: logChan}
}

// 隐藏 Windows 的 CMD 窗口的核心命令封装
func createSilentCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("openclaw", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true, // 彻底静默执行，不弹黑框
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd
}

func (s *Supervisor) Start() {
	if s.isLooping {
		return
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.isLooping = true
	s.cfg.AutoStart = true
	s.cfg.TargetStatus = true
	SaveConfig(*s.cfg)

	go s.superviseLoop()
}

func (s *Supervisor) Stop() {
	if !s.isLooping {
		return
	}
	s.cancel()
	s.isLooping = false // 确保状态位同步重置
	SaveConfig(*s.cfg)

	// 【优化】先发日志（防止后面管道关闭发不进去），再执行耗时的同步停止命令
	select {
	case s.logChan <- "[Supervisor] 正在停止 OpenClaw...":
	default:
		// 如果通道堵塞或没人读（比如软件正在退出），直接忽略日志，防止死锁
	}

	go func() {
		stopCmd := createSilentCmd("gateway", "stop")
		_ = stopCmd.Run()
	}()
}

func (s *Supervisor) StopAutoStart() {
	s.Stop()
	s.cfg.AutoStart = false
	s.cfg.TargetStatus = false
	SaveConfig(*s.cfg)
}

func (s *Supervisor) superviseLoop() {
	// 初始化定时器，一进来先触发一次检测（通过设置一个已经过期的 ticker 或者手动先跑一次）
	interval := time.Duration(s.cfg.Interval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logChan <- "[Supervisor] 激活保活守护线程..."

	// 定义一个核心检测并尝试拉起的函数
	checkAndRun := func() {
		address := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
		conn, err := net.DialTimeout("tcp", address, 3*time.Second)
		if err == nil {
			conn.Close() // 存活，正常响应，直接返回
			return
		}

		// 端口未响应，执行拉起
		s.logChan <- fmt.Sprintf("[Supervisor] 检测到端口 %s 未响应，尝试拉起 OpenClaw...", address)
		runCmd := createSilentCmd("gateway", "run")

		stdout, err := runCmd.StdoutPipe()
		if err == nil {
			stderr, _ := runCmd.StderrPipe()
			go s.pipeLog(stdout)
			go s.pipeLog(stderr)
		}

		if err := runCmd.Start(); err != nil {
			s.logChan <- fmt.Sprintf("[Error] 拉起失败: %v", err)
		} else {
			s.logChan <- "[Supervisor] OpenClaw 进程已在后台隐式建立"
			s.currentCmd = runCmd

			s.logChan <- "[Supervisor] 进入启动保护期，等待 Node.js 异步服务挂载..."

			// 【关键点】直接利用 select 阻塞 6 秒，同时能响应上下文退出信号
			// 这样在 6 秒保护期内，绝对不会进入下一个 ticker 循环，也就绝不会重复拉起
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(30 * time.Second):
				s.logChan <- "[Supervisor] 启动保护期结束，恢复正常监控"
			}
		}
	}

	// 先执行一次首次检查
	checkAndRun()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// 每次定时器到了，才执行一次检查
			checkAndRun()
		}
	}
}

func (s *Supervisor) pipeLog(reader io.ReadCloser) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		s.logChan <- scanner.Text()
	}
}
