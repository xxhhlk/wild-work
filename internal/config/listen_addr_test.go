package config

import "testing"

// TestSameAddr 覆盖「全部接口」各种写法与真正改地址的区分。
func TestSameAddr(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// 同一地址的不同写法（空 host / 0.0.0.0 / :: / * 都是全部接口）
		{":7863", "0.0.0.0:7863", true},
		{":7863", "[::]:7863", true},
		{"0.0.0.0:7863", "[::]:7863", true},
		{"*:7863", ":7863", true},
		// 完全相同的具体地址
		{"127.0.0.1:7863", "127.0.0.1:7863", true},
		{"[::1]:7863", "[::1]:7863", true},
		{"192.168.1.100:7863", "192.168.1.100:7863", true},
		// 真的改了监听范围或端口 → 必须重绑
		{"127.0.0.1:7863", "0.0.0.0:7863", false},
		{"127.0.0.1:7863", "127.0.0.1:7864", false},
		{":7863", ":7864", false},
		{"192.168.1.100:7863", "192.168.1.101:7863", false},
	}
	for _, c := range cases {
		if got := SameAddr(c.a, c.b); got != c.want {
			t.Errorf("SameAddr(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := SameAddr(c.b, c.a); got != c.want { // 必须对称
			t.Errorf("SameAddr(%q, %q) = %v, want %v", c.b, c.a, got, c.want)
		}
	}
}

// TestSameAddrLegacyEmptyHost 复现面板保存链路：旧版配置 host 为空（":7863"），
// 面板回显 0.0.0.0 后回传 "0.0.0.0:7863"，必须判定为同一地址，
// 否则「不改端口直接保存」会重新 net.Listen 同端口而报「端口已被占用」。
func TestSameAddrLegacyEmptyHost(t *testing.T) {
	legacy := Listen{Host: "", Port: 7863}
	fromPanel := Listen{Host: "0.0.0.0", Port: 7863}
	if !SameAddr(legacy.Addr(), fromPanel.Addr()) {
		t.Fatalf("SameAddr(%q, %q) 应为 true", legacy.Addr(), fromPanel.Addr())
	}
}
