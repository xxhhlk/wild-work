// client.go MonkeyCode 平台托管模型渠道客户端，实现 provider.Upstream。
//
// 渠道形态：**B（从本机官方客户端导入凭据）** —— 一个账号 = 一对
// `oma_` api_key + `omas_` signing_secret，无 uid、无 refresh 端点。
// 凭据由 internal/app 的 ImportLocal 从 ohmyagent 的 settings.json 读出，
// 落成 `auths/monkeycode-<派生标识>.json`。
//
// 上游是 **Anthropic Messages** 形状（`{base}/messages`），因此本渠道做两件
// 方言投影：请求 OpenAI Chat → Anthropic（request.go），响应 Anthropic SSE →
// OpenAI SSE（sse.go）。签名见 sign.go。
package monkeycode

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
	"wild-work/internal/upstream"
)

// Client 上游 HTTP 客户端。Base 可覆盖以便测试。
type Client struct {
	HTTP *http.Client
	Base string
}

// New 生产默认客户端。
func New() *Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSNextProto:          make(map[string]func(string, *tls.Conn) http.RoundTripper),
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
	}
	return &Client{HTTP: &http.Client{Timeout: requestTimeout, Transport: tr}, Base: DefaultBase}
}

// NewWithBase 测试用：覆盖上游基址。
func NewWithBase(base string) *Client {
	c := New()
	c.Base = base
	return c
}

// base 返回该账号的上游基址（账号自带 ApiHost+Domain 优先，否则回默认）。
func (c *Client) base(a *auth.Auth) string {
	if a != nil {
		host := strings.TrimSpace(a.ApiHost)
		if host != "" {
			path := strings.TrimSpace(a.Domain)
			if path == "" {
				path = "/v1"
			}
			return strings.TrimRight(host, "/") + "/" + strings.TrimLeft(path, "/")
		}
	}
	if c.Base != "" {
		return c.Base
	}
	return DefaultBase
}

// StaticModels 上游无 GET /models（405），模型表为静态清单。
func StaticModels() []provider.ModelInfo { return modelInfos() }

// modelInfos 构造静态模型表。
//
// ContextWindow/MaxTokens 取自客户端 settings.json 的托管条目（200000 / 32000），
// 是上游声明的真实容量而非估算，故 ContextFromAPI 置 true —— 让 /v1/models
// 正常透出容量，客户端据此做上下文预算。
func modelInfos() []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(staticModels))
	for _, id := range staticModels {
		name := modelDisplayNames[id]
		if name == "" {
			name = id
		}
		out = append(out, provider.ModelInfo{
			ID:             id,
			Name:           name,
			ContextWindow:  200000,
			MaxTokens:      32000,
			ContextFromAPI: true,
			SupportsTools:  true,
		})
	}
	return out
}

// modelAllowed 报告模型是否属于本渠道托管清单。
// 未知模型**必须本地拒绝**：上游对未知模型会静默回落到默认模型并返回 200，
// 不校验就等于静默烧额度（同 raccoon / loomy 的硬约束）。
func modelAllowed(id string) bool {
	return staticSet[bareModelID(id)]
}

// unknownModelBody 未知模型的本地 400（OpenAI 风格，不请求上游）。
func unknownModelBody(m string) []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": fmt.Sprintf("monkeycode: 未知模型 %q。上游对未知模型会静默回落到默认模型，故本地直接拒绝；可用模型见 GET /v1/models", m),
		"type":    "invalid_request_error",
		"code":    "model_not_found",
	}})
	return b
}

// ---------------------------------------------------------------------------
// provider.Upstream 实现
// ---------------------------------------------------------------------------

// ChatStream 把 OpenAI Chat 请求体翻成上游方言（Anthropic Messages 或
// OpenAI Responses，取决于模型），注入签名后转发。
//
// 两条路径的差异（2026-09-24 实测，见评估文档 §3.9）：
//
//	anthropic 型：POST {base}/messages，签名对象 = system[0].text
//	responses 型：POST {base}/responses，签名对象 = input[0](role=system).content
//
// 两处的签名对象都是同一个固定常量 signatureSystemPrompt，故签名值相同、共用缓存。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (io.ReadCloser, int, []byte, error) {
	model := modelOf(body)
	if !modelAllowed(model) {
		return nil, http.StatusBadRequest, unknownModelBody(model), nil
	}
	useResponses := usesResponsesAPI(model)

	var (
		out  []byte
		path string
		err  error
	)
	if useResponses {
		out, err = buildResponsesBody(body)
		path = "/responses"
	} else {
		out, err = buildAnthropicBody(body)
		path = "/messages"
	}
	if err != nil {
		return nil, 0, nil, err
	}

	sig, err := signatureFor(secretOf(a))
	if err != nil {
		return nil, 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.base(a)+path, bytes.NewReader(out))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Api-Key", apiKeyOf(a))
	req.Header.Set(SignatureHeader, sig)
	if !useResponses {
		// Responses 面不需要 anthropic-version（实测不发送亦 200）
		req.Header.Set("Anthropic-Version", anthropicVersion)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, resp.StatusCode, raw, nil
	}
	return resp.Body, resp.StatusCode, nil, nil
}

