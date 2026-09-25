package qoder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wild-work/internal/provider"
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
	raw, err := buildAgentBody(msgs, "dmodel", nil, reasoningSpec{}, 0)
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
	meta := modelMeta{
		Key: "qfmodel", DisplayName: "Qwen3.8-Flash", IsVL: true,
		MaxInputTokens: 180000, MaxOutputTokens: 32000, DefaultContextWindow: 200000,
	}
	raw, err := buildAgentBodyMeta(msgs, meta, nil, reasoningSpec{Enabled: true, Effort: "xhigh"}, pickContextWindow(meta, 0))
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
		// max_input_tokens 与 parameters.context_length 双写同步（上游 issue #27）
		"api_key": "", "url": "", "source": "system", "max_input_tokens": float64(200000),
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
	raw, err := buildAgentBody([]map[string]any{{"role": "user", "content": "hi"}}, "dmodel", nil, reasoningSpec{}, 0)
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
	raw, err := buildAgentBody(msgs, "qfmodel", nil, reasoningSpec{Enabled: true, Effort: "medium"}, 0)
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
	raw, _ = buildAgentBody(msgs, "qfmodel", nil, reasoningSpec{Enabled: false, Effort: "none"}, 0)
	body.Parameters, body.ModelConfig = nil, nil
	_ = json.Unmarshal(raw, &body)
	if body.ModelConfig["is_reasoning"] != false {
		t.Errorf("关闭时 model_config.is_reasoning = %v, want false", body.ModelConfig["is_reasoning"])
	}
	if body.Parameters["reasoning_effort"] != "none" || body.Parameters["enable_thinking"] != false {
		t.Errorf("关闭时 parameters = %v, want none/false", body.Parameters)
	}

	// 未表达档位：parameters 恒下发，reasoning_effort 不下发（不打扰上游默认档），
	// 但 enable_thinking **必须下发且为 false** —— 它是关闭思考的必要字段。
	// 依据：桌面版 A6e() 里 h 始终非空（至少写 max_tokens），且 enable_thinking
	// 与 is_reasoning 同源恒写；实测缺它时上游关不掉思考（见 live_probe 用例 1）。
	raw, _ = buildAgentBody(msgs, "qfmodel", nil, reasoningSpec{}, 0)
	var plain map[string]any
	_ = json.Unmarshal(raw, &plain)
	p, _ := plain["parameters"].(map[string]any)
	if p == nil {
		t.Fatalf("parameters 应恒下发，实际缺失")
	}
	if _, has := p["reasoning_effort"]; has {
		t.Errorf("未表达档位时不应下发 reasoning_effort，实际 %v", p)
	}
	if v, has := p["enable_thinking"]; !has || v != false {
		t.Errorf("未表达档位时 enable_thinking 必须为 false（关闭思考的必要字段），实际 has=%v v=%v", has, v)
	}
}

