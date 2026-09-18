// reasoning_test.go 思考控制（reasoning_effort）在兼容层的归一化与校验。
//
// 覆盖点（对齐 Buddy2api 的 reasoning_controls 契约）：
//   - Anthropic 的 thinking.{type,budget_tokens} → reasoning_effort
//   - Responses 的 reasoning.effort（preferNested）与顶层 reasoning_effort
//   - 非法取值 / 同一对象内自相矛盾 → 400 invalid_reasoning_control
package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// captureHandler 记录内层收到的 Chat 请求体，并回一个最小可用的 Chat 响应。
type captureHandler struct {
	mu   sync.Mutex
	body map[string]any
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(raw, &req)
	h.mu.Lock()
	h.body = req
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
}

func (h *captureHandler) captured() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.body
}

func postCaptured(t *testing.T, path, body string) (*captureHandler, *httptest.ResponseRecorder) {
	t.Helper()
	inner := &captureHandler{}
	_, mux := newTestGateway(inner)
	return inner, post(mux, path, body)
}

func TestAnthropicThinkingNormalizedToEffort(t *testing.T) {
	inner, rec := postCaptured(t, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}],`+
			`"thinking":{"type":"enabled","budget_tokens":4096}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := inner.captured()
	if got["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", got["reasoning_effort"])
	}
	// 原始 thinking 对象保留（渠道可能直接识别），type 不在这里剥（内层会归一化）
	if _, has := got["thinking"]; !has {
		t.Errorf("thinking 应透传，实际缺失: %v", got)
	}
}

func TestAnthropicThinkingDisabled(t *testing.T) {
	inner, _ := postCaptured(t, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}],`+
			`"thinking":{"type":"disabled"}}`)
	if got := inner.captured()["reasoning_effort"]; got != "none" {
		t.Errorf("reasoning_effort = %v, want none", got)
	}
}

func TestAnthropicReasoningEffortPassThrough(t *testing.T) {
	inner, _ := postCaptured(t, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}],`+
			`"reasoning_effort":"max"}`)
	if got := inner.captured()["reasoning_effort"]; got != "max" {
		t.Errorf("reasoning_effort = %v, want max", got)
	}
}

func TestAnthropicInvalidThinkingRejected(t *testing.T) {
	_, rec := postCaptured(t, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}],`+
			`"thinking":{"type":"maybe"}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "thinking.type") {
		t.Errorf("应 400 并指出 thinking.type，得到 %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"error"`) {
		t.Errorf("Anthropic 形状错误体缺失: %s", rec.Body.String())
	}
}

func TestResponsesReasoningEffortNormalized(t *testing.T) {
	inner, rec := postCaptured(t, "/v1/responses",
		`{"model":"gpt-5","input":"hi","reasoning":{"effort":"max","summary":"auto"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := inner.captured()["reasoning_effort"]; got != "max" {
		t.Errorf("reasoning_effort = %v, want max", got)
	}
}

func TestResponsesTopLevelEffortAccepted(t *testing.T) {
	inner, _ := postCaptured(t, "/v1/responses",
		`{"model":"gpt-5","input":"hi","reasoning_effort":"low"}`)
	if got := inner.captured()["reasoning_effort"]; got != "low" {
		t.Errorf("reasoning_effort = %v, want low", got)
	}
}

// Responses 语义：嵌套 reasoning.effort 优先于顶层 reasoning_effort。
func TestResponsesNestedEffortPreferred(t *testing.T) {
	inner, _ := postCaptured(t, "/v1/responses",
		`{"model":"gpt-5","input":"hi","reasoning_effort":"low","reasoning":{"effort":"max"}}`)
	if got := inner.captured()["reasoning_effort"]; got != "max" {
		t.Errorf("reasoning_effort = %v, want max", got)
	}
}

func TestResponsesInvalidReasoningRejected(t *testing.T) {
	_, rec := postCaptured(t, "/v1/responses",
		`{"model":"gpt-5","input":"hi","reasoning":{"effort":"very-high"}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_reasoning_control") {
		t.Errorf("应 400 invalid_reasoning_control，得到 %d %s", rec.Code, rec.Body.String())
	}
}

func TestResponsesConflictingReasoningRejected(t *testing.T) {
	_, rec := postCaptured(t, "/v1/responses",
		`{"model":"gpt-5","input":"hi","reasoning":{"effort":"high","enabled":false}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_reasoning_control") {
		t.Errorf("应 400 invalid_reasoning_control，得到 %d %s", rec.Code, rec.Body.String())
	}
}

// 未表达思考意图时不得注入字段（默认档由内层按配置注入）。
func TestNoReasoningControlLeavesFieldAbsent(t *testing.T) {
	inner, _ := postCaptured(t, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	if got, has := inner.captured()["reasoning_effort"]; has {
		t.Errorf("不应注入 reasoning_effort，实际 %v", got)
	}
}
