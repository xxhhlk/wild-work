// image_url 归一化测试：上游只认 OpenAI 对象形态，字符串形态会 400 code=11101。
package upstream

import (
	"encoding/json"
	"testing"
)

// imagePart 取出 messages[idx].content 里第一个 type=image_url 的 part。
func imagePart(t *testing.T, src string) map[string]any {
	t.Helper()
	obj := projectOne(t, src)
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("messages missing: %#v", obj)
	}
	msg, _ := msgs[0].(map[string]any)
	parts, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("content is not an array: %#v", msg["content"])
	}
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if ok && part["type"] == "image_url" {
			return part
		}
	}
	t.Fatalf("image_url part not found in %#v", parts)
	return nil
}

func TestNormalizeImageURL(t *testing.T) {
	cases := []struct {
		name string
		body string
		// wantURL 非 nil 时断言 image_url 是对象且 url 等于它
		wantURL any
		// wantRaw 非 nil 时断言 image_url 原样等于它（不做形状转换的形态）
		wantRaw any
		// wantAbsent 断言 image_url 键不存在
		wantAbsent bool
	}{
		{
			name:    "data url 字符串 → 对象",
			body:    `{"messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":"data:image/png;base64,QUJD"}]}]}`,
			wantURL: "data:image/png;base64,QUJD",
		},
		{
			name:    "http url 字符串 → 对象",
			body:    `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"https://example.test/a.png"}]}]}`,
			wantURL: "https://example.test/a.png",
		},
		{
			name:    "已是对象则原样保留（含 detail/mime_type）",
			body:    `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD","detail":"low","mime_type":"image/png"}}]}]}`,
			wantRaw: map[string]any{"url": "data:image/png;base64,QUJD", "detail": "low", "mime_type": "image/png"},
		},
		{
			name:    "对象内 url 类型不对 → 不动（交给上游报真实错误）",
			body:    `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":123}}]}]}`,
			wantRaw: map[string]any{"url": float64(123)},
		},
		{
			name:       "image_url 缺失 → 不补默认值",
			body:       `{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`,
			wantAbsent: true,
		},
		{
			name:    "空字符串 → 不补默认值",
			body:    `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":""}]}]}`,
			wantRaw: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			part := imagePart(t, tc.body)
			got, exists := part["image_url"]
			if tc.wantAbsent {
				if exists {
					t.Fatalf("image_url 应保持缺失，实际 %#v", got)
				}
				return
			}
			if !exists {
				t.Fatalf("image_url 不应被删除")
			}
			switch {
			case tc.wantURL != nil:
				obj, ok := got.(map[string]any)
				if !ok {
					t.Fatalf("image_url 应为对象，实际 %#v", got)
				}
				if obj["url"] != tc.wantURL {
					t.Fatalf("url = %#v, want %#v", obj["url"], tc.wantURL)
				}
				if len(obj) != 1 {
					t.Fatalf("字符串形态只应转出 url 一个键，实际 %#v", obj)
				}
			case tc.wantRaw != nil:
				want, _ := json.Marshal(tc.wantRaw)
				have, _ := json.Marshal(got)
				if string(want) != string(have) {
					t.Fatalf("image_url = %s, want %s", have, want)
				}
			}
		})
	}
}

// 非 image_url 的 part、以及 content 是纯字符串的形态都不得被改动或 panic。
func TestNormalizeImageURLUntouchedShapes(t *testing.T) {
	// text part 原样
	obj := projectOne(t, `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":"data:image/png;base64,QUJD"}]}]}`)
	parts := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["text"] != "hi" {
		t.Fatalf("text part 被改动: %#v", parts[0])
	}
	// content 是纯字符串：不 panic、原样保留
	obj = projectOne(t, `{"messages":[{"role":"user","content":"plain string"}]}`)
	if got := obj["messages"].([]any)[0].(map[string]any)["content"]; got != "plain string" {
		t.Fatalf("字符串 content 被改动: %#v", got)
	}
	// 非 image_url 类型但带 image_url 字段：不动
	obj = projectOne(t, `{"messages":[{"role":"user","content":[{"type":"text","image_url":"data:image/png;base64,QUJD"}]}]}`)
	if got := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["image_url"]; got != "data:image/png;base64,QUJD" {
		t.Fatalf("非 image_url part 被改动: %#v", got)
	}
}

// 幂等：归一化后的对象再跑一遍不应产生新改动（避免每次转发都在改 body）。
func TestNormalizeImageURLIdempotent(t *testing.T) {
	src := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,QUJD"}]}]}`
	once := PrepareBody([]byte(src))
	twice := PrepareBody(once)
	if string(once) != string(twice) {
		t.Fatalf("非幂等:\n once=%s\ntwice=%s", once, twice)
	}
}
