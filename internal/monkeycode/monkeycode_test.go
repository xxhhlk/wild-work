package monkeycode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

// ---------------------------------------------------------------------------
// 签名（sign.go）
// ---------------------------------------------------------------------------

// TestSignFixedVector 固定向量：算法是
// `"v1=" + hex(HMAC-SHA256(secret, text))`。
// 期望值由独立实现（python hmac/hashlib）算出，用于捕获编码方式回归
// （漏 v1= 前缀、误用 base64、大小写 hex 等）。
func TestSignFixedVector(t *testing.T) {
	got := Sign("omas_test_secret", "You are a helpful assistant.")
	want := "v1=a62052ff54bed4f362590aeab65922c85ffefa88b455b71559895fd9c77b53ca"
	if got != want {
		t.Fatalf("签名不匹配：\n got=%s\nwant=%s", got, want)
	}
}

// TestSignPreservesCRLF 是本渠道最关键的回归点：官方提示词含 44 个 CRLF，
// 任何换行归一化都会让签名与内容不匹配 → 上游 403。
// 这里断言 `a\r\nb` 与 `a\nb` 得到**不同**签名，且 `a\r\nb` 的签名等于
// 独立实现按原字节算出的值。
func TestSignPreservesCRLF(t *testing.T) {
	crlf := Sign("omas_test_secret", "a\r\nb")
	lf := Sign("omas_test_secret", "a\nb")
	if crlf == lf {
		t.Fatal("CRLF 与 LF 得到相同签名：签名输入被归一化了（会让上游 403）")
	}
	if want := "v1=709a1e4a08a739619cd1b47e9cf7bf5b85f6c394259209e5ab7b93fa5bd4647b"; crlf != want {
		t.Fatalf("CRLF 输入签名不匹配：\n got=%s\nwant=%s", crlf, want)
	}
}

func TestSignatureForRequiresSecret(t *testing.T) {
	if _, err := signatureFor("   "); err == nil {
		t.Fatal("空 signing_secret 应当报错")
	}
	a, err := signatureFor("omas_test_secret")
	if err != nil {
		t.Fatalf("首次计算失败：%v", err)
	}
	b, err := signatureFor("omas_test_secret")
	if err != nil || a != b {
		t.Fatalf("缓存命中结果不一致：%v %q %q", err, a, b)
	}
}

// ---------------------------------------------------------------------------
// 请求投影（request.go）
// ---------------------------------------------------------------------------

