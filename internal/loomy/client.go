package loomy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

// Client 讯飞 Loomy 上游客户端。
// 与既有渠道不同，本渠道不做登录编排（凭据由「从本机客户端导入」得到），
// 且**无 refresh 端点**：session 到期需重新导入。
type Client struct {
	HTTP *http.Client
}

// New 默认 180s 超时。
func New() *Client { return NewWithTimeout(180 * time.Second) }

// NewWithTimeout 指定超时。
func NewWithTimeout(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &Client{HTTP: &http.Client{Timeout: timeout}}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 180 * time.Second}
	}
	return c.HTTP
}

func session(a *auth.Auth) string { return strings.TrimSpace(a.AccessTokenValue()) }

// traceparent 生成 W3C traceparent；官方源码注释实测：**缺这个头 chat/completions 会挂死到超时**。
func traceparent() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00-" + strings.Repeat("0", 32) + "-" + strings.Repeat("0", 16) + "-01"
	}
	return "00-" + hex.EncodeToString(b[:16]) + "-" + hex.EncodeToString(b[16:]) + "-01"
}

// applyAuth 写鉴权与追踪头（官方客户端 session 模式是 token 头 + Authorization **双写**）。
func applyAuth(req *http.Request, a *auth.Auth) {
	tok := session(a)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("token", tok)
	req.Header.Set("traceparent", traceparent())
	req.Header.Set("loomy-version", AppVersion)
	req.Header.Set("Accept", "application/json")
}

// do 发一次带鉴权的非流式请求。
func (c *Client) do(method, url string, a *auth.Auth, body []byte) (*http.Response, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyAuth(req, a)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp, raw, rerr
}

// ---------------------------------------------------------------------------
// 凭据
// ---------------------------------------------------------------------------

// RefreshToken 实现 provider.Upstream。
// 上游**没有 refresh 端点**（客户端登录时请求 14 天有效期的 session，见 docs 备忘）：
// `utils/remote-keepalive.js` 经查是电源保活，与登录态无关。因此这里返回明确错误，
// 让调用方走 session_dead 分支提示用户重新导入，而不是发出一个必然失败的请求。
func (c *Client) RefreshToken(a *auth.Auth) error {
	return &provider.Error{
		Kind: provider.ErrSessionDead, Status: 0,
		Msg: "loomy: 上游无 refresh 端点（session 有效期 14 天），请在面板重新「从本机客户端导入」",
	}
}

// ---------------------------------------------------------------------------
// 推理
// ---------------------------------------------------------------------------

// ChatStream 实现 provider.Upstream。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	if m := modelOf(body); m != "" && !KnownModel(m) {
		return nil, http.StatusBadRequest, []byte(unknownModelBody(m)), nil
	}
	projected := projectEffort(body)

	req, err := http.NewRequest(http.MethodPost, GatewayBase+EpChat, bytes.NewReader(projected))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuth(req, a)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, resp.StatusCode, raw, nil
	}
	return resp.Body, resp.StatusCode, nil, nil
}

// projectEffort 把顶层 `reasoning_effort` 投影成官方客户端的三件套方言。
//
// 官方形态（桌面版 `llm-completion.js` 实测）：
//
//	{ reasoning_effort, enable_thinking, chat_template_kwargs: { enable_thinking } }
//
// 阶段 C 实测：三件套能把思考从 98 字降到 59 字（单调递减），但该模型**无法完全关闭**思考。
// 只在客户端确实表达了档位时才补字段（未表达时保持原样，避免替客户端做决定 —— 对齐 R19 的思路）。
func projectEffort(body []byte) []byte {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	raw, ok := obj["reasoning_effort"]
	if !ok {
		return body
	}
	effort, _ := raw.(string)
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return body
	}
	on := effort != "none"
	obj["enable_thinking"] = on
	if _, exists := obj["chat_template_kwargs"]; !exists {
		obj["chat_template_kwargs"] = map[string]any{"enable_thinking": on}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

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

// unknownModelBody 未知模型的本地 400（上游会静默回落默认模型，故本地拒绝）。
func unknownModelBody(m string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": fmt.Sprintf("loomy: 未知模型 %q。上游对未知模型会静默回落到默认模型，故本地直接拒绝；可用模型见 GET /v1/models", m),
		"type":    "invalid_request_error",
		"code":    "model_not_found",
	}})
	return string(b)
}

