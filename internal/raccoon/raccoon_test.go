package raccoon

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

// TestClassify 覆盖阶段 C 实测到的全部错误形态（LiteLLM 信封）。
func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   provider.ErrKind
	}{
		{"401 空 token", 401, `{"code":200001,"message":"authorization_empty_error"}`, provider.ErrSessionDead},
		{"401 token 校验失败", 401, `{"code":200003,"message":"authorization_verify_error"}`, provider.ErrSessionDead},
		{"403 也按会话失效", 403, `{"code":200003}`, provider.ErrSessionDead},
		{"本地拒绝的未知模型", 400, `{"error":{"code":"model_not_found"}}`, provider.ErrBadParams},
		{"400 空消息/超长", 400, `{"error":{"code":"400","message":"litellm.BadRequestError: message is empty or message is bigger than 70MB"}}`, provider.ErrPromptTooLong},
		{"429 纯限流", 429, `{"error":{"message":"rate limit exceeded"}}`, provider.ErrSoftRate},
		{"429 带 quota 字样仍按限流（§6.15）", 429, `{"code":200001,"message":"quota exceeded"}`, provider.ErrSoftRate},
		{"402 余额不足", 402, `{"message":"insufficient credit"}`, provider.ErrHardCredit},
		{"404", 404, `404 page not found`, provider.ErrNotFound},
		{"500", 500, `internal error`, provider.ErrServer},
		{"成功", 200, `{"id":"x"}`, provider.ErrNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.status, tc.body); got != tc.want {
				t.Fatalf("Classify(%d, %s) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestChatStreamRejectsUnknownModel 守门：上游对未知模型会静默回落默认模型，
// 渠道必须本地拒绝，否则用户写错模型名会被静默计费。
func TestChatStreamRejectsUnknownModel(t *testing.T) {
	c := New()
	a := &auth.Auth{AccessToken: "dummy"}
	_, status, body, err := c.ChatStream(a, []byte(`{"model":"no-such-model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 400 {
		t.Fatalf("status = %d, want 400", status)
	}
	if !strings.Contains(string(body), "model_not_found") {
		t.Fatalf("body 应含 model_not_found，实际: %s", body)
	}
}

func TestKnownModel(t *testing.T) {
	for _, id := range []string{DefaultModel, ClientAliasModel, "sn-kimi-k3", "raccoon-405a1c"} {
		if !KnownModel(id) {
			t.Fatalf("KnownModel(%q) = false, want true", id)
		}
	}
	if KnownModel("nope-xyz") {
		t.Fatal("KnownModel(未知) 应为 false")
	}
}

func TestStaticModelsNonEmpty(t *testing.T) {
	ms := StaticModels()
	if len(ms) != len(staticModels) {
		t.Fatalf("StaticModels 应返回副本，got %d", len(ms))
	}
	ms[0].ID = "mutated"
	if staticModels[0].ID == "mutated" {
		t.Fatal("StaticModels 必须返回副本，不能共享底层数组")
	}
}

// sse 构造一条 data 行。
func sseLine(v any) string {
	raw, _ := json.Marshal(v)
	return "data: " + string(raw) + "\n\n"
}

func TestStreamRewritesModelAndEmitsDone(t *testing.T) {
	in := sseLine(map[string]any{
		"id": "a", "object": "chat.completion.chunk", "model": "upstream-name",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}}},
	}) + "data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	if err := Stream(rec, strings.NewReader(in), "raccoon/sn-kimi-k3"); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"model":"raccoon/sn-kimi-k3"`) {
		t.Fatalf("model 未被重写为客户端模型名: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("缺少 [DONE]: %s", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

// TestStreamAppendsDoneWhenUpstreamOmitsIt 覆盖上游无 [DONE] 的异常收尾。
func TestStreamAppendsDoneWhenUpstreamOmitsIt(t *testing.T) {
	in := sseLine(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "x"}}}})
	rec := httptest.NewRecorder()
	if err := Stream(rec, strings.NewReader(in), "m"); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("应在流末尾补 [DONE]: %s", rec.Body.String())
	}
}

func TestAggregateMergesDeltaAndReasoning(t *testing.T) {
	in := sseLine(map[string]any{
		"id": "id-1", "created": 1700000000, "model": "upstream",
		"choices": []any{map[string]any{"index": 0,
			"delta": map[string]any{"role": "assistant", "reasoning_content": "think"}}},
	}) +
		sseLine(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "he"}}}}) +
		sseLine(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "llo"}}}}) +
		sseLine(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}) +
		sseLine(map[string]any{"usage": map[string]any{"total_tokens": 7}}) +
		"data: [DONE]\n\n"

	out, err := aggregate(strings.NewReader(in), "raccoon/x")
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if out["model"] != "raccoon/x" {
		t.Fatalf("model = %v", out["model"])
	}
	if out["object"] != "chat.completion" {
		t.Fatalf("object = %v", out["object"])
	}
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Fatalf("content = %v, want hello", msg["content"])
	}
	if msg["reasoning_content"] != "think" {
		t.Fatalf("reasoning_content = %v", msg["reasoning_content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v", choices[0].(map[string]any)["finish_reason"])
	}
	if _, ok := out["usage"]; !ok {
		t.Fatal("usage 应被带上")
	}
}

