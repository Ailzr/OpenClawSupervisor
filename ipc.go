package main

import (
	"fmt"
	"net"
	"os"
)

const IPCPort = 38789 // 自身防多开的固定端口

func initIPC(onWakeup func()) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", IPCPort))
	if err != nil {
		// 端口被占用，说明已有实例在运行，发送信号唤起它
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", IPCPort))
		if err == nil {
			conn.Write([]byte("WAKEUP"))
			conn.Close()
		}
		os.Exit(0) // 退出当前实例
	}

	// 持续监听唤起信号
	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}
			buf := make([]byte, 10)
			n, _ := conn.Read(buf)
			if string(buf[:n]) == "WAKEUP" {
				onWakeup()
			}
			conn.Close()
		}
	}()
}
