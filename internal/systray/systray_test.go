package systray

import (
	"testing"
	"time"
)

// 未启动托盘（--no-tray / 初始化失败）时 Quit 立即返回，不拖慢进程退出。
func TestQuitWithoutTray(t *testing.T) {
	start := time.Now()
	if !Quit(5 * time.Second) {
		t.Fatal("未启动托盘时 Quit 应返回 true")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("未启动托盘时不应等待，实际耗时 %v", d)
	}
}

// 消息循环未响应（图标回收未确认）时按超时返回 false，不阻塞退出。
func TestQuitTimesOutWithoutLoop(t *testing.T) {
	trayStarted.Store(true)
	t.Cleanup(func() { trayStarted.Store(false) })

	start := time.Now()
	if Quit(150 * time.Millisecond) {
		t.Fatal("消息循环未响应时应返回 false")
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("应等待到超时，实际 %v", d)
	}
}

// 图标回收完成后 Quit 不再等待（doneCh 是消息循环执行完 NIM_DELETE 的信号）。
func TestQuitReturnsOnDone(t *testing.T) {
	trayStarted.Store(true)
	doneOnce.Do(func() { close(doneCh) })
	t.Cleanup(func() { trayStarted.Store(false) })

	start := time.Now()
	if !Quit(5 * time.Second) {
		t.Fatal("回收完成后 Quit 应返回 true")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("回收完成后不应等待，实际耗时 %v", d)
	}
}
