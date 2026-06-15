package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

// safelog 写日志，不阻塞
func safelog(logChan chan string, msg string) {
	if logChan == nil {
		return
	}
	select {
	case logChan <- msg:
	default:
	}
}

// safeGo 在 goroutine 中执行 fn，自动捕获 panic 并弹窗 + 写日志
// onFinally 在主协程结束时一定执行（即使 panic），通常用于恢复按钮状态
func safeGo(w fyne.Window, logChan chan string, label string, fn func()) {
	go func() {
		panicked := false
		var panicVal interface{}

		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					panicVal = r
				}
			}()
			fn()
		}()

		if panicked {
			errMsg := fmt.Sprintf("[Panic] %s: %v", label, panicVal)
			safelog(logChan, errMsg)

			// 弹窗必须在 UI 线程
			if w != nil {
				fyne.Do(func() {
					dialog.ShowError(
						fmt.Errorf("未预期的错误 (%s)\n\n%v\n\n详细日志已记录，请查看监控面板。", label, panicVal),
						w,
					)
				})
			}
		}
	}()
}
