package qoder

import (
	"encoding/json"
	"testing"

	"wild-work/internal/reasoning"
)

func TestNormalizeModelName(t *testing.T) {
	cases := map[string]string{
		"Qwen3.8-Max":      "qwen3.8-max",
		"DeepSeek-V4-Pro":  "deepseek-v4-pro",
		"GLM-5.3":          "glm-5.3",
		"Kimi-K2.7-Code":   "kimi-k2.7-code",
		"MiniMax-M2.7":     "minimax-m2.7",
		"Auto":             "auto",
		"Qwen3.8 Max Test": "qwen3.8-max-test",
	}
	for in, want := range cases {
		if got := NormalizeModelName(in); got != want {
			t.Errorf("NormalizeModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelKeyStatic(t *testing.T) {
	cases := map[string]string{
		"deepseek-v4-pro": "dmodel",
		"glm-5.3":         "gmodel",
		"glm-5.2":         "gm51model",
		"qwen3.8-max":     "qmodel_38max",
		"kimi-k2.7-code":  "kmodel",
		"unknown-model":   "",
	}
	for name, want := range cases {
		if got := ModelKey(name); got != want {
			t.Errorf("ModelKey(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestModelKeyDynamicPrecedence(t *testing.T) {
	c := New()
	c.setModelMap(
		map[string]string{"glm-5.3": "gmodel-new", "custom": "ckey"},
		map[string]modelMeta{"gmodel-new": {Key: "gmodel-new", DisplayName: "GLM-5.3"}},
	)
	if got := c.modelKey("glm-5.3"); got != "gmodel-new" {
		t.Errorf("dynamic should win, got %q", got)
	}
	if got := c.modelKey("custom"); got != "ckey" {
		t.Errorf("dynamic custom got %q", got)
	}
	if got := c.modelKey("deepseek-v4-pro"); got != "dmodel" { // 动态缺失 → 静态兜底
		t.Errorf("static fallback got %q", got)
	}
	// 元数据按上游 key 查；未命中返回只带 key 的零值（不猜窗口/档位）
	if m := c.modelMetaFor("gmodel-new"); m.DisplayName != "GLM-5.3" {
		t.Errorf("modelMetaFor hit = %#v", m)
	}
	if m := c.modelMetaFor("nope"); m.Key != "nope" || m.MaxOutputTokens != 0 || m.DefaultContextWindow != 0 {
		t.Errorf("modelMetaFor miss = %#v, want zero-value meta with key", m)
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	plain := []byte(`{"a":1,"b":"中文内容 😀","c":[true,null,3.14]}`)
	enc := qoderEncode(plain)
	if enc == "" {
		t.Fatal("empty encode")
	}
	dec, err := qoderDecode(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(dec) != string(plain) {
		t.Errorf("roundtrip mismatch:\n in=%s\nout=%s", plain, dec)
	}
}

func TestDecodeInvalidChar(t *testing.T) {
	if _, err := qoderDecode("!!!"); err == nil {
		t.Error("expected error for invalid char")
	}
}

func TestBuildAgentBodyDeveloperToSystem(t *testing.T) {
	// developer 角色应改写为 system；其余消息不受影响；原数据不被污染。
	msgs := []map[string]any{
		{"role": "developer", "content": "你是助手"},
		{"role": "system", "content": "保持简洁"},
		{"role": "user", "content": "你好"},
	}
	raw, err := buildAgentBody(msgs, "dmodel", nil, reasoningSpec{})
	if err != nil {
		t.Fatalf("buildAgentBody: %v", err)
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(body.Messages))
	}
	if body.Messages[0]["role"] != "system" {
		t.Errorf("developer should become system, got %v", body.Messages[0]["role"])
	}
	if body.Messages[1]["role"] != "system" {
		t.Errorf("system unchanged, got %v", body.Messages[1]["role"])
	}
	if body.Messages[2]["role"] != "user" {
		t.Errorf("user unchanged, got %v", body.Messages[2]["role"])
	}
	// 原数据不被污染
	if msgs[0]["role"] != "developer" {
		t.Errorf("source mutated: role=%v", msgs[0]["role"])
	}
	// prompt 取最后一条 user 内容（chat_context.text 是字符串，不是 text 块）
	var prompt struct {
		ChatContext struct {
			Text  string `json:"text"`
			Extra struct {
				OriginalContent string `json:"originalContent"`
			} `json:"extra"`
		} `json:"chat_context"`
	}
	_ = json.Unmarshal(raw, &prompt)
	if prompt.ChatContext.Text != "你好" {
		t.Errorf("chat_context.text = %q, want 你好", prompt.ChatContext.Text)
	}
	if prompt.ChatContext.Extra.OriginalContent != "你好" {
		t.Errorf("extra.originalContent = %q, want 你好", prompt.ChatContext.Extra.OriginalContent)
	}
}

// 顶层字段按桌面版实测形状对齐（_spy/http-bodies/*.json）：
// system 数组、task_id/source/version/is_retry、session_type=app、
// aliyun_user_type 空串、tools 恒为数组、parameters 恒下发。
func TestBuildAgentBodyWireShape(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": []any{
			map[string]any{"type": "text", "text": "块一"},
			map[string]any{"type": "text", "text": "块二"},
		}},
		{"role": "user", "content": "看看当前目录有什么文件"},
	}
	raw, err := buildAgentBodyMeta(msgs, modelMeta{
		Key: "qfmodel", DisplayName: "Qwen3.8-Flash", IsVL: true,
		MaxInputTokens: 180000, MaxOutputTokens: 32000, DefaultContextWindow: 200000,
	}, nil, reasoningSpec{Enabled: true, Effort: "xhigh"})
	if err != nil {
		t.Fatalf("buildAgentBodyMeta: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 标量字段
	want := map[string]any{
		"chat_task":        "FREE_INPUT",
		"is_reply":         true,
		"is_retry":         false,
		"source":           float64(1),
		"version":          "3",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"session_type":     "app",
		"aliyun_user_type": "",
		"stream":           true,
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %#v, want %#v", k, body[k], v)
		}
	}

	// chat_record_id 必须与 request_id 同值；request_set_id 是同一回合的稳定 id
	if body["chat_record_id"] != body["request_id"] {
		t.Errorf("chat_record_id(%v) != request_id(%v)", body["chat_record_id"], body["request_id"])
	}

	// system 数组：从 system 消息抽文本块，与 messages[0].content 同构
	sys, _ := body["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(sys))
	}
	if b0, _ := sys[0].(map[string]any); b0["type"] != "text" || b0["text"] != "块一" {
		t.Errorf("system[0] = %#v", sys[0])
	}

	// tools 恒为数组（空也要下发）
	if tools, ok := body["tools"].([]any); !ok || tools == nil || len(tools) != 0 {
		t.Errorf("tools = %#v, want empty array", body["tools"])
	}

	// parameters 恒下发，且带 max_tokens / context_length
	params, _ := body["parameters"].(map[string]any)
	if params == nil {
		t.Fatal("parameters 缺失")
	}
	if params["max_tokens"] != float64(32000) {
		t.Errorf("parameters.max_tokens = %v, want 32000", params["max_tokens"])
	}
	if params["context_length"] != float64(200000) {
		t.Errorf("parameters.context_length = %v, want 200000", params["context_length"])
	}
	if params["reasoning_effort"] != "xhigh" || params["enable_thinking"] != true {
		t.Errorf("parameters = %#v", params)
	}

	// model_config 完整对象
	mc, _ := body["model_config"].(map[string]any)
	for k, v := range map[string]any{
		"key": "qfmodel", "display_name": "Qwen3.8-Flash", "model": "",
		"format": "openai", "is_vl": true, "is_reasoning": true,
		"api_key": "", "url": "", "source": "system", "max_input_tokens": float64(180000),
	} {
		if mc[k] != v {
			t.Errorf("model_config.%s = %#v, want %#v", k, mc[k], v)
		}
	}

	// business：富对象，id 与 request_set_id 同值
	biz, _ := body["business"].(map[string]any)
	if biz == nil {
		t.Fatal("business 缺失")
	}
	if biz["product"] != "app" || biz["type"] != "agent" || biz["version"] != clientVersion {
		t.Errorf("business = %#v", biz)
	}
	if biz["id"] != body["request_set_id"] {
		t.Errorf("business.id(%v) != request_set_id(%v)", biz["id"], body["request_set_id"])
	}
	if biz["stage"] != "start" {
		t.Errorf("business.stage = %v, want start", biz["stage"])
	}
	if _, has := body["image_urls"]; has {
		t.Error("顶层 image_urls 不应存在（桌面端放在 chat_context.imageUrls）")
	}
}

// 模型元数据缺失时回落默认值：不猜窗口（不下发 context_length），
// max_tokens 用目录里 14 个模型一致的 32000。
func TestBuildAgentBodyMetaDefaults(t *testing.T) {
	raw, err := buildAgentBody([]map[string]any{{"role": "user", "content": "hi"}}, "dmodel", nil, reasoningSpec{})
	if err != nil {
		t.Fatalf("buildAgentBody: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	params, _ := body["parameters"].(map[string]any)
	if params == nil || params["max_tokens"] != float64(defaultMaxOutputTokens) {
		t.Fatalf("parameters = %#v, want max_tokens=%d", body["parameters"], defaultMaxOutputTokens)
	}
	if _, has := params["context_length"]; has {
		t.Errorf("窗口未知时不应下发 context_length，实际 %v", params["context_length"])
	}
	mc, _ := body["model_config"].(map[string]any)
	if mc["display_name"] != "dmodel" || mc["format"] != "openai" || mc["source"] != "system" {
		t.Errorf("model_config 默认值 = %#v", mc)
	}
	if mc["max_input_tokens"] != float64(defaultMaxInputTokens) {
		t.Errorf("max_input_tokens = %v, want %d", mc["max_input_tokens"], defaultMaxInputTokens)
	}
}

// context_config 解析：只认 is_default 标记，缺标记返回 0（不下发）。
func TestParseDefaultContextWindow(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{`{"200K":{"token_count":200000,"is_default":true},"400K":{"token_count":400000}}`, 200000},
		{`{"400K":{"token_count":400000,"is_default":true},"200K":{"token_count":200000}}`, 400000},
		{`{"200K":{"token_count":200000}}`, 0},
		{``, 0},
		{`null`, 0},
		{`{`, 0},
	}
	for _, tc := range cases {
		if got := parseDefaultContextWindow(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("parseDefaultContextWindow(%s) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// thinking_config 解析：efforts 是对象（键=档位名），默认档来自
// efforts 里标了 is_default 的那一项，disabled 节点表示支持显式关闭。
// 样本取自 2026-09-19 实测的上游目录。
func TestParseThinkingConfig(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		efforts  []string
		def      string
		canClose bool
	}{
		{
			name:     "qwen3.8 系（low/medium/xhigh，medium 默认，可关闭）",
			raw:      `{"disabled":{},"enabled":{"efforts":{"low":{},"medium":{"is_default":true},"xhigh":{}},"is_default":true}}`,
			efforts:  []string{"low", "medium", "xhigh"},
			def:      "medium",
			canClose: true,
		},
		{
			name:     "glm 系（low/high/max，max 默认，无 disabled 节点）",
			raw:      `{"enabled":{"efforts":{"high":{},"low":{},"max":{"is_default":true}},"is_default":true}}`,
			efforts:  []string{"low", "high", "max"},
			def:      "max",
			canClose: false,
		},
		{
			name:     "只有开关没有 ladder（可关闭）",
			raw:      `{"disabled":{"description":"Disable thinking"},"enabled":{"description":"Enable thinking","is_default":true}}`,
			canClose: true,
		},
		{name: "缺失", raw: ``},
		{name: "null", raw: `null`},
		{name: "非法 JSON", raw: `{`},
	}
	for _, tc := range cases {
		got := parseThinkingConfig(json.RawMessage(tc.raw))
		if len(got.Efforts) != len(tc.efforts) {
			t.Errorf("%s: Efforts = %v, want %v", tc.name, got.Efforts, tc.efforts)
		} else {
			for i := range tc.efforts {
				if got.Efforts[i] != tc.efforts[i] {
					t.Errorf("%s: Efforts = %v, want %v（须按强度升序）", tc.name, got.Efforts, tc.efforts)
					break
				}
			}
		}
		if got.DefaultEffort != tc.def {
			t.Errorf("%s: DefaultEffort = %q, want %q", tc.name, got.DefaultEffort, tc.def)
		}
		if got.SupportsDisable != tc.canClose {
			t.Errorf("%s: SupportsDisable = %v, want %v", tc.name, got.SupportsDisable, tc.canClose)
		}
	}
}

// 思考投影测试：官方 bve() 的写法是「开关 + 档位」两处同源下发，
// 且档位必须按该模型 ladder 就近降级、关闭意图要落在上游真认识的形态上。
//
// 测试用独立模型名，避免污染其它用例共享的能力表（reasoning.Caps 是进程级）。
func TestReasoningSpecForProjection(t *testing.T) {
	reasoning.Caps.SetRemote(reasoning.RealmQoder, map[string]reasoning.Cap{
		// qwen3.8 系：三档 + 可关闭
		"probe-ladder": {Efforts: []string{"low", "medium", "xhigh"}, DefaultEffort: "medium", SupportsDisable: true},
		// 只有 enabled、没有 disabled 节点（如 glm-5.3）：不可显式关闭
		"probe-nodisable": {Efforts: []string{"low", "high", "max"}, DefaultEffort: "max"},
		// 只有开关、没有 ladder（如 qwen3.7-max）
		"probe-switchonly": {SupportsDisable: true},
	})
	cases := []struct {
		name     string
		model    string
		effort   string
		thinking *thinkingParam
		wantOn   bool
		wantEff  string
	}{
		{"未表达", "probe-ladder", "", nil, false, ""},
		{"显式关闭（可关模型）", "probe-ladder", "none", nil, false, "none"},
		{"显式关闭 off（可关模型）", "probe-ladder", "off", nil, false, "none"},
		{"显式关闭（不可关模型→降最低档）", "probe-nodisable", "none", nil, true, "low"},
		{"显式关闭（只有开关的模型）", "probe-switchonly", "none", nil, false, "none"},
		{"显式关闭（能力未知→只关开关）", "probe-unknown", "none", nil, false, ""},
		{"指定档位命中", "probe-ladder", "medium", nil, true, "medium"},
		{"指定档位就近降级", "probe-ladder", "high", nil, true, "medium"},
		{"指定档位超上限", "probe-ladder", "ultra", nil, true, "xhigh"},
		{"指定档位低于下限", "probe-nodisable", "minimal", nil, true, "low"},
		{"只说开思考→补默认档", "probe-ladder", "", &thinkingParam{Type: "enabled"}, true, "medium"},
		{"adaptive→补默认档", "probe-ladder", "", &thinkingParam{Type: "adaptive"}, true, "medium"},
		{"只说开思考（能力未知）", "probe-unknown", "", &thinkingParam{Type: "enabled"}, true, ""},
		{"thinking disabled 兜底", "probe-ladder", "", &thinkingParam{Type: "disabled"}, false, "none"},
		{"effort 优先于 thinking", "probe-ladder", "none", &thinkingParam{Type: "enabled"}, false, "none"},
	}
	for _, tc := range cases {
		got := reasoningSpecFor(tc.model, tc.effort, tc.thinking)
		if got.Enabled != tc.wantOn || got.Effort != tc.wantEff {
			t.Errorf("%s: reasoningSpecFor(%q, %q, %v) = {on:%v effort:%q}, want {on:%v effort:%q}",
				tc.name, tc.model, tc.effort, tc.thinking, got.Enabled, got.Effort, tc.wantOn, tc.wantEff)
		}
		// 开关与 enable_thinking 必须同源：effort=none 时绝不能同时说「开着」。
		if got.Effort == "none" && got.Enabled {
			t.Errorf("%s: effort=none 却 Enabled=true（矛盾态）", tc.name)
		}
	}
}

// body 层：档位字段按官方写法落在 parameters，且与 model_config 同源。
func TestBuildAgentBodyReasoningFields(t *testing.T) {
	msgs := []map[string]any{{"role": "user", "content": "hi"}}

	// 有档位：parameters 同时写 reasoning_effort 与 enable_thinking。
	raw, err := buildAgentBody(msgs, "qfmodel", nil, reasoningSpec{Enabled: true, Effort: "medium"})
	if err != nil {
		t.Fatalf("buildAgentBody: %v", err)
	}
	var body struct {
		ModelConfig map[string]any `json:"model_config"`
		Parameters  map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ModelConfig["is_reasoning"] != true {
		t.Errorf("model_config.is_reasoning = %v, want true", body.ModelConfig["is_reasoning"])
	}
	if body.Parameters["reasoning_effort"] != "medium" {
		t.Errorf("parameters.reasoning_effort = %v, want medium", body.Parameters["reasoning_effort"])
	}
	if body.Parameters["enable_thinking"] != true {
		t.Errorf("parameters.enable_thinking = %v, want true", body.Parameters["enable_thinking"])
	}

	// 关闭：三处一致为 false。
	raw, _ = buildAgentBody(msgs, "qfmodel", nil, reasoningSpec{Enabled: false, Effort: "none"})
	body.Parameters, body.ModelConfig = nil, nil
	_ = json.Unmarshal(raw, &body)
	if body.ModelConfig["is_reasoning"] != false {
		t.Errorf("关闭时 model_config.is_reasoning = %v, want false", body.ModelConfig["is_reasoning"])
	}
	if body.Parameters["reasoning_effort"] != "none" || body.Parameters["enable_thinking"] != false {
		t.Errorf("关闭时 parameters = %v, want none/false", body.Parameters)
	}

	// 未表达档位：parameters 仍恒下发（只带 max_tokens），但不带思考字段。
	// 依据：桌面版 A6e() 里 h 始终非空，至少写 max_tokens。
	raw, _ = buildAgentBody(msgs, "qfmodel", nil, reasoningSpec{})
	var plain map[string]any
	_ = json.Unmarshal(raw, &plain)
	p, _ := plain["parameters"].(map[string]any)
	if p == nil {
		t.Fatalf("parameters 应恒下发，实际缺失")
	}
	if _, has := p["reasoning_effort"]; has {
		t.Errorf("未表达档位时不应下发 reasoning_effort，实际 %v", p)
	}
	if _, has := p["enable_thinking"]; has {
		t.Errorf("未表达档位时不应下发 enable_thinking，实际 %v", p)
	}
}
