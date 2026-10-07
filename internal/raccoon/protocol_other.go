//go:build !windows

// protocol_other.go 非 Windows 平台的退化实现。
//
// 协议回调接管依赖 HKCU\Software\Classes 的 URL Protocol 注册（Windows 专有机制），
// 当前仅在 Windows 实现并验证过；其他平台尚未实现，欢迎有对应设备的社区贡献者补齐——
// 调用方拿到 ErrProtocolUnsupported 后应引导用户改用「从本机客户端导入」。
package raccoon

// HijackProtocol 非 Windows 恒失败。
func HijackProtocol(exePath, backupFP string) error { return ErrProtocolUnsupported }

// RestoreProtocol 非 Windows 无注册表可改：恒成功且不改动任何东西。
func RestoreProtocol(backupFP string) (bool, error) { return false, nil }

// IsHijackedBy 非 Windows 恒 false。
func IsHijackedBy(exePath string) bool { return false }

// HasPendingHijack 非 Windows 恒 false。
func HasPendingHijack(stateDir string) bool { return false }

// CurrentProtocolCommand 非 Windows 恒空串。
func CurrentProtocolCommand() string { return "" }
