package loomy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
	"wild-work/internal/reasoning"
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

// TestProjectEffortClamp 守门：档位必须按该模型 ladder 降级（与 Qoder 三渠道对齐）。
//
// 上游对不认识的档位**静默按默认档处理**（不报错），不降级会让用户以为档位生效。
// 测试用独立模型名，避免污染其它用例共享的进程级能力表（reasoning.Caps）。
func TestProjectEffortClamp(t *testing.T) {
	// 真实形态：上游 8 个模型声明一致（none/low/medium/high/xhigh，default=low）。
	reasoning.Caps.SetRemote(reasoning.RealmLoomy, map[string]reasoning.Cap{
		"probe-full": {Efforts: []string{"none", "low", "medium", "high", "xhigh"}, DefaultEffort: "low"},
		// 窄 ladder：只有 low/high
		"probe-narrow": {Efforts: []string{"low", "high"}, DefaultEffort: "low"},
		// 能力未知（目录未拉到）：不降级，原样下发
		"probe-unknown": {},
	})
	cases := []struct {
		name   string
		model  string
		effort string
		want   string
		wantOn bool
	}{
		{"ladder 内档位原样", "probe-full", "high", "high", true},
		{"超出 ladder 的 max → 降最高档", "probe-full", "max", "xhigh", true},
		{"超出 ladder 的 ultra → 降最高档", "probe-full", "ultra", "xhigh", true},
		{"窄 ladder：xhigh → 降 high", "probe-narrow", "xhigh", "high", true},
		{"窄 ladder：medium → 降 low（不超发强度）", "probe-narrow", "medium", "low", true},
		{"窄 ladder：minimal → 降 low（偏离最小）", "probe-narrow", "minimal", "low", true},
		{"none 保持关闭语义", "probe-full", "none", "none", false},
		{"能力未知时不降级", "probe-unknown", "ultra", "ultra", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"model":"` + tc.model + `","reasoning_effort":"` + tc.effort + `","messages":[]}`
			out := projectEffort([]byte(raw))
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatal(err)
			}
			if got := m["reasoning_effort"]; got != tc.want {
				t.Fatalf("reasoning_effort = %v, want %v", got, tc.want)
			}
			if got := m["enable_thinking"]; got != tc.wantOn {
				t.Fatalf("enable_thinking = %v, want %v", got, tc.wantOn)
			}
		})
	}
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

// TestStreamClientHasNoTotalTimeout 守门：流式必须走**无总超时**的 client。
//
// 背景（2026-09-24 实测）：http.Client.Timeout 是整请求上限，计时器在 Do() 返回后继续跑
// 直到 body 读完；SSE 整个生成期都在读 body，故长思考请求会被从流中间掐断 ——
// 日志两次中断都恰好 120.00s（= config.upstream.timeout_seconds），
// 客户端表现为「突然无响应」。非流式 client 必须保留总超时（短请求的合理兜底）。
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
// 这是「突然无响应」的直接成因 —— 此前 sc.Err() != nil 时直接 return，
// 客户端收到一条没有 [DONE] 的截断流，只能一直等（或判定会话损坏）。
func TestStreamTruncationEmitsFrames(t *testing.T) {
	// 前半段正常，随后读错误（模拟空闲超时/连接被切断）。
	in := "data: " + `{"id":"1","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{"content":"there"}}]}` + "\n\n"
	rc := &errAfterReader{data: in, err: provider.ErrIdleTimeout}

	rec := httptest.NewRecorder()
	_, err := Stream(rec, rc, "loomy/spark-x")
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
