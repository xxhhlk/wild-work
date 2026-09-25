package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCookieFile 在临时配置目录里落一份 monkeycode-cookies.json（客户端同形）。
func writeCookieFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "monkeycode-cookies.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	t.Setenv("MONKEYCODE_CONFIG_DIR", dir)
	return dir
}

// TestMonkeyCodeConsoleCookie 控制台 Cookie 是「尽力而为」：能取到就取，
// 取不到只给一句说明（不影响凭据导入本身）。
func TestMonkeyCodeConsoleCookie(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantVal  string
		wantNote string // 期望 note 里的关键字（空表示无 note）
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
			name:     "expired",
			body:     `[{"name":"monkeycode_ai_session","value":"sess-old","expires":"2020-01-01T00:00:00Z"}]`,
			wantNote: "已过期",
		},
		{
			name:     "other-cookie-only",
			body:     `[{"name":"baizhi_session","value":"x"}]`,
			wantNote: "没有 monkeycode_ai_session",
		},
		{
			name:     "bad-json",
			body:     `not-json`,
			wantNote: "解析",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeCookieFile(t, tc.body)
			got, note := monkeyCodeConsoleCookie()
			if got != tc.wantVal {
				t.Errorf("cookie=%q want %q", got, tc.wantVal)
			}
			if tc.wantNote == "" && note != "" {
				t.Errorf("note=%q want empty", note)
			}
			if tc.wantNote != "" && !strings.Contains(note, tc.wantNote) {
				t.Errorf("note=%q want 含 %q", note, tc.wantNote)
			}
		})
	}
}

// TestMonkeyCodeConsoleCookieMissingFile 三处候选路径都找不到文件时，
// 返回空值 + 说明（不报错：积分只是展示，不该挡住凭据导入）。
func TestMonkeyCodeConsoleCookieMissingFile(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("MONKEYCODE_CONFIG_DIR", empty)
	t.Setenv("APPDATA", empty)
	t.Setenv("LOCALAPPDATA", empty)
	got, note := monkeyCodeConsoleCookie()
	if got != "" || note == "" {
		t.Fatalf("cookie=%q note=%q want 空值 + 说明", got, note)
	}
}
