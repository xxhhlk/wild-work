//go:build !windows

package systray

import "wild-work/internal/platform"

// notify 非 Windows：沿用平台系统通知（macOS 走 osascript 的 display notification，
// 其余平台写 stderr）。这些实现本身就不阻塞，无需气泡。
//
// 返回 false：本平台没有「托盘气泡」这条路径（见 notify_windows.go 的约定）。
func notify(title, msg string) bool {
	platform.Notify(title, msg)
	return false
}
