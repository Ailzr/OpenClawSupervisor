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

		// 端口挂了，但先检查是否已有进程正在启动中
		if s.currentCmd != nil && s.currentCmd.Process != nil {
			// 有进程在跑，别急着拉起新的，等它自己起来
			s.logChan <- "[Supervisor] OpenClaw 进程仍在启动中，跳过本次拉起..."
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
			return
		}

		s.logChan <- "[Supervisor] OpenClaw 进程已在后台隐式建立"
		s.currentCmd = runCmd

		// 监听进程退出（runCmd.Wait() 会阻塞，扔到 goroutine 里）
		processExited := make(chan struct{})
		go func() {
			_ = runCmd.Wait()
			close(processExited)
		}()

		s.logChan <- "[Supervisor] 进入启动保护期，轮询等待服务挂载..."

		// 轮询：端口通了 → 成功 / 进程挂了 → 失败 / 超时 → 兜底放行
		maxWait := 120 * time.Second
		pollInterval := 3 * time.Second
		deadline := time.After(maxWait)
		pollTicker := time.NewTicker(pollInterval)
		defer pollTicker.Stop()

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-processExited:
				s.logChan <- "[Supervisor] OpenClaw 进程异常退出，启动失败"
				s.currentCmd = nil
				return
			case <-deadline:
				s.logChan <- "[Supervisor] 启动等待超时，进程可能仍在初始化，恢复正常监控"
				return
			case <-pollTicker.C:
				conn, err := net.DialTimeout("tcp", address, 2*time.Second)
				if err == nil {
					conn.Close()
					s.logChan <- "[Supervisor] OpenClaw 服务已成功挂载，恢复正常监控"
					return
				}
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
