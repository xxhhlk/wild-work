package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wild-work/internal/monkeycode"
)

// writeCookieFile 在临时配置目录里落一份客户端 cookie 文件，并把该目录设为
// MONKEYCODE_CONFIG_DIR（导入器的首选候选）。其余候选（APPDATA/LOCALAPPDATA）
// 一并指向空目录，避免测试命中本机真实客户端。
func writeCookieFile(t *testing.T, file, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	t.Setenv("MONKEYCODE_CONFIG_DIR", dir)
	empty := t.TempDir()
	t.Setenv("APPDATA", empty)
	t.Setenv("LOCALAPPDATA", empty)
}

// TestMonkeyCodeCookie 两侧 Cookie 都是「尽力而为」：能取到就取，
// 取不到只给一句**原因**（由调用方决定怎么向用户表述）。
func TestMonkeyCodeCookie(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantVal string
		wantWhy string // 期望原因里的关键字（空表示取到了值）
	}{
		{
			name:    "valid",
			body:    `[{"name":"monkeycode_ai_session","value":"sess-abc","expires":"2099-01-01T00:00:00Z"}]`,
			wantVal: "sess-abc",
		},
		{
			name:    "no-expiry",
			body:    `[{"name":"monkeycode_ai_session","value":"sess-noexp"}]`,
			wantVal: "sess-noexp",
		},
		{
			name:    "expired",
			body:    `[{"name":"monkeycode_ai_session","value":"sess-old","expires":"2020-01-01T00:00:00Z"}]`,
			wantWhy: "已过期",
		},
		{
			name:    "other-cookie-only",
			body:    `[{"name":"baizhi_session","value":"x"}]`,
			wantWhy: "没有 monkeycode_ai_session",
		},
		{
			name:    "bad-json",
			body:    `not-json`,
			wantWhy: "解析失败",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeCookieFile(t, "monkeycode-cookies.json", tc.body)
			got, why := monkeyCodeCookie("monkeycode-cookies.json", monkeycode.CookieNameConsole)
			if got != tc.wantVal {
				t.Errorf("cookie=%q want %q", got, tc.wantVal)
			}
			if tc.wantWhy == "" && why != "" {
				t.Errorf("why=%q want empty", why)
			}
			if tc.wantWhy != "" && !strings.Contains(why, tc.wantWhy) {
				t.Errorf("why=%q want 含 %q", why, tc.wantWhy)
			}
		})
	}
}

// TestMonkeyCodeCookieBaizhi 百智云 Cookie 走**另一个文件**（baizhi-cookies.json），
// 两份文件同时存在时各取各的，不串。
func TestMonkeyCodeCookieBaizhi(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"monkeycode-cookies.json": `[{"name":"monkeycode_ai_session","value":"sess-console","expires":"2099-01-01T00:00:00Z"}]`,
		"baizhi-cookies.json":     `[{"name":"baizhi_session","value":"sess-baizhi","expires":"2099-01-01T00:00:00Z"}]`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("MONKEYCODE_CONFIG_DIR", dir)

	console, why := monkeyCodeCookie("monkeycode-cookies.json", monkeycode.CookieNameConsole)
	if console != "sess-console" || why != "" {
		t.Fatalf("console=%q why=%q want sess-console/空", console, why)
	}
	baizhi, why := monkeyCodeCookie("baizhi-cookies.json", monkeycode.CookieNameBaizhi)
	if baizhi != "sess-baizhi" || why != "" {
		t.Fatalf("baizhi=%q why=%q want sess-baizhi/空", baizhi, why)
	}
}

// TestMonkeyCodeCookieMissingFile 三处候选路径都找不到文件时，
// 返回空值 + 原因（不报错：Cookie 只是锦上添花，不该挡住凭据导入）。
func TestMonkeyCodeCookieMissingFile(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("MONKEYCODE_CONFIG_DIR", empty)
	t.Setenv("APPDATA", empty)
	t.Setenv("LOCALAPPDATA", empty)
	got, why := monkeyCodeCookie("monkeycode-cookies.json", monkeycode.CookieNameConsole)
	if got != "" || why != "文件缺失" {
		t.Fatalf("cookie=%q why=%q want 空值/文件缺失", got, why)
	}
}
