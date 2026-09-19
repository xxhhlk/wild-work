// 思考控制在内层的归一化与默认档注入（prepareChatBody / reasoningDefaultFor）。
package server

import (
	"encoding/json"
	"testing"

	"wild-work/internal/provider"
	"wild-work/internal/reasoning"
)

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return obj
}

func TestPrepareChatBodyNormalizesAndRewritesModel(t *testing.T) {
	raw, err := prepareChatBody([]byte(`{"model":"glm-5.2","messages":[],"reasoning_effort":"off"}`), "glm-5.2", "")
	if err != nil {
		t.Fatalf("prepareChatBody: %v", err)
	}
	obj := decodeBody(t, raw)
	if obj["model"] != "glm-5.2" {
		t.Errorf("model = %v", obj["model"])
	}
	if obj["reasoning_effort"] != "none" {
		t.Errorf("off 应归一化为 none，实际 %v", obj["reasoning_effort"])
	}
}

func TestPrepareChatBodyInjectsDefaultWhenUnspecified(t *testing.T) {
	raw, err := prepareChatBody([]byte(`{"model":"deepseek-v4-pro","messages":[]}`), "deepseek-v4-pro", "max")
	if err != nil {
		t.Fatalf("prepareChatBody: %v", err)
	}
	if got := decodeBody(t, raw)["reasoning_effort"]; got != "max" {
		t.Errorf("未表达时应注入默认档，实际 %v", got)
	}
}

// 客户端显式关闭思考时，默认档不得把它复活。
func TestPrepareChatBodyDefaultDoesNotOverrideExplicitDisable(t *testing.T) {
	for _, src := range []string{
		`{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"none"}`,
		`{"model":"deepseek-v4-pro","messages":[],"thinking":{"type":"disabled"}}`,
		`{"model":"deepseek-v4-pro","messages":[],"disable_reasoning":true}`,
	} {
		raw, err := prepareChatBody([]byte(src), "deepseek-v4-pro", "high")
		if err != nil {
			t.Fatalf("prepareChatBody(%s): %v", src, err)
		}
		if got := decodeBody(t, raw)["reasoning_effort"]; got != "none" {
			t.Errorf("%s → %v, want none", src, got)
		}
	}
}

func TestPrepareChatBodyWithoutDefaultLeavesFieldAbsent(t *testing.T) {
	raw, err := prepareChatBody([]byte(`{"model":"glm-5.2","messages":[]}`), "glm-5.2", "")
	if err != nil {
		t.Fatalf("prepareChatBody: %v", err)
	}
	if got, has := decodeBody(t, raw)["reasoning_effort"]; has {
		t.Errorf("默认档为空时不应注入，实际 %v", got)
	}
}

func TestPrepareChatBodyRejectsInvalidControl(t *testing.T) {
	for _, src := range []string{
		`{"model":"glm-5.2","messages":[],"reasoning_effort":"very-high"}`,
		`{"model":"glm-5.2","messages":[],"reasoning":{"effort":"high","enabled":false}}`,
		`{"model":"glm-5.2","messages":[],"thinking":{"type":"maybe"}}`,
	} {
		if _, err := prepareChatBody([]byte(src), "glm-5.2", ""); !reasoning.IsInvalid(err) {
			t.Errorf("%s 期望 InvalidError，实际 %v", src, err)
		}
	}
}

// 默认档对 WorkBuddy 双面与 Qoder 生效：这三条渠道都有可验证的档位字段。
// Qoder 的档位来自模型目录的 thinking_config，能不能用由渠道层按模型能力
// 就近降级（Clamp）决定，所以这里照常注入。TraeWork 协议没有该字段。
func TestReasoningDefaultFor(t *testing.T) {
	h := NewHandler(Config{Runtimes: map[provider.Kind]*Runtime{}})
	h.SetReasoningEffort("high")
	cases := map[provider.Kind]string{
		provider.WorkBuddy:   "high",
		provider.WorkBuddyAI: "high",
		provider.Qoder:       "high",
		provider.TraeWork:    "",
	}
	for kind, want := range cases {
		if got := h.reasoningDefaultFor(kind); got != want {
			t.Errorf("reasoningDefaultFor(%s) = %q, want %q", kind, got, want)
		}
	}
	h.SetReasoningEffort("")
	if got := h.reasoningDefaultFor(provider.WorkBuddy); got != "" {
		t.Errorf("清空后应返回空串，实际 %q", got)
	}
	if got := h.reasoningDefaultFor(provider.Qoder); got != "" {
		t.Errorf("清空后 Qoder 也应返回空串，实际 %q", got)
	}
}
