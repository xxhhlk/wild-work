package loomy

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

// TestClassifyBusinessCodeFirst 守门：Loomy 的鉴权失败是 **HTTP 200 + code 100002**，
// 业务码判定必须排在状态码判定之前，否则会被当成成功（AGENTS §6.25）。
func TestClassifyBusinessCodeFirst(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   provider.ErrKind
	}{
		{"200 + 100002 登录失效", 200, `{"code":"100002","desc":"登录已失效，请重新登录","data":{}}`, provider.ErrSessionDead},
		{"200 + 100002 缺少 token", 200, `{"code":"100002","desc":"缺少 token"}`, provider.ErrSessionDead},
		{"200 + 020002 会话校验失败", 200, `{"code":"020002","desc":"session invalid"}`, provider.ErrSessionDead},
		{"数字型 100002", 200, `{"code":100002}`, provider.ErrSessionDead},
		{"前缀误命中必须排除", 200, `{"code":1000020}`, provider.ErrNone},
		{"400 空 messages", 400, `{"error":{"message":"messages 不能为空","type":"invalid_request_error","param":"messages","code":400,"metadata":{"provider_name":"loomy"}}}`, provider.ErrBadParams},
		{"本地拒绝的未知模型", 400, `{"error":{"code":"model_not_found"}}`, provider.ErrBadParams},
		{"429 限流", 429, `{"message":"rate limit"}`, provider.ErrSoftRate},
		{"402 积分不足", 402, `{"message":"积分不足"}`, provider.ErrHardCredit},
		{"500", 500, `boom`, provider.ErrServer},
		{"404", 404, `not found`, provider.ErrNotFound},
		{"正常响应", 200, `{"id":"x","object":"chat.completion"}`, provider.ErrNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.status, tc.body); got != tc.want {
				t.Fatalf("Classify(%d, %s) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestProjectEffort 覆盖官方三件套投影（阶段 C 实测的递减最优形态）。
func TestProjectEffort(t *testing.T) {
	t.Run("none → 三件套全 false", func(t *testing.T) {
		out := projectEffort([]byte(`{"model":"spark-x","reasoning_effort":"none","messages":[]}`))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if m["enable_thinking"] != false {
			t.Fatalf("enable_thinking = %v, want false", m["enable_thinking"])
		}
		ctk, _ := m["chat_template_kwargs"].(map[string]any)
		if ctk == nil || ctk["enable_thinking"] != false {
			t.Fatalf("chat_template_kwargs 缺失或不对: %v", m["chat_template_kwargs"])
		}
	})
	t.Run("low → true", func(t *testing.T) {
		out := projectEffort([]byte(`{"reasoning_effort":"low","messages":[]}`))
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		if m["enable_thinking"] != true {
			t.Fatalf("enable_thinking = %v, want true", m["enable_thinking"])
		}
		ctk, _ := m["chat_template_kwargs"].(map[string]any)
		if ctk == nil || ctk["enable_thinking"] != true {
			t.Fatalf("chat_template_kwargs = %v", m["chat_template_kwargs"])
		}
	})
	t.Run("未表达档位时不改写 body", func(t *testing.T) {
		raw := []byte(`{"model":"spark-x","messages":[]}`)
		if got := string(projectEffort(raw)); got != string(raw) {
			t.Fatalf("不应改写：%s", got)
		}
	})
	t.Run("已有 chat_template_kwargs 时不覆盖", func(t *testing.T) {
		out := projectEffort([]byte(`{"reasoning_effort":"low","chat_template_kwargs":{"custom":1}}`))
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		ctk, _ := m["chat_template_kwargs"].(map[string]any)
		if _, ok := ctk["custom"]; !ok {
			t.Fatalf("调用方自定义的 chat_template_kwargs 被覆盖: %v", ctk)
		}
	})
}

// TestParsePoints 守门：上游积分有两个池（balance 常规 + dailyBalance 每日），
// 可用总额是两者之和（v1 面直接下发 availableBalance）。只读 balance 会漏掉每日积分 ——
// 2026-09-23 实测该账号 balance=15000 / dailyBalance=4800 / availableBalance=19800。
func TestParsePoints(t *testing.T) {
	// v1 面真实响应形状（截自实测，list 省略）。
	v1 := `{"code":"000000","desc":"成功","data":{"balance":15000,"dailyBalance":4800,
		"availableBalance":19800,"pageNo":1,"pageSize":20,"total":622,"list":[]}}`
	// v2 面：聊天聚合形状，**没有** availableBalance。
	v2 := `{"code":"000000","desc":"成功","data":{"balance":15000,"dailyBalance":4800,
		"pageNo":1,"pageSize":20,"total":101,"list":[]}}`

	cases := []struct {
		name     string
		body     string
		want     int64
		wantName []string
		wantEach []int64
	}{
		{"v1 用上游 availableBalance", v1, 19800, []string{"积分", "每日积分"}, []int64{15000, 4800}},
		{"v2 缺失时两池相加兜底", v2, 19800, []string{"积分", "每日积分"}, []int64{15000, 4800}},
		{"只有常规池", `{"data":{"balance":1200}}`, 1200, []string{"积分"}, []int64{1200}},
		{"只有每日池", `{"data":{"dailyBalance":300}}`, 300, []string{"每日积分"}, []int64{300}},
		{"余额为 0 也要展示条目", `{"data":{"balance":0,"dailyBalance":0,"availableBalance":0}}`, 0,
			[]string{"积分", "每日积分"}, []int64{0, 0}},
		{"字段全缺 → 宁缺勿错", `{"data":{"pageNo":1}}`, 0, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec pointsRecord
			if err := json.Unmarshal([]byte(tc.body), &rec); err != nil {
				t.Fatal(err)
			}
			remain, items := parsePoints(rec)
			if remain != tc.want {
				t.Fatalf("remain = %d, want %d", remain, tc.want)
			}
			if len(items) != len(tc.wantName) {
				t.Fatalf("items = %v, want %v", items, tc.wantName)
			}
			for i, it := range items {
				if it.Name != tc.wantName[i] || it.Total != tc.wantEach[i] || it.Remain != tc.wantEach[i] || !it.Usable {
					t.Fatalf("items[%d] = %+v, want name=%s total=remain=%d usable=true",
						i, it, tc.wantName[i], tc.wantEach[i])
				}
			}
		})
	}
}

func TestPriceFromName(t *testing.T) {
	cases := map[string]float64{
		"DeepSeek V4 Flash 0731（x3.0）": 3.0,
		"Qwen 3.8 Max (x12.0)":         12.0,
		"Spark X2.5（x0.1）":             0.1,
		"qwen 3.8 flash（x0.8）":         0.8,
		"Kimi k2.6（x6.5）":              6.5,
		"没有倍率":                         0,
	}
	for in, want := range cases {
		if got := priceFromName(in); got != want {
			t.Fatalf("priceFromName(%q) = %v, want %v", in, got, want)
		}
	}
	if got := cleanName("Spark X2.5（x0.1）"); got != "Spark X2.5" {
		t.Fatalf("cleanName = %q", got)
	}
}

func TestKnownModel(t *testing.T) {
	for _, id := range []string{DefaultModel, "spark-x", "MiniMax-M3", "mimo-v2.5"} {
		if !KnownModel(id) {
			t.Fatalf("KnownModel(%q) = false", id)
		}
	}
	if KnownModel("nope-xyz") {
		t.Fatal("KnownModel(未知) 应为 false")
	}
}

// TestRefreshTokenReportsSessionDead 守门：Loomy 无 refresh 端点，
// 必须返回 ErrSessionDead（而不是发出一个必然失败的请求）。
func TestRefreshTokenReportsSessionDead(t *testing.T) {
	c := New()
	err := c.RefreshToken(&auth.Auth{AccessToken: "s"})
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("应为 *provider.Error，实际 %T", err)
	}
	if pe.Kind != provider.ErrSessionDead {
		t.Fatalf("Kind = %v, want ErrSessionDead", pe.Kind)
	}
}

func TestChatStreamRejectsUnknownModel(t *testing.T) {
	c := New()
	a := &auth.Auth{AccessToken: "dummy"}
	_, status, body, err := c.ChatStream(a, []byte(`{"model":"nope","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 400 || !strings.Contains(string(body), "model_not_found") {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestTraceparentShape(t *testing.T) {
	tp := traceparent()
	parts := strings.Split(tp, "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || parts[3] != "01" {
		t.Fatalf("traceparent 形状不对: %s", tp)
	}
	if traceparent() == tp {
		t.Fatal("traceparent 应每次不同（缺它会挂死到超时，上游依赖它做链路追踪）")
	}
}

func TestStreamAndAggregate(t *testing.T) {
	in := "data: " + `{"id":"1","object":"chat.completion.chunk","model":"up","choices":[{"index":0,"delta":{"reasoning_content":"t"}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n" +
		"data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	if _, err := Stream(rec, strings.NewReader(in), "loomy/spark-x"); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `"model":"loomy/spark-x"`) {
		t.Fatalf("model 未重写: %s", rec.Body.String())
	}

	out, err := aggregate(strings.NewReader(in), "loomy/spark-x")
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hi" || msg["reasoning_content"] != "t" {
		t.Fatalf("聚合结果不对: %v", msg)
	}
}
