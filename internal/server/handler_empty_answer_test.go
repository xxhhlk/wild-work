// handler_empty_answer_test.go 覆盖「空回答（思考吃光 max_tokens）」的放大重试兜底。
//
// 思考型模型的 max_tokens 是输出总预算（含思考 token）。客户端给 256 这类小预算时，
// 预算可能全部耗在思考上：正文空 + finish_reason=length。此时把预算放大重跑一次通常
// 就能拿到正文。测试的核心是**只在四条判据同时命中时**才重试——否则会给每个正常请求
// 都多发一次上游调用（多花额度）。
package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
)

// emptyAnswerUpstream 首次请求返回「空正文 + length」，放大预算后返回正文。
// 记录实际收到的 max_tokens 序列，用于断言「确实放大了再发」。
type emptyAnswerUpstream struct {
	calls     atomic.Int64
	seenMax   []int64
	firstBody string // 首次响应（空回答）；为空时用默认
	// retryBody 放大后的响应
	retryBody string
}

func (e *emptyAnswerUpstream) RefreshToken(*auth.Auth) error { return nil }

func (e *emptyAnswerUpstream) ChatStream(_ *auth.Auth, body []byte) (io.ReadCloser, int, []byte, error) {
	n := e.calls.Add(1)
	var obj map[string]any
	_ = json.Unmarshal(body, &obj)
	if v, ok := obj["max_tokens"].(float64); ok {
		e.seenMax = append(e.seenMax, int64(v))
	} else {
		e.seenMax = append(e.seenMax, 0)
	}
	// 非流式：status 200 + 空 body，内容由 Aggregate 提供
	if n == 1 {
		return io.NopCloser(strings.NewReader("")), 200, nil, nil
	}
	return io.NopCloser(strings.NewReader("")), 200, nil, nil
}

func (e *emptyAnswerUpstream) Aggregate(_ io.Reader, _ string) (map[string]any, error) {
	if e.calls.Load() == 1 {
		return e.aggregate(e.firstBody), nil
	}
	return e.aggregate(e.retryBody), nil
}