// TestKey 用最小请求验证凭据可用性（面板「测试」按钮）。
// 返回 (status, 上游原文片段, error)；200/429 都算连通，401/403 表示凭据或签名问题。
//
// 用清单里第一条 anthropic 型模型（响应面最省事、无推理摘要噪声）。
func (c *Client) TestKey(a *auth.Auth) (int, string, error) {
	model := upstreamModelName(staticModels[0])
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 8,
		"stream":     true,
		"system":     []any{map[string]any{"type": "text", "text": signatureSystemPrompt}},
		"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
		"thinking":   map[string]any{"type": "disabled"},
	})
	sig, err := signatureFor(secretOf(a))
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPost, c.base(a)+"/messages", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", anthropicVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Api-Key", apiKeyOf(a))
	req.Header.Set(SignatureHeader, sig)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, string(raw), nil
}

// FetchModels 上游无目录接口，恒回静态清单。
func (c *Client) FetchModels(_ *auth.Auth) ([]provider.ModelInfo, error) {
	return modelInfos(), nil
}

// FetchModelPricing 上游无费率接口，返回空（面板显示未知，而非「免费」）。
func (c *Client) FetchModelPricing(_ *auth.Auth) ([]provider.ModelPricing, error) {
	return nil, nil
}

// RefreshToken 空实现：本渠道无 refresh 端点。
// 账号 ExpiresAt 由导入器置为远期值，正常路径不会走到这里；
// 即便走到也不报错，避免把「没有刷新机制」误判成会话失效。
func (c *Client) RefreshToken(_ *auth.Auth) error { return nil }

// UserResource 上游无额度查询接口，恒 0（面板显示「不适用」）。
func (c *Client) UserResource(_ *auth.Auth) (int64, error) { return 0, nil }

// UserResourceDetail 同上：无明细条目。
func (c *Client) UserResourceDetail(_ *auth.Auth) (int64, []provider.ResourceItem, error) {
	return 0, nil, nil
}

// DailyCheckin 本渠道无签到活动（渠道声明 noExplicitCheckin，调度器不会调用）。
func (c *Client) DailyCheckin(_ *auth.Auth) error {
	return fmt.Errorf("monkeycode 渠道无签到活动")
}

// Classify 上游错误分类。
//
// 关键：`403 {"error":"invalid ohmyagent request"}` 是**签名/协议错误**，
// 不是账号问题 → 必须归 ErrPassthrough（原文透传、不罚号）。
// 若按「403 = 账号不可用」处理会把健康账号喂到冷却，整条渠道随签名细节波动下线。
func (c *Client) Classify(status int, body string) provider.ErrKind {
	if provider.CodeMarker(strings.ToLower(body), "model_not_found") {
		return provider.ErrBadParams // 本地拒绝的未知模型：请求级，不罚号
	}
	switch {
	case status == http.StatusUnauthorized:
		return provider.ErrSessionDead // key 失效：需重新导入客户端凭据
	case status == http.StatusTooManyRequests:
		return provider.ErrSoftRate
	case status >= 500:
		return provider.ErrServer
	case status >= 400:
		// 403（签名不匹配）/400（body 不被接受）等一律请求级透传
		return provider.ErrPassthrough
	default:
		return provider.ErrNone
	}
}

// Stream 把上游流（Anthropic SSE 或 Responses SSE）转成 OpenAI SSE 后
// 交给统一规范化层。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader, model string) (map[string]any, error) {
	var usage map[string]any
	err := upstream.StreamCapture(w, newUpstreamStream(r, model), func(u map[string]any) { usage = u })
	return usage, err
}

// Aggregate 把上游流收敛成单个 OpenAI chat.completion 响应。
func (c *Client) Aggregate(r io.Reader, model string) (map[string]any, error) {
	return upstream.Aggregate(newUpstreamStream(r, model))
}

// newUpstreamStream 按模型选择上游流转换器（入参含渠道前缀，用 bareModelID 归一）。
func newUpstreamStream(r io.Reader, model string) io.Reader {
	if usesResponsesAPI(model) {
		return newResponsesStream(r, model)
	}
	return newAnthropicStream(r, model)
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// modelOf 取请求体里的 model。
func modelOf(body []byte) string {
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Model)
}

// apiKeyOf 取账号的 api_key（oma_…）。兼容手填 auth 文件时写在 refreshToken 的情况。
func apiKeyOf(a *auth.Auth) string {
	if a == nil {
		return ""
	}
	if k := strings.TrimSpace(a.AccessToken); k != "" {
		return k
	}
	return strings.TrimSpace(a.RefreshToken)
}

// secretOf 取账号的 signing_secret（omas_…）。
func secretOf(a *auth.Auth) string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.SigningSecret)
}
