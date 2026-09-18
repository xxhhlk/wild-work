// 思考强度投影测试：按模型能力就近降级 + DeepSeek 思考开关/回填。
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

// 按模型能力降级：上游只认特定档位的模型不再被塞进非法档位。
func TestProjectReasoningClampByModelCapability(t *testing.T) {
	cases := []struct {
		model string
		in    string
		want  string
	}{
		// 国内版 deepseek-v4-pro：low/high/xhigh（无 max，medium 不在表内）
		{"deepseek-v4-pro", "minimal", "low"},
		{"deepseek-v4-pro", "low", "low"},
		{"deepseek-v4-pro", "medium", "low"},
		{"deepseek-v4-pro", "high", "high"},
		{"deepseek-v4-pro", "xhigh", "xhigh"},
		{"deepseek-v4-pro", "max", "xhigh"},
		{"deepseek-v4-pro", "ultra", "xhigh"},
		// 国内版 deepseek-v4-flash：low/high/max
		{"deepseek-v4-flash", "minimal", "low"},
		{"deepseek-v4-flash", "medium", "low"},
		{"deepseek-v4-flash", "xhigh", "high"},
		{"deepseek-v4-flash", "max", "max"},
		{"deepseek-v4-flash", "ultra", "max"},
		// 国内版 hy3：low/high
		{"hy3", "medium", "low"},
		{"hy3", "ultra", "high"},
		// 国内版 glm-5.1：只有 medium（请求档低于支持档时取最低支持档）
		{"glm-5.1", "low", "medium"},
		{"glm-5.1", "high", "medium"},
		{"glm-5.1", "medium", "medium"},
		// 国内版 glm-5.2：high/xhigh
		{"glm-5.2", "medium", "high"},
		{"glm-5.2", "max", "xhigh"},
		// 未收录模型：档位原样透传
		{"unknown-model", "ultra", "ultra"},
	}
	for _, tc := range cases {
		obj := projectOne(t, `{"model":"`+tc.model+`","messages":[],"reasoning_effort":"`+tc.in+`"}`)
		if got := obj["reasoning_effort"]; got != tc.want {
			t.Errorf("%s + %s → %v, want %s", tc.model, tc.in, got, tc.want)
		}
	}
}

// 未表达 / 关闭：不下发该字段（上游用「无字段」表示不启用思考）；
// DeepSeek 系还要把 thinking 一并删掉，避免残留开关又触发思考。
func TestProjectReasoningDropsFieldWhenDisabledOrAbsent(t *testing.T) {
	for _, src := range []string{
		`{"model":"deepseek-v4-pro","messages":[]}`,
		`{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"none"}`,
		`{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"off"}`,
		`{"model":"deepseek-v4-pro","messages":[],"thinking":{"type":"disabled"}}`,
		`{"model":"deepseek-v4-pro","messages":[],"disable_reasoning":true}`,
		`{"model":"glm-5.2","messages":[],"reasoning_effort":"none"}`,
		`{"model":"glm-5.2","messages":[],"disable_reasoning":true}`,
	} {
		obj := projectOne(t, src)
		if got, has := obj["reasoning_effort"]; has {
			t.Errorf("%s → 不应下发 reasoning_effort，实际 %v", src, got)
		}
		if _, has := obj["reasoningEffort"]; has {
			t.Errorf("%s → 不应下发 reasoningEffort", src)
		}
		if _, has := obj["thinking"]; has {
			t.Errorf("%s → 不应残留 thinking", src)
		}
	}
}