// ---------------------------------------------------------------------------
// 模型与费率
// ---------------------------------------------------------------------------

type modelsResp struct {
	ReasoningEnabled        bool           `json:"reasoning_enabled"`
	ReasoningCatalogVersion string         `json:"reasoning_catalog_version"`
	Data                    []loomyModel   `json:"data"`
}

type loomyModel struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Protocol               string   `json:"protocol"`
	ContextLength          int64    `json:"context_length"`
	MaxOutputTokens        int64    `json:"max_output_tokens"`
	ReasoningEfforts       []string `json:"reasoning_efforts"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort"`
	IsDefaultModel         bool     `json:"is_default_model"`
	Capabilities           struct {
		Reasoning       bool     `json:"reasoning"`
		Vision          bool     `json:"vision"`
		FunctionCalling bool     `json:"function_calling"`
		Streaming       bool     `json:"streaming"`
		InputModalities []string `json:"input_modalities"`
	} `json:"capabilities"`
}

// fetchModels 拉取上游模型目录。
func (c *Client) fetchModels(a *auth.Auth) (*modelsResp, error) {
	resp, raw, err := c.do(http.MethodGet, GatewayBase+EpModels, a, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &provider.Error{
			Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 300),
		}
	}
	var out modelsResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("loomy 模型目录解析失败: %w (%s)", err, truncate(string(raw), 200))
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("loomy 模型目录为空")
	}
	return &out, nil
}

// FetchModels 实现 provider.Upstream（档位直接取上游 reasoning_efforts —— 权威值）。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	resp, err := c.fetchModels(a)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ModelInfo, 0, len(resp.Data))
	for _, m := range resp.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		name := cleanName(m.Name)
		if name == "" {
			name = id
		}
		info := provider.ModelInfo{
			ID:                 id,
			Name:               name,
			ContextWindow:      m.ContextLength,
			MaxTokens:          m.MaxOutputTokens,
			ContextFromAPI:     m.ContextLength > 0,
			SupportsImages:     m.Capabilities.Vision,
			SupportsReasoning:  m.Capabilities.Reasoning,
			SupportsTools:      m.Capabilities.FunctionCalling,
			SupportedEfforts:   append([]string{}, m.ReasoningEfforts...),
			DefaultEffort:      strings.TrimSpace(m.DefaultReasoningEffort),
			ReasoningCanDisable: false, // 实测该系列"强制思考"，最低档仍会产生思考
		}
		out = append(out, info)
	}
	return out, nil
}

// FetchModelPricing 实现 provider.Upstream：倍率写在模型显示名里（`（x3.0）`），需解析。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	resp, err := c.fetchModels(a)
	if err != nil {
		return nil, err
	}
	explicit := true
	out := make([]provider.ModelPricing, 0, len(resp.Data))
	for _, m := range resp.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		rate := priceFromName(m.Name)
		if rate <= 0 {
			// 解析不到倍率时必须置 Explicit=false：面板据此显示「未知」而不是「免费」（R5 约定）。
			explicit = false
		}
		out = append(out, provider.ModelPricing{
			Model:    id,
			Channel:  ChannelName,
			Rate:     rate,
			Note:     cleanName(m.Name),
			Explicit: &explicit,
		})
		explicit = true
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 额度 / 签到
// ---------------------------------------------------------------------------

type pointsRecord struct {
	Balance *int64 `json:"balance"`
	Points  *int64 `json:"points"`
	Data    struct {
		Balance        *int64 `json:"balance"`
		CurrentBalance *int64 `json:"currentBalance"`
		RemainPoints   *int64 `json:"remainPoints"`
		TotalPoints    *int64 `json:"totalPoints"`
	} `json:"data"`
}

// UserResource 实现 provider.Upstream：读积分记录接口的余额字段。
//
// 上游积分以「积分」为单位（推理响应里的 `usage.points_consumed` 即扣减量）。
// 端点在阶段 A 静态取证确认、字段名按宽松多候选解析；解析不到一律返回 0（宁缺勿错）。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	remain, _, err := c.UserResourceDetail(a)
	return remain, err
}

