//go:build windows

package systray

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

// notifyIconData 的内存布局必须与 energye/systray 内部的同名字段完全一致：
// Shell_NotifyIcon 按布局解释结构体，错位会**静默**失败（返回 FALSE，不报错）。
// 这里把大小与关键字段偏移钉死，升级库或改结构时立刻能发现。
func TestNotifyIconDataLayout(t *testing.T) {
	if got := unsafe.Sizeof(notifyIconData{}); got != 984 {
		t.Fatalf("notifyIconData 大小 = %d，期望 984（对齐 systray@v1.0.3）", got)
	}
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Wnd", unsafe.Offsetof(notifyIconData{}.Wnd), 8},
		{"ID", unsafe.Offsetof(notifyIconData{}.ID), 16},
		{"Flags", unsafe.Offsetof(notifyIconData{}.Flags), 20},
		{"Tip", unsafe.Offsetof(notifyIconData{}.Tip), 40},
		{"Info", unsafe.Offsetof(notifyIconData{}.Info), 304},
		{"InfoTitle", unsafe.Offsetof(notifyIconData{}.InfoTitle), 824},
		{"InfoFlags", unsafe.Offsetof(notifyIconData{}.InfoFlags), 952},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 偏移 = %d，期望 %d", c.name, c.got, c.want)
		}
	}
}

// copyUTF16 按码点写入并留 NUL 结尾，超长截断（不能按字节截，否则中文被劈半）。
func TestCopyUTF16(t *testing.T) {
	var buf [8]uint16
	copyUTF16(buf[:], "abcdefghij") // 10 字符 > 7，应截断到 7 + NUL
	if got := string(utf16.Decode(buf[:7])); got != "abcdefg" {
		t.Errorf("截断结果 = %q，期望 %q", got, "abcdefg")
	}
	if buf[7] != 0 {
		t.Errorf("末位应为 NUL，实际 %d", buf[7])
	}

	// 中文：4 个汉字需要 5 个 uint16（含 NUL），刚好放下
	var cn [5]uint16
	copyUTF16(cn[:], "中文中文")
	if got := string(utf16.Decode(cn[:4])); got != "中文中文" {
		t.Errorf("中文结果 = %q，期望 %q", got, "中文中文")
	}
	if cn[4] != 0 {
		t.Errorf("末位应为 NUL，实际 %d", cn[4])
	}

	// 缓冲区复用时旧内容必须被清掉，否则会带上一次通知的尾巴
	var reuse [8]uint16
	copyUTF16(reuse[:], "longertext")
	copyUTF16(reuse[:], "ab")
	if got := string(utf16.Decode(reuse[:8])); got != "ab\x00\x00\x00\x00\x00\x00" {
		t.Errorf("复用缓冲区残留旧内容：%q", got)
	}
}

// 托盘未启动时 notify 必须安全返回 false（不能 panic、不能命中别的进程的托盘窗口）。
func TestNotifyWithoutTrayFails(t *testing.T) {
	if notify("wild-work 测试", "托盘未启动") {
		t.Error("托盘未就绪时 notify 应返回 false")
	}
}

// Notify 是导出入口，托盘未启动时同样只返回 false（不阻塞、不弹窗）。
func TestNotifyIsNonBlockingWithoutTray(t *testing.T) {
	done := make(chan bool, 1)
	go func() { done <- Notify("t", "m") }()
	select {
	case ok := <-done:
		if ok {
			t.Error("托盘未就绪时 Notify 应返回 false")
		}
	case <-time.After(time.Second):
		t.Fatal("Notify 阻塞超过 1 秒 —— 模态弹窗会把启动卡住")
	}
}

// 通知文本里的换行与中文都应原样进入 UTF-16 缓冲（气泡支持 \n 换行）。
func TestCopyUTF16KeepsNewline(t *testing.T) {
	var buf [64]uint16
	copyUTF16(buf[:], "地址：http://127.0.0.1:38263\n点击托盘图标")
	s := string(utf16.Decode(buf[:]))
	if !strings.HasPrefix(s, "地址：http://") || !strings.Contains(s, "\n点击托盘图标") {
		t.Errorf("文本被改动：%q", s)
	}
}
