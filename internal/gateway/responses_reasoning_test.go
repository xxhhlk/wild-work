// responses_reasoning_test.go Responses 思考链（reasoning item / summary 事件族）下发。
//
// 覆盖点：
//   - 事件序列与官方一致：output_item.added → summary_part.added → summary_text.delta
//     → summary_text.done → summary_part.done → output_item.done
//   - output_index 动态分配：reasoning=0、message=1、function_call 顺延
//   - auto / on / off 三种策略
//   - 文本开始后的思考增量丢弃（思考必须先于回答）
//   - 非流式响应把思考链放在 output 首位
package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// reasoningStreamHandler 假内层：按脚本发思考 + 文本 + 可选工具调用的 Chat SSE。
type reasoningStreamHandler struct {
	// thinkAfterText 为 true 时把最后一段思考放在文本之后（用于验证被丢弃）。
	thinkAfterText bool
	withTool       bool
}

func (h *reasoningStreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	f, _ := w.(http.Flusher)
	write := func(payload string) {
		_, _ = w.Write([]byte("data: " + payload + "\n\n"))
		if f != nil {
			f.Flush()
		}
	}
	chunk := func(delta string) string {
		return `{"id":"c1","object":"chat.completion.chunk","model":"up","choices":[{"index":0,"delta":` + delta + `}]}`
	}
	write(chunk(`{"reasoning_content":"思考A"}`))
	write(chunk(`{"reasoning_content":"思考B"}`))
	if h.withTool {
		write(chunk(`{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"shell","arguments":"{\"cmd\":"}}]}`))
		write(chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}`))
		write(`{"id":"c1","object":"chat.completion.chunk","model":"up","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	} else {
		write(chunk(`{"content":"你好"}`))
		if h.thinkAfterText {
			write(chunk(`{"reasoning_content":"迟到的思考"}`))
		}
		write(`{"id":"c1","object":"chat.completion.chunk","model":"up","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if f != nil {
		f.Flush()
	}
}

// reasoningJSONHandler 假内层：非流式响应带 reasoning_content。
type reasoningJSONHandler struct{}

func (reasoningJSONHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"up","choices":[` +
		`{"index":0,"message":{"role":"assistant","content":"你好","reasoning_content":"思考内容"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":7}}}`))
}

// newSummaryMux 构造带思考摘要策略的兼容层 mux。
func newSummaryMux(inner http.Handler, mode string) *http.ServeMux {
	g := New(Config{Inner: inner, APIKey: "secret", Router: testRouter(), ResponsesReasoningSummary: mode})
	mux := http.NewServeMux()
	g.Routes(mux)
	mux.Handle("/", inner)
	return mux
}

// sseEvent 一条解析后的 SSE 事件。
type sseEvent struct {
	name string
	data map[string]any
}

// parseSSEEvents 把 SSE 响应体解析为事件列表（跳过 [DONE]）。
func parseSSEEvents(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, block := range strings.Split(body, "\n\n") {
		var name, payload string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatalf("事件不是合法 JSON: %s", payload)
		}
		if name == "" {
			name = asString(m["type"])
		}
		out = append(out, sseEvent{name: name, data: m})
	}
	return out
}

// eventNames 提取事件名序列。
func eventNames(events []sseEvent) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.name)
	}
	return names
}

// findEvent 返回首个指定类型的事件，缺失时失败。
func findEvent(t *testing.T, events []sseEvent, name string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e.name == name {
			return e.data
		}
	}
	t.Fatalf("缺少事件 %s（实际序列 %v）", name, eventNames(events))
	return nil
}

// assertNoEvent 断言事件序列里不存在某类型事件。
func assertNoEvent(t *testing.T, events []sseEvent, name string) {
	t.Helper()
	for _, e := range events {
		if e.name == name {
			t.Fatalf("不应出现事件 %s（实际序列 %v）", name, eventNames(events))
		}
	}
}