// UserResourceDetail 实现 provider.Upstream。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	resp, raw, err := c.do(http.MethodGet, GatewayBase+EpPointsV2, a, nil)
	if err != nil {
		return 0, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, nil, &provider.Error{
			Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 300),
		}
	}
	// 上游可能返回 HTTP 200 + 业务错误码（同 100002 的形态）。
	if k := businessErrKind(string(raw)); k != provider.ErrNone {
		return 0, nil, &provider.Error{Kind: k, Status: resp.StatusCode, Msg: truncate(string(raw), 300)}
	}
	var rec pointsRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return 0, nil, nil // 结构未知：不报错、不留余额（避免把账号误判为异常）
	}
	if v := firstInt(rec.Balance, rec.Points, rec.Data.Balance, rec.Data.CurrentBalance, rec.Data.RemainPoints, rec.Data.TotalPoints); v != nil {
		item := provider.ResourceItem{Name: "积分", Remain: *v, Usable: true, Total: *v}
		return *v, []provider.ResourceItem{item}, nil
	}
	return 0, nil, nil
}

func firstInt(vals ...*int64) *int64 {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// DailyCheckin 实现 provider.Upstream：一期不做签到。
// 上游有 `/pet-work`（快照）+ `/pet-work/rewards`（领奖），但属"宠物任务"而非明确签到，
// 且用户已同意签到可豁免（计划 D1）。若二期要做，只需在此实现。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	return fmt.Errorf("loomy 暂未实现签到（pet-work 每日任务待二期评估）")
}

// ---------------------------------------------------------------------------
// 错误分类
// ---------------------------------------------------------------------------

// Classify 实现 provider.Upstream。
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// businessErrKind 识别"HTTP 200 + 业务码"形态的失败（Loomy 的鉴权失效就是这样返回的）。
// 返回 ErrNone 表示不是已知业务错误。
func businessErrKind(body string) provider.ErrKind {
	lower := strings.ToLower(body)
	// 100002 登录态失效；020002 会话校验失败（源码 AUTH_ERROR_CODES）。
	// CodeMarker 的第一个参数要求是**已小写化**的 body。
	if provider.CodeMarker(lower, "100002") || provider.CodeMarker(lower, "020002") {
		return provider.ErrSessionDead
	}
	if strings.Contains(lower, "model_not_found") {
		return provider.ErrBadParams
	}
	return provider.ErrNone
}

// Classify 包级分类。
//
// ⚠️ 顺序关键：上游把**鉴权失效塞在 HTTP 200** 里（{"code":"100002"}），
// 所以业务码判定必须排在状态码判定之前（对齐 AGENTS §6.25）。
func Classify(status int, body string) provider.ErrKind {
	if k := businessErrKind(body); k != provider.ErrNone {
		return k
	}
	lower := strings.ToLower(body)

	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return provider.ErrSessionDead
	}
	if status == http.StatusPaymentRequired {
		return provider.ErrHardCredit
	}
	// 429 优先于 hardMarkers（§6.15）。
	if status == http.StatusTooManyRequests {
		return provider.ErrSoftRate
	}
	for _, m := range hardMarkers {
		if strings.Contains(lower, m) {
			return provider.ErrHardCredit
		}
	}
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "请求过于频繁") {
		return provider.ErrSoftRate
	}
	if status == http.StatusNotFound {
		return provider.ErrNotFound
	}
	if status >= 500 {
		return provider.ErrServer
	}
	if status >= 400 {
		switch {
		case strings.Contains(body, "messages 不能为空"):
			return provider.ErrBadParams
		case strings.Contains(lower, "context length"), strings.Contains(lower, "too long"), strings.Contains(lower, "tokens exceed"):
			return provider.ErrPromptTooLong
		case strings.Contains(lower, "invalid image"), strings.Contains(lower, "image_url"):
			return provider.ErrImageInvalid
		case strings.Contains(lower, "content policy"), strings.Contains(lower, "sensitive"):
			return provider.ErrContentBlocked
		}
		return provider.ErrBadParams
	}
	return provider.ErrNone
}

// hardMarkers 余额/积分不足关键词。
var hardMarkers = []string{
	"积分不足", "额度不足", "余额不足", "积分用完", "额度用尽",
	"insufficient credit", "no credit", "credit exhausted", "out of credit",
	"quota exceeded", "quota exhaust", "payment required",
}

// truncate rune 安全截断。
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