// aggregate 把 JSON 文本转成聚合结果；空串给默认的「空回答」形态。
func (e *emptyAnswerUpstream) aggregate(body string) map[string]any {
	if body == "" {
		return map[string]any{
			"choices": []any{map[string]any{
				"finish_reason": "length",
				"message":       map[string]any{"role": "assistant", "content": ""},
			}},
		}
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	return out
}

func (*emptyAnswerUpstream) FetchModels(*auth.Auth) ([]provider.ModelInfo, error) { return nil, nil }
func (*emptyAnswerUpstream) FetchModelPricing(*auth.Auth) ([]provider.ModelPricing, error) {
	return nil, nil
}
func (*emptyAnswerUpstream) UserResource(*auth.Auth) (int64, error) { return 1, nil }
func (*emptyAnswerUpstream) UserResourceDetail(*auth.Auth) (int64, []provider.ResourceItem, error) {
	return 0, nil, nil
}
func (*emptyAnswerUpstream) DailyCheckin(*auth.Auth) error { return nil }
func (*emptyAnswerUpstream) Classify(int, string) provider.ErrKind {
	return provider.ErrClient
}
func (*emptyAnswerUpstream) Stream(http.ResponseWriter, io.Reader, string) (map[string]any, error) {
	return nil, nil
}

// newEmptyAnswerHandler 单渠道单账号，非流式请求走 Aggregate 路径。
func newEmptyAnswerHandler(up *emptyAnswerUpstream) *Handler {
	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t", ExpiresAt: time.Now().Add(24 * time.Hour).Unix()})
	rt := &Runtime{Kind: provider.Qoder, Pool: p, Upstream: up}
	return NewHandler(Config{
		Runtimes:     map[provider.Kind]*Runtime{provider.Qoder: rt},
		ErrThreshold: 3,
		ErrCooldown:  time.Minute,
	})
}

// postChatWithMax 发一个非流式请求，带指定 max_tokens。
func postChatWithMax(h *Handler, maxTokens string) *httptest.ResponseRecorder {
	body := `{"model":"qoder/glm-5.2","stream":false,"max_tokens":` + maxTokens +
		`,"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rec, req)
	return rec
}

// contentOf 取响应里的 choices[0].message.content（无则空串）。
func contentOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %v body=%s", err, rec.Body.String())
	}
	choices, _ := out["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	first, _ := choices[0].(map[string]any)
	msg, _ := first["message"].(map[string]any)
	s, _ := msg["content"].(string)
	return s
}

// TestEmptyAnswerRetryBumpsBudget 空回答 + 小预算 → 放大重试一次并返回新正文。
func TestEmptyAnswerRetryBumpsBudget(t *testing.T) {
	up := &emptyAnswerUpstream{}
	up.firstBody = "" // 默认：content 空 + finish_reason=length
	up.retryBody = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"放大后的正文"}}]}`
	h := newEmptyAnswerHandler(up)

	rec := postChatWithMax(h, "256")
	if rec.Code != 200 {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := contentOf(t, rec); got != "放大后的正文" {
		t.Fatalf("content=%q want 放大后的正文（应由重试结果替换）", got)
	}
	if n := up.calls.Load(); n != 2 {
		t.Fatalf("上游调用次数=%d want 2（原始 + 放大重试）", n)
	}
	if len(up.seenMax) != 2 || up.seenMax[0] != 256 || up.seenMax[1] != 1024 {
		t.Fatalf("max_tokens 序列=%v want [256 1024]", up.seenMax)
	}
}

// TestEmptyAnswerNoRetryWhenContentPresent 有正文 → 不重试。
func TestEmptyAnswerNoRetryWhenContentPresent(t *testing.T) {
	up := &emptyAnswerUpstream{
		firstBody: `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"正常回答"}}]}`,
		retryBody: `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"不该出现"}}]}`,
	}
	h := newEmptyAnswerHandler(up)

	rec := postChatWithMax(h, "256")
	if got := contentOf(t, rec); got != "正常回答" {
		t.Fatalf("content=%q want 正常回答", got)
	}
	if n := up.calls.Load(); n != 1 {
		t.Fatalf("上游调用次数=%d want 1（有正文不该重试）", n)
	}
}

// TestEmptyAnswerNoRetryWhenBudgetLarge 预算够大（≥1024）→ 不重试。
func TestEmptyAnswerNoRetryWhenBudgetLarge(t *testing.T) {
	up := &emptyAnswerUpstream{}
	up.retryBody = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"不该出现"}}]}`
	h := newEmptyAnswerHandler(up)

	postChatWithMax(h, "4096")
	if n := up.calls.Load(); n != 1 {
		t.Fatalf("上游调用次数=%d want 1（预算已够大不该重试）", n)
	}
}

// TestEmptyAnswerNoRetryWithoutBudget 客户端未给 max_tokens → 不重试。
func TestEmptyAnswerNoRetryWithoutBudget(t *testing.T) {
	up := &emptyAnswerUpstream{}
	up.retryBody = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"不该出现"}}]}`
	h := newEmptyAnswerHandler(up)

	body := `{"model":"qoder/glm-5.2","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rec, req)

	if n := up.calls.Load(); n != 1 {
		t.Fatalf("上游调用次数=%d want 1（无预算不该重试）", n)
	}
}

// TestEmptyAnswerNoRetryWhenToolCalls 有工具调用 → 不算「被思考吃光」，不重试。
func TestEmptyAnswerNoRetryWhenToolCalls(t *testing.T) {
	up := &emptyAnswerUpstream{
		firstBody: `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`,
	}
	up.retryBody = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"不该出现"}}]}`
	h := newEmptyAnswerHandler(up)

	rec := postChatWithMax(h, "256")
	if rec.Code != 200 {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if n := up.calls.Load(); n != 1 {
		t.Fatalf("上游调用次数=%d want 1（有 tool_calls 不该重试）", n)
	}
}