// auto 策略 + 客户端索要摘要（Codex 的 reasoning.summary=auto）→ 完整 reasoning 事件序列。
func TestResponsesStreamReasoningSummary(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "")
	rec := post(mux, "/v1/responses",
		`{"model":"gpt-5","input":"hi","stream":true,"reasoning":{"effort":"high","summary":"auto"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())

	// 事件顺序：思考块完整出现在文本块之前
	want := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",            // reasoning
		"response.reasoning_summary_part.added", // summary_index=0
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.delta",
		"response.output_item.added", // message
		"response.content_part.added",
		"response.output_text.delta",
		"response.reasoning_summary_text.done",
		"response.reasoning_summary_part.done",
		"response.output_item.done", // reasoning
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done", // message
		"response.completed",
	}
	got := eventNames(events)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列不符\n got: %v\nwant: %v", got, want)
	}

	// reasoning item：index 0，summary 汇总两段增量
	rsAdded := findEvent(t, events, "response.reasoning_summary_part.added")
	if rsAdded["output_index"] != float64(0) || rsAdded["summary_index"] != float64(0) {
		t.Errorf("summary_part.added 索引 = %v/%v, want 0/0", rsAdded["output_index"], rsAdded["summary_index"])
	}
	textDone := findEvent(t, events, "response.reasoning_summary_text.done")
	if textDone["text"] != "思考A思考B" {
		t.Errorf("summary text = %v, want 思考A思考B", textDone["text"])
	}
	// message item：index 顺延为 1
	msgAdded := findEvent(t, events, "response.content_part.added")
	if msgAdded["output_index"] != float64(1) {
		t.Errorf("message output_index = %v, want 1", msgAdded["output_index"])
	}

	// response.completed 的 output 顺序 = [reasoning, message]，且 reasoning.summary 完整
	final := findEvent(t, events, "response.completed")["response"].(map[string]any)
	output := final["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output 长度 = %d, want 2: %v", len(output), output)
	}
	first := output[0].(map[string]any)
	if first["type"] != "reasoning" {
		t.Fatalf("output[0].type = %v, want reasoning", first["type"])
	}
	summary := first["summary"].([]any)
	if len(summary) != 1 || summary[0].(map[string]any)["text"] != "思考A思考B" {
		t.Errorf("output[0].summary = %v", summary)
	}
	if output[1].(map[string]any)["type"] != "message" {
		t.Errorf("output[1].type = %v, want message", output[1].(map[string]any)["type"])
	}
	// output_text 只含正文，不含思考
	if final["output_text"] != "你好" {
		t.Errorf("output_text = %v, want 你好", final["output_text"])
	}
}

// auto 策略 + 客户端未索要摘要 → 完全不下发思考事件。
func TestResponsesStreamReasoningAutoSkipsWhenNotRequested(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "")
	rec := post(mux, "/v1/responses", `{"model":"gpt-5","input":"hi","stream":true}`)
	events := parseSSEEvents(t, rec.Body.String())
	assertNoEvent(t, events, "response.reasoning_summary_text.delta")
	assertNoEvent(t, events, "response.reasoning_summary_part.added")

	final := findEvent(t, events, "response.completed")["response"].(map[string]any)
	if len(final["output"].([]any)) != 1 {
		t.Errorf("output 应只有 message: %v", final["output"])
	}
}

// include 里含 reasoning.encrypted_content 也视为索要思考。
func TestResponsesStreamReasoningAutoViaInclude(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "")
	rec := post(mux, "/v1/responses",
		`{"model":"gpt-5","input":"hi","stream":true,"include":["reasoning.encrypted_content"]}`)
	events := parseSSEEvents(t, rec.Body.String())
	findEvent(t, events, "response.reasoning_summary_text.delta")
}

// reasoning.summary=none 表示明确不要摘要。
func TestResponsesStreamReasoningAutoSummaryNone(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "")
	rec := post(mux, "/v1/responses",
		`{"model":"gpt-5","input":"hi","stream":true,"reasoning":{"effort":"high","summary":"none"}}`)
	events := parseSSEEvents(t, rec.Body.String())
	assertNoEvent(t, events, "response.reasoning_summary_text.delta")
}

// on：客户端没要也下发。
func TestResponsesStreamReasoningOnForces(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "on")
	rec := post(mux, "/v1/responses", `{"model":"gpt-5","input":"hi","stream":true}`)
	events := parseSSEEvents(t, rec.Body.String())
	findEvent(t, events, "response.reasoning_summary_text.delta")
}

// off：客户端要了也不下发（保持旧行为）。
func TestResponsesStreamReasoningOff(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "off")
	rec := post(mux, "/v1/responses",
		`{"model":"gpt-5","input":"hi","stream":true,"reasoning":{"effort":"high","summary":"auto"}}`)
	events := parseSSEEvents(t, rec.Body.String())
	assertNoEvent(t, events, "response.reasoning_summary_text.delta")
	assertNoEvent(t, events, "response.reasoning_summary_part.added")
}

// 文本开始后到达的思考增量必须丢弃（思考先于回答），避免 output_index 倒序。
func TestResponsesStreamReasoningAfterTextDropped(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{thinkAfterText: true}, "on")
	rec := post(mux, "/v1/responses", `{"model":"gpt-5","input":"hi","stream":true}`)
	events := parseSSEEvents(t, rec.Body.String())

	textDone := findEvent(t, events, "response.reasoning_summary_text.done")
	if textDone["text"] != "思考A思考B" {
		t.Errorf("迟到的思考应被丢弃，实际 %v", textDone["text"])
	}
	final := findEvent(t, events, "response.completed")["response"].(map[string]any)
	output := final["output"].([]any)
	if output[0].(map[string]any)["type"] != "reasoning" || output[1].(map[string]any)["type"] != "message" {
		t.Errorf("output 顺序应为 reasoning,message: %v", output)
	}
}

// 工具调用场景：reasoning=0、message=1、function_call=2。
func TestResponsesStreamReasoningIndexWithTool(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{withTool: true}, "on")
	rec := post(mux, "/v1/responses", `{"model":"gpt-5","input":"hi","stream":true}`)
	events := parseSSEEvents(t, rec.Body.String())

	// 无文本 → 只有 reasoning 与 function_call 两个 item
	final := findEvent(t, events, "response.completed")["response"].(map[string]any)
	output := final["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output 长度 = %d, want 2: %v", len(output), output)
	}
	if output[0].(map[string]any)["type"] != "reasoning" || output[1].(map[string]any)["type"] != "function_call" {
		t.Fatalf("output 类型 = %v, %v", output[0].(map[string]any)["type"], output[1].(map[string]any)["type"])
	}
	// function_call item 的 output_index 必须顺延为 1
	fcDone := false
	for _, e := range events {
		if e.name == "response.output_item.done" && e.data["output_index"] == float64(1) {
			item := e.data["item"].(map[string]any)
			if item["type"] != "function_call" {
				t.Fatalf("index 1 的 item 应为 function_call: %v", item)
			}
			fcDone = true
		}
	}
	if !fcDone {
		t.Errorf("缺少 index=1 的 function_call output_item.done（实际序列 %v）", eventNames(events))
	}
	if got := output[1].(map[string]any)["arguments"]; got != `{"cmd":"ls"}` {
		t.Errorf("工具参数拼接错误: %v", got)
	}
}

// 非流式：思考链作为 output 首位的 reasoning item 返回。
func TestResponsesNonStreamReasoningItem(t *testing.T) {
	mux := newSummaryMux(reasoningJSONHandler{}, "")
	rec := post(mux, "/v1/responses",
		`{"model":"gpt-5","input":"hi","reasoning":{"effort":"high","summary":"auto"}}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	output := out["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output 长度 = %d, want 2: %v", len(output), output)
	}
	rs := output[0].(map[string]any)
	if rs["type"] != "reasoning" {
		t.Fatalf("output[0].type = %v, want reasoning", rs["type"])
	}
	summary := rs["summary"].([]any)
	if len(summary) != 1 || summary[0].(map[string]any)["text"] != "思考内容" {
		t.Errorf("summary = %v", summary)
	}
	if output[1].(map[string]any)["type"] != "message" {
		t.Errorf("output[1].type = %v, want message", output[1].(map[string]any)["type"])
	}
	if out["output_text"] != "你好" {
		t.Errorf("output_text = %v, want 你好", out["output_text"])
	}
	// reasoning_tokens 仍按 usage 映射
	details := out["usage"].(map[string]any)["output_tokens_details"].(map[string]any)
	if details["reasoning_tokens"] != float64(7) {
		t.Errorf("reasoning_tokens = %v, want 7", details["reasoning_tokens"])
	}
}

