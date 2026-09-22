package workbuddyai

import (
	"net/http"
	"testing"

	"wild-work/internal/provider"
)

// TestClassifyPromptTooLong11115 守门：上下文超限（11115）是请求级错误——
// 必须判成 ErrPromptTooLong（不罚号、透传），且业务码走 provider.CodeMarker：
//   - 裸 Contains(lower,"11115") 太松：request_id 等任意含这五个数字的文本会误命中；
//   - 判定必须在 404 兜底（→ ErrNotFound 软冷却）之前：404 上的超限与账号健康无关。
func TestClassifyPromptTooLong11115(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   provider.ErrKind
	}{
		{"400 紧凑码", 400, `{"code":11115,"msg":"too long"}`, provider.ErrPromptTooLong},
		{"400 JSON 空白", 400, `{"code": 11115}`, provider.ErrPromptTooLong},
		{"400 字符串码", 400, `{"code":"11115"}`, provider.ErrPromptTooLong},
		{"404 上的超限不软冷却", http.StatusNotFound, `{"code":11115,"msg":"prompt is too long"}`, provider.ErrPromptTooLong},
		{"文案形态", 400, `prompt is too long: 300000 tokens`, provider.ErrPromptTooLong},
		{"request_id 误命中→保持罚号", 400, `{"code":400,"request_id":"a11115b7c3"}`, provider.ErrClient},
		{"前缀码不误命中", 400, `{"code":111150}`, provider.ErrClient},
	}
	for _, tc := range cases {
		if got := Classify(tc.status, tc.body); got != tc.want {
			t.Errorf("%s: Classify(%d, %s) = %v, want %v", tc.name, tc.status, tc.body, got, tc.want)
		}
	}
}