// TestEmptyAnswerKeepsOriginalWhenRetryStillEmpty 重试仍为空 → 保留原结果，不用更差的替换。
func TestEmptyAnswerKeepsOriginalWhenRetryStillEmpty(t *testing.T) {
	up := &emptyAnswerUpstream{}
	up.firstBody = "" // 空回答
	up.retryBody = "" // 重试仍为空回答
	h := newEmptyAnswerHandler(up)

	rec := postChatWithMax(h, "256")
	if rec.Code != 200 {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if n := up.calls.Load(); n != 2 {
		t.Fatalf("上游调用次数=%d want 2（应尝试过一次重试）", n)
	}
	// 保留原结果：finish_reason 仍为 length
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	choices, _ := out["choices"].([]any)
	first, _ := choices[0].(map[string]any)
	if fr, _ := first["finish_reason"].(string); fr != "length" {
		t.Fatalf("finish_reason=%q want length（保留原结果）", fr)
	}
}

// TestEmptyAnswerBumpedFloor 放大值 = max(asked*4, 1024)。
func TestEmptyAnswerBumpedFloor(t *testing.T) {
	cases := []struct{ asked, want int64 }{
		{100, 1024}, // 100*4=400 < 1024 → 取下限
		{256, 1024}, // 256*4=1024 → 刚好等于下限
		{300, 1200}, // 300*4=1200 > 1024 → 取四倍
	}
	for _, tc := range cases {
		if got := emptyAnswerBumped(tc.asked); got != tc.want {
			t.Errorf("emptyAnswerBumped(%d)=%d want %d", tc.asked, got, tc.want)
		}
	}
}

// TestRequestedMaxTokens 兼容 max_tokens / max_completion_tokens，非法值返回 0。
func TestRequestedMaxTokens(t *testing.T) {
	cases := []struct {
		body string
		want int64
	}{
		{`{"max_tokens":256}`, 256},
		{`{"max_completion_tokens":512}`, 512},
		{`{"max_tokens":256,"max_completion_tokens":512}`, 256}, // max_tokens 优先
		{`{}`, 0},
		{`{"max_tokens":0}`, 0},
		{`{"max_tokens":-1}`, 0},
		{`not json`, 0},
	}
	for _, tc := range cases {
		if got := requestedMaxTokens([]byte(tc.body)); got != tc.want {
			t.Errorf("requestedMaxTokens(%s)=%d want %d", tc.body, got, tc.want)
		}
	}
}

// TestWithMaxTokensRewrites 改写后 max_completion_tokens 被删除，避免上游取到旧值。
func TestWithMaxTokensRewrites(t *testing.T) {
	out, err := withMaxTokens([]byte(`{"max_tokens":100,"max_completion_tokens":200,"model":"m"}`), 1024)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	if v, _ := obj["max_tokens"].(float64); int64(v) != 1024 {
		t.Fatalf("max_tokens=%v want 1024", obj["max_tokens"])
	}
	if _, ok := obj["max_completion_tokens"]; ok {
		t.Fatal("max_completion_tokens 应被删除")
	}
	if obj["model"] != "m" {
		t.Fatal("其他字段不该被改动")
	}
}

// TestEmptyAnswerRetryDoesNotPunishAccount 重试失败不得影响账号健康（同一账号重跑，
// 空回答与账号无关）。
func TestEmptyAnswerRetryDoesNotPunishAccount(t *testing.T) {
	up := &emptyAnswerUpstream{}
	h := newEmptyAnswerHandler(up)
	postChatWithMax(h, "256")

	rt := h.cfg.Runtimes[provider.Qoder]
	st, ok := rt.Pool.Status("u1")
	if !ok {
		t.Fatal("账号消失")
	}
	if st.Cooling || st.Disabled {
		t.Fatalf("空回答重试不该冷却/禁用账号：cooling=%v disabled=%v reason=%q",
			st.Cooling, st.Disabled, st.Reason)
	}
	if st.ErrCount != 0 {
		t.Fatalf("errCount=%d want 0", st.ErrCount)
	}
}