// 非流式 + 客户端未索要摘要 → 无 reasoning item。
func TestResponsesNonStreamReasoningSkipped(t *testing.T) {
	mux := newSummaryMux(reasoningJSONHandler{}, "")
	rec := post(mux, "/v1/responses", `{"model":"gpt-5","input":"hi"}`)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	output := out["output"].([]any)
	if len(output) != 1 || output[0].(map[string]any)["type"] != "message" {
		t.Errorf("output 应只有 message: %v", output)
	}
}

// 热更新：SetCompat 后策略立即生效，无需重启。
func TestResponsesSummaryHotReload(t *testing.T) {
	inner := &reasoningStreamHandler{}
	g := New(Config{Inner: inner, APIKey: "secret", Router: testRouter()})
	mux := http.NewServeMux()
	g.Routes(mux)
	mux.Handle("/", inner)

	body := `{"model":"gpt-5","input":"hi","stream":true}`
	events := parseSSEEvents(t, post(mux, "/v1/responses", body).Body.String())
	assertNoEvent(t, events, "response.reasoning_summary_text.delta") // 默认 auto：未索要 → 不发

	g.SetCompat("workbuddy", 0, nil, []string{"workbuddy"}, "on")
	events = parseSSEEvents(t, post(mux, "/v1/responses", body).Body.String())
	findEvent(t, events, "response.reasoning_summary_text.delta") // 热更新为 on → 下发

	g.SetCompat("workbuddy", 0, nil, []string{"workbuddy"}, "off")
	events = parseSSEEvents(t, post(mux, "/v1/responses",
		`{"model":"gpt-5","input":"hi","stream":true,"reasoning":{"summary":"auto"}}`).Body.String())
	assertNoEvent(t, events, "response.reasoning_summary_text.delta")
}

// 未知策略字符串按 auto 兜底（配置加载阶段已校验，这里防运行时脏值）。
func TestResponsesSummaryUnknownModeFallsBackToAuto(t *testing.T) {
	mux := newSummaryMux(&reasoningStreamHandler{}, "bogus")
	rec := post(mux, "/v1/responses", `{"model":"gpt-5","input":"hi","stream":true}`)
	events := parseSSEEvents(t, rec.Body.String())
	assertNoEvent(t, events, "response.reasoning_summary_text.delta")
}