// mustAgentBody 构造请求体并解回 map（测试辅助）。
func mustAgentBody(t *testing.T, msgs []map[string]any, meta modelMeta) map[string]any {
	t.Helper()
	raw, err := buildAgentBodyMeta(msgs, meta, nil, reasoningSpec{}, pickContextWindow(meta, contextWindowFor(meta.ClientName)))
	if err != nil {
		t.Fatalf("buildAgentBodyMeta: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

// TestParseContextOptions 上游 context_config → 升序去重的档位列表。
func TestParseContextOptions(t *testing.T) {
	raw := json.RawMessage(`{"1M":{"token_count":1000000},"200K":{"token_count":200000,"is_default":true},"400K":{"token_count":400000}}`)
	got := parseContextOptions(raw)
	want := []int64{200000, 400000, 1000000}
	if len(got) != len(want) {
		t.Fatalf("档位 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("档位[%d] = %d, want %d（应升序）", i, got[i], want[i])
		}
	}
	if opts := parseContextOptions(nil); opts != nil {
		t.Errorf("无 context_config 应为空，得到 %v", opts)
	}
	if opts := parseContextOptions(json.RawMessage(`{}`)); len(opts) != 0 {
		t.Errorf("空对象应为空，得到 %v", opts)
	}
}

// TestPickContextWindow 按目标值就近取不超过它的最高档。
func TestPickContextWindow(t *testing.T) {
	meta := modelMeta{DefaultContextWindow: 200000, ContextOptions: []int64{200000, 400000, 1000000}}
	cases := []struct {
		target, want int64
		reason       string
	}{
		{0, 200000, "未选档位跟随上游默认"},
		{1000000, 1000000, "精确命中最大档"},
		{400000, 400000, "精确命中中间档"},
		{300000, 200000, "介于两档之间取下限档"},
		{100000, 200000, "低于最小档回落默认"},
	}
	for _, c := range cases {
		if got := pickContextWindow(meta, c.target); got != c.want {
			t.Errorf("target=%d: got %d, want %d（%s）", c.target, got, c.want, c.reason)
		}
	}
	if got := pickContextWindow(modelMeta{DefaultContextWindow: 200000}, 1000000); got != 200000 {
		t.Errorf("上游未声明档位时应回落默认档，得到 %d", got)
	}
}

// TestBuildAgentBodyHonorsContextWindow 逐模型配置的档位投影到 parameters.context_length，
// 且不改变 model_config.max_input_tokens（上游单次输入上限，另一字段）。
func TestBuildAgentBodyHonorsContextWindow(t *testing.T) {
	meta := modelMeta{
		Key: "qfmodel", DisplayName: "Qwen3.8-Flash", ClientName: "qwen3.8-flash", IsVL: true,
		MaxInputTokens: 180000, MaxOutputTokens: 32000,
		DefaultContextWindow: 200000, ContextOptions: []int64{200000, 400000, 1000000},
	}
	msgs := []map[string]any{{"role": "user", "content": "hi"}}
	defer SetContextWindows(nil)

	SetContextWindows(nil)
	body := mustAgentBody(t, msgs, meta)
	if got := body["parameters"].(map[string]any)["context_length"]; got != float64(200000) {
		t.Errorf("未配置档位应发上游默认档 200000，得到 %v", got)
	}
	// 上游 issue #27：context_length 与 model_config.max_input_tokens 双写
	// （只写 parameters 时 catalog 仍停在 180000，档位选了却不生效）。
	if got := body["model_config"].(map[string]any)["max_input_tokens"]; got != float64(200000) {
		t.Errorf("max_input_tokens 应与 context_length 同步为 200000，得到 %v", got)
	}

	SetContextWindows(map[string]int64{"qwen3.8-flash": 1000000})
	body = mustAgentBody(t, msgs, meta)
	if got := body["parameters"].(map[string]any)["context_length"]; got != float64(1000000) {
		t.Errorf("该模型选 1M 应发 1M，得到 %v", got)
	}

	// 逐模型生效：配置的是别的模型，本模型不受影响
	SetContextWindows(map[string]int64{"glm-5.3": 1000000})
	body = mustAgentBody(t, msgs, meta)
	if got := body["parameters"].(map[string]any)["context_length"]; got != float64(200000) {
		t.Errorf("未配置本模型时应发默认档 200000，得到 %v", got)
	}

	narrow := meta
	narrow.ContextOptions = []int64{200000, 400000}
	SetContextWindows(map[string]int64{"qwen3.8-flash": 1000000})
	body = mustAgentBody(t, msgs, narrow)
	if got := body["parameters"].(map[string]any)["context_length"]; got != float64(400000) {
		t.Errorf("模型不支持 1M 时应就近取 400K，得到 %v", got)
	}
}

// TestParseSceneModelsContextWindow 验证场景三级回退 + context_config 解析。
// 回归：旧实现只读 chat 场景（且缺字段时硬失败）、完全丢弃 context_config。
func TestParseSceneModelsContextWindow(t *testing.T) {
	// ① 只有 assistant 场景 → 三级回退命中（旧实现会报 "no chat scene"）
	raw := map[string]json.RawMessage{
		"assistant": json.RawMessage(`[
			{"key":"qmodel_38max","display_name":"Qwen3.8-Max","enable":true,"is_default":true,
			 "is_reasoning":true,"is_vl":true,"max_input_tokens":180000,"price_factor":0.5,
			 "format":"openai","source":"system",
			 "context_config":{"default":{"is_default":true,"token_count":200000},
			                   "1M":{"is_default":false,"token_count":1000000}}},
			{"key":"dmodel","display_name":"DeepSeek-V4-Pro","enable":true,"max_input_tokens":96000,
			 "price_factor":0.1,"context_config":[]},
			{"key":"off","display_name":"OFF","enable":false}
		]`),
	}
	ms, err := parseSceneModels(raw)
	if err != nil {
		t.Fatalf("parseSceneModels: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("enabled len = %d, want 2 (off 应被过滤)", len(ms))
	}
	// ② is_default 那项胜出，而不是 max_input_tokens 的 180000
	if ms[0].ContextWindow != 200000 {
		t.Errorf("is_default token_count not parsed: %+v", ms[0])
	}
	if ms[0].Format != "openai" || ms[0].Source != "system" {
		t.Errorf("format/source not parsed: %+v", ms[0])
	}
	// ③ 形状不符（数组）只损失本字段，不应让整批解析失败
	if ms[1].ContextWindow != 0 {
		t.Errorf("malformed context_config should degrade to 0: %+v", ms[1])
	}

	// ④ chat 缺失、developer 存在 → 回退 developer
	raw = map[string]json.RawMessage{
		"developer": json.RawMessage(`[{"key":"dk","display_name":"Dev","enable":true}]`),
	}
	if ms, err := parseSceneModels(raw); err != nil || len(ms) != 1 || ms[0].Key != "dk" {
		t.Errorf("developer fallback: %v %v", ms, err)
	}

	// ⑤ 三场景都空 → 报错
	raw = map[string]json.RawMessage{"chat": json.RawMessage(`[]`)}
	if _, err := parseSceneModels(raw); err == nil {
		t.Error("empty scenes should error")
	}

	// ⑥ assistant 优先于 chat
	raw = map[string]json.RawMessage{
		"chat":      json.RawMessage(`[{"key":"ck","enable":true}]`),
		"assistant": json.RawMessage(`[{"key":"ak","enable":true}]`),
	}
	if ms, err := parseSceneModels(raw); err != nil || len(ms) != 1 || ms[0].Key != "ak" {
		t.Errorf("assistant precedence: %v %v", ms, err)
	}
}

// TestBuildAgentBodyFormatSourceFromUpstream 验证 model_config 带上上游的 format/source。
// 回归：旧实现的 model_config 只有 {key,is_reasoning}，完全不下发 source
// （DEVELOPMENT.md §8 已记为 issue #32：旧 Qoder 思考过程不暴露）。
func TestBuildAgentBodyFormatSourceFromUpstream(t *testing.T) {
	body, err := buildAgentBodyMeta([]map[string]any{{"role": "user", "content": "hi"}},
		modelMeta{Key: "k1", Format: "up-format", Source: "up-source"}, nil, reasoningSpec{}, 0)
	if err != nil {
		t.Fatalf("buildAgentBody: %v", err)
	}
	var parsed struct {
		ModelConfig map[string]any `json:"model_config"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if parsed.ModelConfig["key"] != "k1" {
		t.Errorf("model_config.key = %v", parsed.ModelConfig["key"])
	}
	if parsed.ModelConfig["format"] != "up-format" {
		t.Errorf("model_config.format = %v, want upstream value", parsed.ModelConfig["format"])
	}
	if parsed.ModelConfig["source"] != "up-source" {
		t.Errorf("model_config.source = %v, want upstream value", parsed.ModelConfig["source"])
	}

	// 静态表兜底路径（mc == nil）→ 用兜底常量
	body, err = buildAgentBody([]map[string]any{{"role": "user", "content": "hi"}},
		"dmodel", nil, reasoningSpec{}, 0)
	if err != nil {
		t.Fatalf("buildAgentBody(nil entry): %v", err)
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body(nil entry): %v", err)
	}
	if parsed.ModelConfig["format"] != defaultModelFormat || parsed.ModelConfig["source"] != defaultModelSource {
		t.Errorf("nil entry should use fallback: %+v", parsed.ModelConfig)
	}
}

// TestBuildAgentBodyContextLength 验证 context_length 注入（issue #27）。
func TestBuildAgentBodyContextLength(t *testing.T) {
	mc := modelMeta{Key: "k", MaxInputTokens: 180000, DefaultContextWindow: 200000}
	raw, err := buildAgentBodyMeta([]map[string]any{{"role": "user", "content": "hi"}},
		mc, nil, reasoningSpec{}, 400000)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Parameters  map[string]any `json:"parameters"`
		ModelConfig map[string]any `json:"model_config"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Parameters["context_length"] != float64(400000) {
		t.Errorf("context_length = %v", body.Parameters["context_length"])
	}
	if body.ModelConfig["max_input_tokens"] != float64(400000) {
		t.Errorf("max_input_tokens = %v", body.ModelConfig["max_input_tokens"])
	}
	// window=0：parameters 仍恒下发（含 max_tokens / enable_thinking），但不得带 context_length
	raw, err = buildAgentBodyMeta([]map[string]any{{"role": "user", "content": "hi"}},
		mc, nil, reasoningSpec{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body2 struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &body2); err != nil {
		t.Fatal(err)
	}
	if _, has := body2.Parameters["context_length"]; has {
		t.Errorf("context_length should be absent when window=0: %v", body2.Parameters)
	}
	if body2.Parameters["enable_thinking"] != false {
		t.Errorf("parameters.enable_thinking 应恒下发为 false：%v", body2.Parameters)
	}
}

// TestResolveContextWindow TZ 校验 + 本仓默认最大档。
func TestResolveContextWindow(t *testing.T) {
	mc := &DynamicModel{Key: "m", MaxInputTokens: 180000, ContextWindow: 200000,
		AvailableWindows: []int64{200000, 400000, 1000000}}
	if got := resolveContextWindow(400000, mc); got != 400000 {
		t.Errorf("valid: got %d", got)
	}
	// 非法值与未指定 → 最大档（本仓默认，非官方 is_default）
	if got := resolveContextWindow(999, mc); got != 1000000 {
		t.Errorf("invalid → max: got %d, want 1000000", got)
	}
	if got := resolveContextWindow(0, mc); got != 1000000 {
		t.Errorf("default → max: got %d, want 1000000", got)
	}
}

// TestStreamClientHasNoTotalTimeout 守门：流式必须走**无总超时**的 client。
//
// 背景（2026-09-25 实测）：http.Client.Timeout 是整请求上限，计时器在 Do() 返回后继续跑
// 直到 body 读完；SSE 整个生成期都在读 body，故长思考请求会被从流中间掐断 ——
// 日志中断恰好 120.00s（= config.upstream.timeout_seconds），客户端表现为「突然无响应」。
// 非流式 client 必须保留总超时（短请求的合理兜底）。
func TestStreamClientHasNoTotalTimeout(t *testing.T) {
	c := New()
	if c.StreamHTTP == nil {
		t.Fatal("StreamHTTP 必须存在")
	}
	if c.StreamHTTP.Timeout != 0 {
		t.Fatalf("StreamHTTP.Timeout = %v，必须为 0（流式不能有整请求上限）", c.StreamHTTP.Timeout)
	}
	if c.HTTP == nil || c.HTTP.Timeout <= 0 {
		t.Fatalf("非流式 HTTP.Timeout = %v，必须 > 0", c.HTTP.Timeout)
	}
	// 无总超时后必须靠 ResponseHeaderTimeout 兜底，否则连响应头都等不到会无限挂住。
	tr, ok := c.StreamHTTP.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("StreamHTTP.Transport = %T，应为 *http.Transport", c.StreamHTTP.Transport)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Fatal("StreamHTTP 必须有 ResponseHeaderTimeout 兜底")
	}
	if tr.TLSNextProto == nil {
		t.Fatal("应强制 HTTP/1.1（禁 h2）")
	}
}

// TestIdleTimeoutDefaults 未注入配置时回落默认值（装配漏注入不应退化成「无兜底」）。
func TestIdleTimeoutDefaults(t *testing.T) {
	c := New()
	if got := c.idleTimeout(); got != DefaultIdleTimeout {
		t.Fatalf("idleTimeout() = %v, want %v", got, DefaultIdleTimeout)
	}
	c.IdleTimeout = 33 * time.Second
	if got := c.idleTimeout(); got != 33*time.Second {
		t.Fatalf("idleTimeout() = %v, want 33s", got)
	}
}

// TestStreamTruncationEmitsFrames 守门：流中断必须补 error 帧 + [DONE]。
//
// 这是「突然无响应」的直接成因 —— 此前 parseNestedSSE 报错时直接 return，
// 客户端收到一条没有 [DONE] 的截断流，只能一直等（或判定会话损坏）。
func TestStreamTruncationEmitsFrames(t *testing.T) {
	// 前半段正常，随后读错误（模拟空闲超时/连接被切断）。
	in := "data: " + `{"body":"{\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}"}` + "\n\n" +
		"data: " + `{"body":"{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"there\"}}]}"}` + "\n\n"
	rc := &errAfterReader{data: in, err: provider.ErrIdleTimeout}

	rec := httptest.NewRecorder()
	_, err := Stream(rec, rc, "qoder/qwen3.8-flash")
	if err == nil {
		t.Fatal("读错误必须上抛给 handler（用于日志）")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"upstream_timeout"`) {
		t.Fatalf("缺少 upstream_timeout 错误帧: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("缺少 [DONE] 收尾，客户端会一直等: %s", body)
	}
	// 已成功透传的部分不能丢。
	if !strings.Contains(body, "hi") || !strings.Contains(body, "there") {
		t.Fatalf("已透传内容丢失: %s", body)
	}
}

// errAfterReader 先吐完 data，再返回指定错误（模拟「读到一半流断了」）。
type errAfterReader struct {
	data string
	err  error
	off  int
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