func TestBuildAnthropicBody(t *testing.T) {
	in := `{
		"model":"basic/deepseek-flash",
		"stream":true,
		"messages":[
			{"role":"system","content":"你是客服"},
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"在的","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"结果文本"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"查","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],
		"tool_choice":"auto"
	}`
	out, err := buildAnthropicBody([]byte(in))
	if err != nil {
		t.Fatalf("buildAnthropicBody: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}

	// 模型名补回上游前缀
	if got["model"] != "monkeycode-basic/deepseek-flash" {
		t.Fatalf("model 期望 monkeycode-basic/deepseek-flash，实际 %v", got["model"])
	}
	// max_tokens 缺失时回默认
	if v, _ := got["max_tokens"].(float64); int64(v) != maxTokensDefault {
		t.Fatalf("max_tokens 期望默认 %d，实际 %v", maxTokensDefault, got["max_tokens"])
	}
	// thinking 恒 disabled
	th, _ := got["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Fatalf("thinking 期望 disabled，实际 %v", got["thinking"])
	}
	// system[0] 必须是签名对象，客户端 system 内容排在后面
	sys, _ := got["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("system 期望 2 条（签名对象 + 客户端 system），实际 %d", len(sys))
	}
	if s0, _ := sys[0].(map[string]any); s0["text"] != signatureSystemPrompt {
		t.Fatalf("system[0] 必须是签名对象，实际 %v", sys[0])
	}
	if s1, _ := sys[1].(map[string]any); s1["text"] != "你是客服" {
		t.Fatalf("system[1] 期望客户端 system 内容，实际 %v", sys[1])
	}
	// tool_use / tool_result 块
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages 期望 3 条（user/assistant/user(tool_result)），实际 %d", len(msgs))
	}
	asst, _ := msgs[1].(map[string]any)
	blocks, _ := asst["content"].([]any)
	foundToolUse := false
	for _, b := range blocks {
		if bm, ok := b.(map[string]any); ok && bm["type"] == "tool_use" {
			foundToolUse = true
			if in, _ := bm["input"].(map[string]any); in["q"] != "x" {
				t.Fatalf("tool_use.input 解析错误：%v", bm["input"])
			}
		}
	}
	if !foundToolUse {
		t.Fatal("assistant 的 tool_calls 未转成 tool_use 块")
	}
	last, _ := msgs[2].(map[string]any)
	lb, _ := last["content"].([]any)
	tr, _ := lb[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "call_1" {
		t.Fatalf("tool 消息未转成 tool_result 块：%v", tr)
	}
	// tools 定义
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools 期望 1 条，实际 %d", len(tools))
	}
	tl, _ := tools[0].(map[string]any)
	if tl["name"] != "lookup" || tl["input_schema"] == nil {
		t.Fatalf("tools 转换错误：%v", tl)
	}
}

func TestBuildAnthropicBodyMaxTokensClamp(t *testing.T) {
	out, err := buildAnthropicBody([]byte(`{"model":"basic/deepseek-flash","max_tokens":999999,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("buildAnthropicBody: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if v, _ := got["max_tokens"].(float64); int64(v) != maxTokensCap {
		t.Fatalf("max_tokens 期望夹到 %d，实际 %v", maxTokensCap, got["max_tokens"])
	}
}

// TestBuildAnthropicBodyForcesStream 出站**恒为 stream:true**。
//
// 渠道层契约：ChatStream 返回的一定是 SSE，非流式客户端由网关 Aggregate 收敛。
// 若按客户端的 stream:false 透传，上游会回单个 JSON 对象，被 SSE 转换器整包丢弃
// → HTTP 200 但 content 空、usage 0 的**静默失败**（2026-09-24 实测踩到）。
func TestBuildAnthropicBodyForcesStream(t *testing.T) {
	for _, in := range []string{
		`{"model":"basic/deepseek-flash","stream":false,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"basic/deepseek-flash","messages":[{"role":"user","content":"hi"}]}`,
	} {
		out, err := buildAnthropicBody([]byte(in))
		if err != nil {
			t.Fatalf("buildAnthropicBody: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("输出不是合法 JSON: %v", err)
		}
		if got["stream"] != true {
			t.Fatalf("出站 stream 必须强制为 true，实际 %v（入参 %s）", got["stream"], in)
		}
	}
}

// TestToolChoiceNoneDropsTools `tool_choice: "none"` 必须让整个 tools 字段消失。
//
// 两面统一按「裁掉 tools」处理：Anthropic 没有 none 语义，只发 tools 而不发
// tool_choice 时模型照样会调工具（VM 真实上游实测 finish_reason=tool_calls，
// 直接违反 OpenAI 契约）；Responses 面上游虽尊重 none，但统一处理可少依赖
// 一条上游行为。
func TestToolChoiceNoneDropsTools(t *testing.T) {
	const tools = `"tools":[{"type":"function","function":{"name":"get_weather","description":"d","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`

	builders := []struct {
		name string
		body func([]byte) ([]byte, error)
	}{
		{"anthropic", buildAnthropicBody},
		{"responses", buildResponsesBody},
	}

	for _, b := range builders {
		// none：tools 与 tool_choice 都必须消失
		in := `{"model":"basic/deepseek-flash","max_tokens":64,` + tools +
			`,"tool_choice":"none","messages":[{"role":"user","content":"hi"}]}`
		out, err := b.body([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", b.name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: 输出不是合法 JSON: %v", b.name, err)
		}
		if _, ok := got["tools"]; ok {
			t.Fatalf("%s: tool_choice=none 时 tools 必须被裁掉，实际仍在", b.name)
		}
		if _, ok := got["tool_choice"]; ok {
			t.Fatalf("%s: tool_choice=none 时不应下发 tool_choice", b.name)
		}

		// 对照组：auto / required / 指定函数 都必须保留 tools
		for _, tc := range []string{`"auto"`, `"required"`, `{"type":"function","function":{"name":"get_weather"}}`} {
			in := `{"model":"basic/deepseek-flash","max_tokens":64,` + tools +
				`,"tool_choice":` + tc + `,"messages":[{"role":"user","content":"hi"}]}`
			out, err := b.body([]byte(in))
			if err != nil {
				t.Fatalf("%s/%s: %v", b.name, tc, err)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("%s/%s: 输出不是合法 JSON: %v", b.name, tc, err)
			}
			if tl, _ := got["tools"].([]any); len(tl) != 1 {
				t.Fatalf("%s: tool_choice=%s 时 tools 必须保留，实际 %v", b.name, tc, got["tools"])
			}
		}
	}
}

// TestThinkingProjection 两面各自把顶层 `reasoning_effort` 投影成上游方言。
//
// anthropic 面只有开/关两态（`thinking.effort` 在该面**没有线上表达**，
// 抓包证实 low 与 high 产出的 body 完全相同）；responses 面的 `reasoning.effort`
// 有效（`none` 实测 reasoning_tokens=0），原样转发。
func TestThinkingProjection(t *testing.T) {
	cases := []struct {
		effort     string // 空串 = 客户端未表达
		wantThink  string // anthropic 面期望的 thinking.type
		wantReason string // responses 面期望的 reasoning.effort（空 = 不下发）
	}{
		{"", "disabled", ""},
		{"none", "disabled", "none"},
		{"low", "enabled", "low"},
		{"medium", "enabled", "medium"},
		{"high", "enabled", "high"},
		{"xhigh", "enabled", "xhigh"},
	}

	for _, tc := range cases {
		extra := ""
		if tc.effort != "" {
			extra = `,"reasoning_effort":"` + tc.effort + `"`
		}
		in := `{"model":"basic/deepseek-flash","max_tokens":64,` +
			`"messages":[{"role":"user","content":"hi"}]` + extra + `}`

		// anthropic 面
		out, err := buildAnthropicBody([]byte(in))
		if err != nil {
			t.Fatalf("anthropic/%q: %v", tc.effort, err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("anthropic/%q: 输出不是合法 JSON: %v", tc.effort, err)
		}
		th, _ := got["thinking"].(map[string]any)
		if th["type"] != tc.wantThink {
			t.Errorf("anthropic/%q: thinking.type = %v, want %v", tc.effort, th["type"], tc.wantThink)
		}
		// budget_tokens 恒不下发：实测对思考量无可测影响
		if _, ok := th["budget_tokens"]; ok {
			t.Errorf("anthropic/%q: 不应下发 budget_tokens", tc.effort)
		}

		// responses 面
		out, err = buildResponsesBody([]byte(in))
		if err != nil {
			t.Fatalf("responses/%q: %v", tc.effort, err)
		}
		var got2 map[string]any
		if err := json.Unmarshal(out, &got2); err != nil {
			t.Fatalf("responses/%q: 输出不是合法 JSON: %v", tc.effort, err)
		}
		rs, has := got2["reasoning"]
		if tc.wantReason == "" {
			if has {
				t.Errorf("responses/%q: 未表达时不应下发 reasoning，实际 %v", tc.effort, rs)
			}
			continue
		}
		rm, _ := rs.(map[string]any)
		if rm["effort"] != tc.wantReason {
			t.Errorf("responses/%q: reasoning.effort = %v, want %v", tc.effort, rm["effort"], tc.wantReason)
		}
	}
}

// ---------------------------------------------------------------------------
// 本地拒绝（client.go）
// ---------------------------------------------------------------------------

// TestUnknownModelRejectedLocally 未知模型必须本地拒绝：上游对未知模型会
// 静默回落到默认模型并返回 200，不校验就是静默烧额度。
// base 指向必然拒绝的端口，用来证明「没有发出上游请求」。
func TestUnknownModelRejectedLocally(t *testing.T) {
	c := NewWithBase("http://127.0.0.1:9/v1")
	_, status, body, err := c.ChatStream(&auth.Auth{AccessToken: "oma_x", SigningSecret: "omas_y"},
		[]byte(`{"model":"no-such-model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("未知模型不应返回传输错误：%v", err)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("未知模型期望 400，实际 %d", status)
	}
	if !strings.Contains(string(body), "model_not_found") {
		t.Fatalf("未知模型的错误体应含 model_not_found：%s", body)
	}
	// 缺 signing_secret 时必须报错（否则会拿空密钥签名）
	if _, _, _, err := c.ChatStream(&auth.Auth{AccessToken: "oma_x"},
		[]byte(`{"model":"basic/deepseek-flash","messages":[{"role":"user","content":"hi"}]}`)); err == nil {
		t.Fatal("缺 signing_secret 应当报错")
	}
}

func TestClassify(t *testing.T) {
	c := New()
	cases := []struct {
		status int
		body   string
		want   provider.ErrKind
	}{
		// 403 invalid ohmyagent request：签名/协议错误，绝不能当账号问题
		{http.StatusForbidden, `{"error":"invalid ohmyagent request"}`, provider.ErrPassthrough},
		{http.StatusUnauthorized, `{"error":"unauthorized"}`, provider.ErrSessionDead},
		{http.StatusTooManyRequests, `{}`, provider.ErrSoftRate},
		{http.StatusBadGateway, `{}`, provider.ErrServer},
		{http.StatusBadRequest, `{"error":{"code":"model_not_found"}}`, provider.ErrBadParams},
	}
	for _, tc := range cases {
		if got := c.Classify(tc.status, tc.body); got != tc.want {
			t.Errorf("Classify(%d, %s) = %v，期望 %v", tc.status, tc.body, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 响应投影（sse.go）
// ---------------------------------------------------------------------------

const anthropicFixture = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":12}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAnthropicStreamToOpenAI(t *testing.T) {
	raw, err := io.ReadAll(newAnthropicStream(strings.NewReader(anthropicFixture), "monkeycode/basic/deepseek-flash"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)

	for _, want := range []string{
		`"id":"msg_1"`,
		`"model":"monkeycode/basic/deepseek-flash"`,
		`"role":"assistant"`,
		`"content":"你好"`,
		`"finish_reason":"stop"`,
		`"prompt_tokens":12`,
		`"completion_tokens":5`,
		`"total_tokens":17`,
		`data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %s\n实际：\n%s", want, out)
		}
	}
	// 收尾帧只能出现一次
	if n := strings.Count(out, `"finish_reason":"stop"`); n != 1 {
		t.Errorf("收尾帧出现 %d 次，期望 1 次", n)
	}
}

func TestAnthropicStreamToolUse(t *testing.T) {
	fixture := `data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_9","name":"lookup"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}

`
	raw, err := io.ReadAll(newAnthropicStream(strings.NewReader(fixture), "monkeycode/pro/glm-5"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"tool_calls"`,
		`"id":"toolu_9"`,
		`"name":"lookup"`,
		`"arguments":"{\"q\":"`,
		`"finish_reason":"tool_calls"`,
		`data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %s\n实际：\n%s", want, out)
		}
	}
}

// TestAnthropicStreamTruncated 上游未发 message_stop 就断开时，
// 转换器必须补收尾帧 + [DONE]，否则客户端会一直挂着。
func TestAnthropicStreamTruncated(t *testing.T) {
	raw, err := io.ReadAll(newAnthropicStream(strings.NewReader(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"半句"}}`), "m"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	if !strings.Contains(out, "半句") || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("截断流未正确收尾：\n%s", out)
	}
}

// TestAnthropicJSONFallback 上游忽略 stream:true 而回单个 Anthropic Messages
// JSON 时，转换器必须整包转换，而不是把整份 JSON 当非 `data:` 行丢掉。
// 回归的是「HTTP 200 + 空 content + 0 usage」这种静默失败。
func TestAnthropicJSONFallback(t *testing.T) {
	fixture := `{"id":"msg_json","type":"message","role":"assistant",` +
		`"content":[{"type":"text","text":"PONG"}],` +
		`"stop_reason":"end_turn","usage":{"input_tokens":17,"output_tokens":2}}`
	raw, err := io.ReadAll(newAnthropicStream(strings.NewReader(fixture), "monkeycode/basic/deepseek-flash"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"id":"msg_json"`, `"content":"PONG"`, `"finish_reason":"stop"`,
		`"prompt_tokens":17`, `"completion_tokens":2`, `data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON 兜底输出缺少 %s\n实际：\n%s", want, out)
		}
	}
}

// TestAnthropicJSONFallbackToolUse 同一兜底路径下的 tool_use 块。
func TestAnthropicJSONFallbackToolUse(t *testing.T) {
	fixture := `{"id":"msg_t","type":"message","content":[` +
		`{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"x"}}],` +
		`"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":7}}`
	raw, err := io.ReadAll(newAnthropicStream(strings.NewReader(fixture), "m"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"tool_calls"`, `"id":"toolu_1"`, `"name":"lookup"`,
		`"arguments":"{\"q\":\"x\"}"`, `"finish_reason":"tool_calls"`, `data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON 兜底 tool_use 输出缺少 %s\n实际：\n%s", want, out)
		}
	}
}

// ---------------------------------------------------------------------------
// Responses 面（responses.go）
// ---------------------------------------------------------------------------

// TestUsesResponsesAPI 只有客户端 settings.json 里 type=openai-responses 的
// 4 个模型走 Responses 面；三种 ID 形态都要能识别。
func TestUsesResponsesAPI(t *testing.T) {
	yes := []string{
		"basic/qwen3.8-flash", "pro/gpt-5.6-terra",
		"ultra/gpt-5.6-sol", "ultra/gpt-6-astra",
		"monkeycode/basic/qwen3.8-flash",
		"monkeycode-ultra/gpt-6-astra",
	}
	for _, id := range yes {
		if !usesResponsesAPI(id) {
			t.Errorf("%s 应走 Responses 面", id)
		}
	}
	no := []string{"basic/deepseek-flash", "monkeycode/basic/deepseek-flash", "pro/glm-5", "ultra/glm-5.1"}
	for _, id := range no {
		if usesResponsesAPI(id) {
			t.Errorf("%s 不应走 Responses 面", id)
		}
	}
	// 25 个模型里恰好 4 个走 Responses 面
	n := 0
	for _, id := range staticModels {
		if usesResponsesAPI(id) {
			n++
		}
	}
	if n != 4 {
		t.Errorf("Responses 面模型数期望 4，实际 %d", n)
	}
}

// TestModelIDUsesAPINameNotDisplayName 锁定「对外 ID 必须是上游 API 名」。
//
// 客户端 settings.json 里 3 个模型的 key 是中文展示名（`专业模型-5.6-terra`），
// 但条目里的 `model` 字段是 ASCII 名（`gpt-5.6-terra`）。用中文展示名请求会被
// 上游回纯文本 `Forbidden`（2026-09-24 实测），所以 ID 必须取 ASCII 名，
// 中文只作展示名。
func TestModelIDUsesAPINameNotDisplayName(t *testing.T) {
	for _, id := range []string{"pro/gpt-5.6-terra", "ultra/gpt-5.6-sol", "ultra/gpt-6-astra"} {
		if !modelAllowed(id) {
			t.Errorf("%s 应当是可用的模型 ID", id)
		}
		if modelDisplayNames[id] == "" {
			t.Errorf("%s 缺少中文展示名", id)
		}
	}
	// 中文展示名不是合法 ID（上游会 403），必须被本地拒绝
	for _, bad := range []string{"pro/专业模型-5.6-terra", "ultra/极致模型-5.6-sol", "ultra/极致模型-6-astra"} {
		if modelAllowed(bad) {
			t.Errorf("%s 是展示名不是 API 名，不应被当作合法 ID", bad)
		}
	}
	// 展示名不得与 ID 相同（否则说明写错了）
	for id, name := range modelDisplayNames {
		if name == id {
			t.Errorf("%s 的展示名与 ID 相同", id)
		}
	}
	// 静态表里不应残留中文条目
	for _, id := range staticModels {
		for _, r := range id {
			if r > 127 {
				t.Errorf("静态表条目 %q 含非 ASCII 字符：对外 ID 必须用上游 API 名", id)
				break
			}
		}
	}
}

// TestBuildResponsesBody 签名对象必须是 input[0]（role=system）。
// 回归的是：把签名串放进 instructions / 顶层 system → 上游 403。
func TestBuildResponsesBody(t *testing.T) {
	in := `{
		"model":"basic/qwen3.8-flash",
		"messages":[
			{"role":"system","content":"你是客服"},
			{"role":"user","content":"你好"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]
	}`
	out, err := buildResponsesBody([]byte(in))
	if err != nil {
		t.Fatalf("buildResponsesBody: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	if got["model"] != "monkeycode-basic/qwen3.8-flash" {
		t.Fatalf("model 期望 monkeycode-basic/qwen3.8-flash，实际 %v", got["model"])
	}
	if got["stream"] != true {
		t.Fatalf("出站 stream 必须为 true，实际 %v", got["stream"])
	}
	if _, has := got["max_tokens"]; has {
		t.Fatal("Responses 面的长度上限字段是 max_output_tokens，不应出现 max_tokens")
	}
	if v, _ := got["max_output_tokens"].(float64); int64(v) != maxTokensDefault {
		t.Fatalf("max_output_tokens 期望默认 %d，实际 %v", maxTokensDefault, got["max_output_tokens"])
	}
	// 签名对象必须是 input[0]
	input, _ := got["input"].([]any)
	if len(input) < 3 {
		t.Fatalf("input 期望至少 3 条（签名对象 + system + user），实际 %d", len(input))
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != "system" || first["content"] != signatureSystemPrompt {
		t.Fatalf("input[0] 必须是签名对象，实际 %v", first)
	}
	// 不应出现 instructions / 顶层 system（上游不认，且会让签名校验失败）
	if _, has := got["instructions"]; has {
		t.Fatal("不应下发 instructions（上游不参与签名校验）")
	}
	if _, has := got["system"]; has {
		t.Fatal("不应下发顶层 system（上游不参与签名校验）")
	}
	// 工具定义用 Responses 形态
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools 期望 1 条，实际 %d", len(tools))
	}
	tl, _ := tools[0].(map[string]any)
	if tl["type"] != "function" || tl["name"] != "lookup" || tl["parameters"] == nil {
		t.Fatalf("tools 转换错误：%v", tl)
	}
}

// responsesFixture 取自 2026-09-24 对 monkeycode-basic/qwen3.8-flash 的真实抓包
// （事件顺序与字段均按原文，仅裁掉重复的 delta 与冗长元数据）。
const responsesFixture = `event: response.created
data: {"response":{"id":"cc628625-5f29-901f-bb3d-cc23317b88eb","created_at":1790191381,"model":"monkeycode-basic/qwen3.8-flash","status":"in_progress","usage":null},"sequence_number":0,"type":"response.created"}

event: response.in_progress
data: {"response":{"id":"cc628625-5f29-901f-bb3d-cc23317b88eb","status":"in_progress","usage":null},"sequence_number":1,"type":"response.in_progress"}

event: response.output_item.added
data: {"sequence_number":2,"type":"response.output_item.added","item":{"id":"rs_x","summary":[],"type":"reasoning","status":"in_progress"},"output_index":0}

event: response.reasoning_summary_text.delta
data: {"sequence_number":4,"type":"response.reasoning_summary_text.delta","delta":"The user said","item_id":"rs_x","output_index":0,"summary_index":0}

event: response.reasoning_summary_text.done
data: {"sequence_number":16,"type":"response.reasoning_summary_text.done","item_id":"rs_x","output_index":0,"summary_index":0,"text":"The user said"}

event: response.output_item.done
data: {"sequence_number":18,"type":"response.output_item.done","item":{"id":"rs_x","summary":[{"text":"The user said","type":"summary_text"}],"type":"reasoning","status":"completed"},"output_index":0}

event: response.output_item.added
data: {"sequence_number":19,"type":"response.output_item.added","item":{"content":[],"id":"msg_x","role":"assistant","type":"message","status":"in_progress"},"output_index":1}

event: response.content_part.added
data: {"sequence_number":20,"type":"response.content_part.added","content_index":0,"item_id":"msg_x","output_index":1,"part":{"type":"output_text","text":"","logprobs":[],"annotations":[]}}

event: response.output_text.delta
data: {"sequence_number":21,"type":"response.output_text.delta","content_index":0,"delta":"P","item_id":"msg_x","output_index":1,"logprobs":[]}

event: response.output_text.delta
data: {"sequence_number":22,"type":"response.output_text.delta","content_index":0,"delta":"ONG","item_id":"msg_x","output_index":1,"logprobs":[]}

event: response.output_text.done
data: {"sequence_number":23,"type":"response.output_text.done","content_index":0,"item_id":"msg_x","output_index":1,"text":"PONG","logprobs":[]}

event: response.output_item.done
data: {"sequence_number":25,"type":"response.output_item.done","item":{"content":[{"type":"output_text","text":"PONG","logprobs":[],"annotations":[]}],"id":"msg_x","role":"assistant","type":"message","status":"completed"},"output_index":1}

event: response.completed
data: {"response":{"id":"cc628625-5f29-901f-bb3d-cc23317b88eb","status":"completed","output":[],"usage":{"input_tokens":651,"input_tokens_details":{"cached_tokens":512},"output_tokens":131,"output_tokens_details":{"reasoning_tokens":129},"total_tokens":782}},"sequence_number":26,"type":"response.completed"}

`

func TestResponsesStreamToOpenAI(t *testing.T) {
	raw, err := io.ReadAll(newResponsesStream(strings.NewReader(responsesFixture), "monkeycode/basic/qwen3.8-flash"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"id":"cc628625-5f29-901f-bb3d-cc23317b88eb"`,
		`"model":"monkeycode/basic/qwen3.8-flash"`,
		`"role":"assistant"`,
		`"content":"P"`,
		`"content":"ONG"`,
		`"reasoning_content":"The user said"`,
		`"finish_reason":"stop"`,
		`"prompt_tokens":651`,
		`"completion_tokens":131`,
		`"total_tokens":782`,
		`data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %s\n实际：\n%s", want, out)
		}
	}
	if n := strings.Count(out, `"finish_reason":"stop"`); n != 1 {
		t.Errorf("收尾帧出现 %d 次，期望 1 次", n)
	}
}

// TestResponsesStreamFunctionCall 工具调用：output_item.added(function_call)
// 起头 + function_call_arguments.delta 累计入参。
func TestResponsesStreamFunctionCall(t *testing.T) {
	fixture := `data: {"type":"response.created","response":{"id":"r1","created_at":1}}

data: {"type":"response.output_item.added","item":{"id":"fc_1","call_id":"call_9","name":"lookup","type":"function_call","arguments":""},"output_index":0}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"q\":"}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"\"x\"}"}

data: {"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":3,"output_tokens":7}}}

`
	raw, err := io.ReadAll(newResponsesStream(strings.NewReader(fixture), "monkeycode/ultra/gpt-6-astra"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"tool_calls"`, `"id":"call_9"`, `"name":"lookup"`,
		`"arguments":"{\"q\":"`, `"arguments":"\"x\"}"`,
		`"finish_reason":"tool_calls"`, `data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %s\n实际：\n%s", want, out)
		}
	}
}

// TestResponsesJSONFallback 上游回单个 Responses JSON 时的兜底。
func TestResponsesJSONFallback(t *testing.T) {
	fixture := `{"id":"r_json","object":"response","status":"completed",` +
		`"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"想了想"}]},` +
		`{"type":"message","content":[{"type":"output_text","text":"PONG"}]}],` +
		`"usage":{"input_tokens":17,"output_tokens":2,"total_tokens":19}}`
	raw, err := io.ReadAll(newResponsesStream(strings.NewReader(fixture), "m"))
	if err != nil {
		t.Fatalf("读取转换结果失败：%v", err)
	}
	out := string(raw)
	for _, want := range []string{
		`"id":"r_json"`, `"reasoning_content":"想了想"`, `"content":"PONG"`,
		`"finish_reason":"stop"`, `"prompt_tokens":17`, `"completion_tokens":2`, `data: [DONE]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON 兜底输出缺少 %s\n实际：\n%s", want, out)
		}
	}
}

// ---------------------------------------------------------------------------
// 端到端（假上游）
// ---------------------------------------------------------------------------

// TestEndToEndAgainstFakeUpstream 用 httptest 假上游串起整条链路：
// ChatStream（OpenAI → Anthropic + 签名注入）→ Stream（Anthropic SSE → OpenAI SSE）。
// 断言三件事：
//  1. 请求头/路径/body 形态与官方客户端一致；
//  2. **签名等于对出站 system[0].text 用同一 secret 重算的结果**（这是上游唯一校验项）；
//  3. 流式输出能被客户端直接消费（含 usage 与 [DONE]）。
func TestEndToEndAgainstFakeUpstream(t *testing.T) {
	const secret = "omas_test_secret"
	var gotSig, gotKey, gotVer, gotUA, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("路径期望 /v1/messages，实际 %s", r.URL.Path)
		}
		gotSig = r.Header.Get(SignatureHeader)
		gotKey = r.Header.Get("X-Api-Key")
		gotVer = r.Header.Get("Anthropic-Version")
		gotUA = r.Header.Get("User-Agent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(anthropicFixture))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL + "/v1")
	a := &auth.Auth{AccessToken: "oma_test_key", SigningSecret: secret}
	rc, status, respBody, err := c.ChatStream(a, []byte(
		`{"model":"basic/deepseek-flash","stream":true,"messages":[{"role":"user","content":"你好"}]}`))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("状态期望 200，实际 %d body=%s", status, respBody)
	}
	defer rc.Close()

	if gotKey != "oma_test_key" {
		t.Errorf("X-Api-Key 未透传：%q", gotKey)
	}
	if gotVer != anthropicVersion {
		t.Errorf("Anthropic-Version 期望 %s，实际 %q", anthropicVersion, gotVer)
	}
	if gotUA != userAgent {
		t.Errorf("User-Agent 期望 %s，实际 %q", userAgent, gotUA)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("出站 body 不是合法 JSON：%v", err)
	}
	sys, _ := sent["system"].([]any)
	if len(sys) == 0 {
		t.Fatal("出站 body 缺少 system[0]（签名对象）")
	}
	s0, _ := sys[0].(map[string]any)["text"].(string)
	if want := Sign(secret, s0); gotSig != want {
		t.Fatalf("签名与出站 system[0].text 不匹配（上游会 403）：\n got=%s\nwant=%s", gotSig, want)
	}

	rec := httptest.NewRecorder()
	usage, err := c.Stream(rec, rc, "monkeycode/basic/deepseek-flash")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "你好") || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("流式输出不完整：\n%s", out)
	}
	if usage == nil {
		t.Fatal("未捕获到 usage（记账会记 0 token）")
	}
}
