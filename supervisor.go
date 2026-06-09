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

	stopCmd := createSilentCmd("gateway", "stop")
	_ = stopCmd.Run()
}

func (s *Supervisor) StopAutoStart() {
	s.Stop()
	s.cfg.AutoStart = false
	s.cfg.TargetStatus = false
	SaveConfig(*s.cfg)
}

func (s *Supervisor) superviseLoop() {
	ticker := time.NewTicker(time.Duration(s.cfg.Interval) * time.Second)
	defer ticker.Stop()

	s.logChan <- "[Supervisor] 激活保活守护线程..."

	for {
		select {
		case <-s.ctx.Done():
			return
		default:

			address := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)

			// 增加拨号超时到 3 秒，防止 Node.js 后台太忙响应慢
			conn, err := net.DialTimeout("tcp", address, 3*time.Second)

			if err != nil {
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

					// 【核心改动】给 OpenClaw 6 秒钟的优雅启动时间（Startup Grace Period）
					// 在这 6 秒内，守护线程会静静等待它把 HTTP 服务完全跑起来，不重复拉起
					s.logChan <- "[Supervisor] 进入启动保护期，等待 Node.js 异步服务挂载..."
					time.Sleep(6 * time.Second)
				}
			} else {
				conn.Close() // 存活，正常
			}
		}

		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(time.Duration(s.cfg.Interval) * time.Second)
		}
	}
}

func (s *Supervisor) pipeLog(reader io.ReadCloser) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		s.logChan <- scanner.Text()
	}
}
