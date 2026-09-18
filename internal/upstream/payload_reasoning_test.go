// 思考强度投影测试：标准档位 → WorkBuddy 上游方言（low/high/max）。
package upstream

import (
	"encoding/json"
	"testing"
)

func projectOne(t *testing.T, src string) map[string]any {
	t.Helper()
	out := PrepareBody([]byte(src))
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return obj
}

func TestProjectReasoningDialectModels(t *testing.T) {
	// deepseek-v4-pro / flash：标准档位压到 low/high/max 三档
	cases := []struct{ in, want string }{
		{"minimal", "low"},
		{"low", "low"},
		{"medium", "high"},
		{"high", "high"},
		{"xhigh", "max"},
		{"max", "max"},
		{"ultra", "max"},
	}
	for _, tc := range cases {
		for _, model := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
			obj := projectOne(t, `{"model":"`+model+`","messages":[],"reasoning_effort":"`+tc.in+`"}`)
			if got := obj["reasoning_effort"]; got != tc.want {
				t.Errorf("%s + %s → %v, want %s", model, tc.in, got, tc.want)
			}
		}
	}
}

func TestProjectReasoningOtherModelsKeepStandardLevels(t *testing.T) {
	for _, level := range []string{"none", "minimal", "medium", "xhigh", "ultra"} {
		obj := projectOne(t, `{"model":"glm-5.2","messages":[],"reasoning_effort":"`+level+`"}`)
		if got := obj["reasoning_effort"]; got != level {
			t.Errorf("glm-5.2 + %s → %v, want %s", level, got, level)
		}
	}
}

// 未表达 / 关闭：不下发该字段（上游用「无字段」表示不启用思考）。
func TestProjectReasoningDropsFieldWhenDisabledOrAbsent(t *testing.T) {
	for _, src := range []string{
		`{"model":"deepseek-v4-pro","messages":[]}`,
		`{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"none"}`,
		`{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"off"}`,
		`{"model":"deepseek-v4-pro","messages":[],"thinking":{"type":"disabled"}}`,
		`{"model":"deepseek-v4-pro","messages":[],"disable_reasoning":true}`,
	} {
		obj := projectOne(t, src)
		if got, has := obj["reasoning_effort"]; has {
			t.Errorf("%s → 不应下发 reasoning_effort，实际 %v", src, got)
		}
	}
}

// 兼容写法（thinking.type / enable_thinking / reasoning.effort）最终都要落到方言字段。
func TestProjectReasoningAcceptsCompatibleForms(t *testing.T) {
	for _, src := range []string{
		`{"model":"deepseek-v4-pro","messages":[],"thinking":{"type":"enabled"}}`,
		`{"model":"deepseek-v4-pro","messages":[],"enable_thinking":true}`,
		`{"model":"deepseek-v4-pro","messages":[],"reasoning":{"effort":"high"}}`,
		`{"model":"deepseek-v4-pro","messages":[],"reasoningEffort":"high"}`,
	} {
		obj := projectOne(t, src)
		if got := obj["reasoning_effort"]; got != "high" {
			t.Errorf("%s → %v, want high", src, got)
		}
	}
}

// 非法取值原样保留：这类请求在 server 层已被 400 拦下（invalid_reasoning_control），
// 渠道层不做二次判定，也不因解析失败改动请求体。
func TestProjectReasoningLeavesInvalidUntouched(t *testing.T) {
	obj := projectOne(t, `{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"very-high"}`)
	if got := obj["reasoning_effort"]; got != "very-high" {
		t.Errorf("非法档位应原样保留，实际 %v", got)
	}
}