func TestAggregateMergesToolCallArguments(t *testing.T) {
	delta1 := map[string]any{"tool_calls": []any{
		map[string]any{
			"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "f", "arguments": `{"a":`},
		},
	}}
	delta2 := map[string]any{"tool_calls": []any{
		map[string]any{
			"index":    0,
			"function": map[string]any{"arguments": "1}"},
		},
	}}
	in := sseLine(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta1}}}) +
		sseLine(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta2}}}) +
		"data: [DONE]\n\n"

	out, err := aggregate(strings.NewReader(in), "m")
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	tcs, _ := msg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls 数量 = %d", len(tcs))
	}
	fn := tcs[0].(map[string]any)["function"].(map[string]any)
	if fn["arguments"] != `{"a":1}` {
		t.Fatalf("arguments 拼接结果 = %v", fn["arguments"])
	}
}
// TestJwtExp 覆盖 refresh 后从 JWT 取到期时间的路径。
func TestJwtExp(t *testing.T) {
	// {"exp":1790089841}
	tok := "h." + "eyJleHAiOjE3OTAwODk4NDF9" + ".s"
	if got := jwtExp(tok); got != 1790089841 {
		t.Fatalf("jwtExp = %d", got)
	}
	if got := jwtExp("not-a-jwt"); got != 0 {
		t.Fatalf("非 JWT 应返回 0，got %d", got)
	}
}

// TestParsePointsBalance 覆盖积分余额的四个池子与充值冻结标记（实测响应形状）。
func TestParsePointsBalance(t *testing.T) {
	raw := []byte(`{"code":0,"message":"success","data":{"available_points":6313,"daily_points":313,` +
		`"monthly_points":0,"reward_points":6000,"topup_points":0,"topup_frozen":false}}`)
	var resp pointsBalanceResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	remain, items := parsePointsBalance(resp.Data)
	if remain != 6313 {
		t.Fatalf("available = %d, want 6313", remain)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4（每日/奖励/充值/月度）", len(items))
	}
	byName := map[string]provider.ResourceItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if byName["每日额度"].Remain != 313 || byName["奖励积分"].Remain != 6000 {
		t.Fatalf("池子拆分不对: %+v", items)
	}
	if !byName["充值积分"].Usable {
		t.Fatal("topup_frozen=false 时充值积分应可用")
	}

	// 冻结场景：充值池标记为不可用，Summarize 应把它算进 unusable（面板分开展示）。
	// 测试数据保持自洽：实测 available_points == 可用池之和（313+6000=6313），
	// 故冻结的 50 不计入 available。
	frozen := pointsBalanceData{AvailablePoints: 100, DailyPoints: 100, TopupPoints: 50, TopupFrozen: true}
	_, items2 := parsePointsBalance(frozen)
	for _, it := range items2 {
		if it.Name == "充值积分" && it.Usable {
			t.Fatal("topup_frozen=true 时充值积分必须 Usable=false")
		}
	}
	usable, unusable := provider.Summarize(items2)
	if usable != 100 || unusable != 50 {
		t.Fatalf("Summarize = (%d,%d), want (100,50)", usable, unusable)
	}
	if usable != frozen.AvailablePoints {
		t.Fatalf("available_points(%d) 应等于可用池之和(%d)", frozen.AvailablePoints, usable)
	}
}
