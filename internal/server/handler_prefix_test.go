// handler_prefix_test.go 守门：无前缀模型的报错文案必须列出**当前已配置的渠道**。
//
// 2026-09-23 发现该文案是手写字符串、已滞后于渠道表（漏了 workbuddyai / qodercom /
// raccoon / loomy），现改为按 Config.Runtimes 动态生成 —— 本测试守住
// 「新渠道加进 Runtimes 就自动出现在提示里」这条性质。
package server

import (
	"strings"
	"testing"

	"wild-work/internal/provider"
)

func TestPrefixHintListsConfiguredChannels(t *testing.T) {
	h := NewHandler(Config{
		Runtimes: map[provider.Kind]*Runtime{
			provider.WorkBuddy: {},
			provider.Raccoon:   {},
			provider.Loomy:     {},
		},
	})
	_, _, err := h.runtimeForModel("no-slash-here")
	if err == nil {
		t.Fatal("无前缀模型应报错")
	}
	msg := err.Error()
	for _, want := range []string{"workbuddy/<model>", "raccoon/<model>", "loomy/<model>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("提示缺少已配置渠道 %s：%s", want, msg)
		}
	}
	// 未配置的渠道不该出现在提示里（否则用户会照着试一个不存在的渠道）。
	if strings.Contains(msg, "qwenwork/<model>") {
		t.Fatalf("提示含未配置渠道：%s", msg)
	}
}

// TestPrefixHintEmptyRuntimes 兜底：Runtimes 为空时文案仍须给出通用占位，
// 不能退化成一句没有示例的干话（错误信息本身也是用户文档）。
func TestPrefixHintEmptyRuntimes(t *testing.T) {
	h := NewHandler(Config{})
	_, _, err := h.runtimeForModel("no-slash-here")
	if err == nil {
		t.Fatal("无前缀模型应报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "must use explicit prefix") {
		t.Fatalf("错误码文案变了（调用方按此串识别）：%s", msg)
	}
	if !strings.Contains(msg, "<channel>/<model>") {
		t.Fatalf("空 Runtimes 时应给通用占位：%s", msg)
	}
}