// 兼容写法（thinking.type / enable_thinking / reasoning.effort）最终都要落到同一字段。
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
		if _, has := obj["reasoningEffort"]; has {
			t.Errorf("%s → camel 字段应被收敛", src)
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

// DeepSeek 系开思考必须同时带 thinking:{type:"enabled"}（官方客户端行为），
// 非 DeepSeek 模型不加该字段；客户端显式给出的 type 一律不覆盖。
func TestProjectReasoningDeepseekThinkingSwitch(t *testing.T) {
	obj := projectOne(t, `{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"high"}`)
	th, ok := obj["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Fatalf("deepseek 开思考应补 thinking.type=enabled，实际 %v", obj["thinking"])
	}

	obj = projectOne(t, `{"model":"glm-5.2","messages":[],"reasoning_effort":"high"}`)
	if _, has := obj["thinking"]; has {
		t.Errorf("非 deepseek 模型不应注入 thinking，实际 %v", obj["thinking"])
	}

	obj = projectOne(t, `{"model":"deepseek-v4-pro","messages":[],"thinking":{"type":"adaptive","effort":"high"}}`)
	th, _ = obj["thinking"].(map[string]any)
	if th["type"] != "adaptive" {
		t.Errorf("客户端显式 thinking.type 不应被覆盖，实际 %v", th["type"])
	}

	// 开关关闭：回退旧行为（只发 reasoning_effort）
	SetDeepseekThinking(false)
	defer SetDeepseekThinking(true)
	obj = projectOne(t, `{"model":"deepseek-v4-pro","messages":[],"reasoning_effort":"high"}`)
	if _, has := obj["thinking"]; has {
		t.Errorf("开关关闭时不应注入 thinking，实际 %v", obj["thinking"])
	}
	if obj["reasoning_effort"] != "high" {
		t.Errorf("开关关闭时档位仍应下发，实际 %v", obj["reasoning_effort"])
	}
}

// 客户端只说开思考、没给档位 → 用该模型声明的默认档，而不是硬编码 high。
func TestProjectReasoningDefaultEffortFromCatalog(t *testing.T) {
	// deepseek-v4-pro 声明默认 high
	obj := projectOne(t, `{"model":"deepseek-v4-pro","messages":[],"thinking":{"type":"enabled"}}`)
	if got := obj["reasoning_effort"]; got != "high" {
		t.Errorf("deepseek-v4-pro 默认档应为 high，实际 %v", got)
	}
	// glm-5.1 未声明默认档 → 中性 high 再按能力降级到 medium
	obj = projectOne(t, `{"model":"glm-5.1","messages":[],"thinking":{"type":"enabled"}}`)
	if got := obj["reasoning_effort"]; got != "medium" {
		t.Errorf("glm-5.1 应降级到 medium，实际 %v", got)
	}
}

// DeepSeek 多轮一致性：assistant 消息都要带 string 类型的 reasoning_content。
func TestProjectReasoningBackfillsReasoningContent(t *testing.T) {
	src := `{"model":"deepseek-v4-pro","messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":"a"},` +
		`{"role":"assistant","content":"b","reasoning":"trace"},` +
		`{"role":"assistant","content":"c","reasoning_content":null}` +
		`],"reasoning_effort":"high"}`
	obj := projectOne(t, src)
	msgs, _ := obj["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("消息数变了: %d", len(msgs))
	}
	first, _ := msgs[1].(map[string]any)
	if rc, ok := first["reasoning_content"].(string); !ok || rc != "" {
		t.Errorf("无痕迹的 assistant 应补空串，实际 %v", first["reasoning_content"])
	}
	second, _ := msgs[2].(map[string]any)
	if rc, ok := second["reasoning_content"].(string); !ok || rc != "trace" {
		t.Errorf("有 reasoning 的 assistant 应复制该值，实际 %v", second["reasoning_content"])
	}
	third, _ := msgs[3].(map[string]any)
	if rc, ok := third["reasoning_content"].(string); !ok || rc != "" {
		t.Errorf("reasoning_content=null 应视为无并补空串，实际 %v", third["reasoning_content"])
	}

	// 非 DeepSeek 模型不动 messages
	obj = projectOne(t, `{"model":"glm-5.2","messages":[{"role":"assistant","content":"a"}],"reasoning_effort":"high"}`)
	msgs, _ = obj["messages"].([]any)
	if _, has := msgs[0].(map[string]any)["reasoning_content"]; has {
		t.Error("非 deepseek 模型不应回填 reasoning_content")
	}
}
