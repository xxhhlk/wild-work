//go:build windows

package systray

import (
	"log"
	"os"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Windows 气泡通知：复用 energye/systray 已注册的那个托盘图标。
//
// 为什么不用 MessageBox：那是**模态**对话框，会阻塞调用线程。启动阶段调用它会卡在
// 托盘创建之前 —— 双击 exe 启动时表现为「点了没反应、托盘图标迟迟不出现」，
// 直到用户点掉对话框托盘才出来（见 cmd/wild-work/main.go 的启动提示）。
//
// 为什么要自己拼 NOTIFYICONDATA：库（v1.0.3）没有暴露通知 API，而 Windows 的气泡
// 必须挂在**已注册的托盘图标**上（Shell_NotifyIcon + NIF_INFO）。库用类名
// SystrayClass 的隐藏窗口 + 图标 ID 100 注册，这里按同样的 (窗口, ID) 定位后发
// NIM_MODIFY。库若改了这两处常量，气泡会静默失效（Notify 返回 false，调用方自行
// 降级），不影响其它功能 —— 所以宁可失败也不做「兜底弹窗」。
const (
	trayWindowClass = "SystrayClass" // energye/systray 的窗口类名
	trayIconID      = 100            // energye/systray 的 nid.ID

	nimModify = 0x00000001 // NIM_MODIFY
	nifInfo   = 0x00000010 // NIF_INFO：本次修改带气泡内容
	niifInfo  = 0x00000001 // NIIF_INFO：信息类图标
)

// notifyIconData 必须与 energye/systray 内部的同名字段**逐字段同布局**：
// Shell_NotifyIcon 按内存布局解释该结构，字段错位会静默失败（返回 FALSE）。
// 布局由 notify_windows_test.go 的偏移断言守住。
type notifyIconData struct {
	Size                       uint32
	Wnd                        uintptr
	ID, Flags, CallbackMessage uint32
	Icon                       uintptr
	Tip                        [128]uint16
	State, StateMask           uint32
	Info                       [256]uint16
	Timeout, Version           uint32
	InfoTitle                  [64]uint16
	InfoFlags                  uint32
	GuidItem                   [16]byte // GUID
	BalloonIcon                uintptr
}

var (
	user32DLL  = syscall.NewLazyDLL("user32.dll")
	shell32DLL = syscall.NewLazyDLL("shell32.dll")

	procEnumWindows              = user32DLL.NewProc("EnumWindows")
	procGetClassNameW            = user32DLL.NewProc("GetClassNameW")
	procGetWindowThreadProcessID = user32DLL.NewProc("GetWindowThreadProcessId")
	procShellNotifyIconW         = shell32DLL.NewProc("Shell_NotifyIconW")
)

var (
	enumMu   sync.Mutex
	enumPID  uint32
	enumHwnd uintptr
	enumCb   = syscall.NewCallback(enumTrayWindow)
)

// enumTrayWindow 回调：命中「类名 == SystrayClass 且属于本进程」的窗口即停。
func enumTrayWindow(hwnd, _ uintptr) uintptr {
	var cls [64]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls)))
	if n == 0 || syscall.UTF16ToString(cls[:n]) != trayWindowClass {
		return 1
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != enumPID {
		return 1 // 同类名的其它进程（同款库的别的程序），跳过
	}
	enumHwnd = hwnd
	return 0
}

// findTrayWindow 定位托盘隐藏窗口；未创建（--no-tray 或尚未就绪）返回 0。
//
// 只按类名 FindWindow 会命中其它同样用 energye/systray 的进程，故必须枚举后
// 用 GetWindowThreadProcessId 校验 PID。窗口标题是空串（库的 windowName = ""），
// 无法靠标题区分。
func findTrayWindow() uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumPID, enumHwnd = uint32(os.Getpid()), 0
	procEnumWindows.Call(enumCb, 0)
	return enumHwnd
}

// notify Windows：在托盘图标上弹气泡通知，成功返回 true。
//
// 非阻塞：Shell_NotifyIcon 只是把气泡投给外壳（explorer），立刻返回。
func notify(title, msg string) bool {
	hwnd := findTrayWindow()
	if hwnd == 0 {
		return false // 托盘未就绪
	}
	var nid notifyIconData
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.Wnd = hwnd
	nid.ID = trayIconID
	nid.Flags = nifInfo
	nid.InfoFlags = niifInfo
	copyUTF16(nid.InfoTitle[:], title)
	copyUTF16(nid.Info[:], msg)

	res, _, err := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	if res == 0 {
		// 例如库改了窗口类名/图标 ID：调用方降级（日志里仍有地址），不弹模态框。
		log.Printf("systray: 气泡通知未送达（%v）", err)
		return false
	}
	return true
}

// copyUTF16 把 s 按**码点**写入定宽 UTF-16 缓冲区，保留末尾 NUL，超长截断。
//
// 按码点而非字节截断：这两个字段分别是 64 / 256 个 uint16（szInfoTitle / szInfo），
// 中文占 1 个 uint16 但 3 个字节，按字节截会把最后一个汉字劈成半个。
func copyUTF16(dst []uint16, s string) {
	if len(dst) == 0 {
		return
	}
	u := utf16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
	for i := len(u); i < len(dst); i++ {
		dst[i] = 0
	}
}
