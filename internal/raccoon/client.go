package raccoon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

// Client 商汤小浣熊（官方托管网关 xiaohuanxiong.com）上游客户端。
// 与既有渠道不同，本渠道**不做登录编排**：凭据由「从本机客户端导入」得到
// （见 docs/raccoon渠道接入备忘.md §5），因此不实现 FetchNickname 之外的登录态工具。
type Client struct {
	HTTP *http.Client
}

// New 默认 180s 超时的客户端。
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

func accessToken(a *auth.Auth) string { return strings.TrimSpace(a.AccessTokenValue()) }

// do 发一次带 Bearer 的请求（body 为 nil 时视为无请求体）。
// 只用于非流式调用；流式（ChatStream）需要保留 resp.Body，另行实现。
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
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken(a))
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp, raw, rerr
}

// ---------------------------------------------------------------------------
// 凭据刷新
// ---------------------------------------------------------------------------

// RefreshToken 用 refresh_token 换新 access_token。
//
// 上游会**轮换 refresh_token**（阶段 C 实测 + 官方 scheduleAuth.js），故响应里带了新值时必须一并更新；
// 落盘由调用方完成（AGENTS §6.20：凡调 RefreshToken 必紧跟 SaveAtomic）。
func (c *Client) RefreshToken(a *auth.Auth) error {
	rt := strings.TrimSpace(a.RefreshTokenValue())
	if rt == "" {
		return fmt.Errorf("raccoon: 无 refresh token，需重新从客户端导入凭据")
	}
	payload, err := json.Marshal(map[string]string{"refresh_token": rt})
	if err != nil {
		return err
	}
	resp, raw, err := c.do(http.MethodPost, BaseURL+EpAuthRefresh, a, payload)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		// 桌面端前缀 /api/electron/auth/v1 与代码兜底 /api/web/auth/v1 在不同版本上不同，逐个尝试。
		resp, raw, err = c.do(http.MethodPost, BaseURL+EpAuthRefreshWeb, a, payload)
		if err != nil {
			return err
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &provider.Error{Kind: provider.ErrSessionDead, Status: resp.StatusCode, Msg: truncate(string(raw), 300)}
	}
	if resp.StatusCode != http.StatusOK {
		return &provider.Error{Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 300)}
	}
	var out struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("raccoon refresh 响应解析失败: %w (%s)", err, truncate(string(raw), 200))
	}
	if out.Data.AccessToken == "" {
		return fmt.Errorf("raccoon refresh 未返回 access_token: %s", truncate(string(raw), 200))
	}
	a.Lock()
	a.AccessToken = out.Data.AccessToken
	if out.Data.RefreshToken != "" {
		a.RefreshToken = out.Data.RefreshToken // 上游轮换
	}
	a.ExpiresAt = jwtExp(out.Data.AccessToken)
	a.Unlock()
	return nil
}

// jwtExp 从 JWT 取 exp（Unix 秒）；解析失败返回 0（NeedsRefresh 会视作需刷新）。
func jwtExp(tok string) int64 {
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) < 2 {
		return 0
	}
	seg := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	if m := len(seg) % 4; m != 0 {
		seg += strings.Repeat("=", 4-m)
	}
	raw, err := base64.StdEncoding.DecodeString(seg)
	if err != nil {
		return 0
	}
	var p struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0
	}
	return p.Exp
}

// ---------------------------------------------------------------------------
// 推理
// ---------------------------------------------------------------------------

// ChatStream 发流式（或非流式）对话请求。
// 返回 (resp.Body, status, nil, nil) 表示成功；上游 4xx/5xx 时返回 (nil, status, body, nil)。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	// 模型名本地校验：阶段 C 实测确认上游对未知模型**静默回落到默认模型**并返回 200，
	// 不校验会让用户以为在用 A 模型、实际消耗 B 模型的额度。
	if m := modelOf(body); m != "" && !KnownModel(m) {
		return nil, http.StatusBadRequest, []byte(unknownModelBody(m)), nil
	}

	req, err := http.NewRequest(http.MethodPost, LLMBase+EpChat, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken(a))
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

// modelOf 取请求体里的 model（解析失败返回空串）。
func modelOf(body []byte) string {
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Model)
}

// unknownModelBody 未知模型时返回的 OpenAI 风格 400（不请求上游）。
func unknownModelBody(m string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": fmt.Sprintf("raccoon: 未知模型 %q。上游对未知模型会静默回落到默认模型，故本地直接拒绝；可用模型见 GET /v1/models", m),
		"type":    "invalid_request_error",
		"code":    "model_not_found",
	}})
	return string(b)
}

// ---------------------------------------------------------------------------
// 模型与费率
// ---------------------------------------------------------------------------

type catalogResp struct {
	Code int `json:"code"`
	Data struct {
		Categories []struct {
			Type         string         `json:"type"`
			DefaultModel string         `json:"default_model"`
			Models       []catalogModel `json:"models"`
		} `json:"categories"`
	} `json:"data"`
}

type catalogModel struct {
	Name              string   `json:"name"`
	ModelName         string   `json:"model_name"`
	Description       string   `json:"description"`
	DisplayDesc       string   `json:"display_description"`
	Visible           bool     `json:"visible"`
	BillingCategory   string   `json:"billing_category"`
	BillingMultiplier float64  `json:"billing_multiplier"`
	Tags              []string `json:"tags"`
	AbilityLevel      int      `json:"ability_level"`
	Params            struct {
		ContextWindow int64 `json:"context_window"`
		MaxTokens     int64 `json:"max_tokens"`
	} `json:"params"`
}

// fetchCatalog 拉取模型目录（模型表与费率共用同一响应）。
func (c *Client) fetchCatalog(a *auth.Auth) (*catalogResp, []catalogModel, error) {
	resp, raw, err := c.do(http.MethodGet, LLMBase+EpModelCatalog, a, nil)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, &provider.Error{
			Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 300),
		}
	}
	var out catalogResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("raccoon 模型目录解析失败: %w (%s)", err, truncate(string(raw), 200))
	}
	var models []catalogModel
	for _, cat := range out.Data.Categories {
		models = append(models, cat.Models...)
	}
	return &out, models, nil
}

// FetchModels 实现 provider.Upstream。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	_, models, err := c.fetchCatalog(a)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ModelInfo, 0, len(models))
	for _, m := range models {
		id := strings.TrimSpace(m.ModelName)
		if id == "" {
			id = strings.TrimSpace(m.Name)
		}
		if id == "" {
			continue
		}
		name := strings.TrimSpace(m.Description)
		if name == "" {
			name = id
		}
		info := provider.ModelInfo{
			ID:             id,
			Name:           name,
			ContextWindow:  m.Params.ContextWindow,
			MaxTokens:      m.Params.MaxTokens,
			ContextFromAPI: m.Params.ContextWindow > 0,
			SupportsImages: hasTag(m.Tags, "vision"),
			// 本渠道不投影档位（上游目录未声明 reasoning_efforts，见阶段 C 实测），
			// 仅如实声明模型是否具备推理标签，便于客户端自行选择。
			SupportsReasoning: hasTag(m.Tags, "reasoning"),
			SupportsTools:     false, // 未实测，按 provider 约定「未知即不声明」
		}
		out = append(out, info)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("raccoon 模型目录为空")
	}
	return out, nil
}

// FetchModelPricing 实现 provider.Upstream：倍率直接取上游 billing_multiplier。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	_, models, err := c.fetchCatalog(a)
	if err != nil {
		return nil, err
	}
	explicit := true
	out := make([]provider.ModelPricing, 0, len(models))
	for _, m := range models {
		id := strings.TrimSpace(m.ModelName)
		if id == "" {
			id = strings.TrimSpace(m.Name)
		}
		if id == "" {
			continue
		}
		note := strings.TrimSpace(m.BillingCategory)
		if m.Visible {
			// 可见模型即客户端默认展示的模型，面板给个更直观的说明。
			if note == "" {
				note = "official"
			}
		} else {
			note = "internal"
		}
		out = append(out, provider.ModelPricing{
			Model:    id,
			Channel:  ChannelName,
			Rate:     m.BillingMultiplier,
			Note:     note,
			Explicit: &explicit,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 额度 / 签到
// ---------------------------------------------------------------------------

// UserResource 实现 provider.Upstream：可用积分（`available_points`）。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	remain, _, err := c.UserResourceDetail(a)
	return remain, err
}

// pointsBalanceData /api/web/points/v1/balance 的 data 段。
type pointsBalanceData struct {
	AvailablePoints int64 `json:"available_points"`
	DailyPoints     int64 `json:"daily_points"`
	MonthlyPoints   int64 `json:"monthly_points"`
	RewardPoints    int64 `json:"reward_points"`
	TopupPoints     int64 `json:"topup_points"`
	TopupFrozen     bool  `json:"topup_frozen"`
}

// pointsBalanceResp /api/web/points/v1/balance 的响应（2026-09-22 实测）。
type pointsBalanceResp struct {
	Code int               `json:"code"`
	Data pointsBalanceData `json:"data"`
}

// UserResourceDetail 实现 provider.Upstream：拉取积分余额并按上游的池子拆分条目。
//
// 端点 `GET {站点根}/api/web/points/v1/balance`（**与推理网关不同前缀**：推理在
// `/api/web/llm/v2`，积分在 `/api/web/points/v1`）。上游把额度分成四个池：
// 每日 / 月度 / 奖励 / 充值，另有 `topup_frozen` 标记充值池是否被冻结 —— 冻结时该池
// 对用户「看得见用不了」，正好对应 `provider.ResourceItem.Usable` 的语义。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	resp, raw, err := c.do(http.MethodGet, BaseURL+EpPointsBalance, a, nil)
	if err != nil {
		return 0, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, nil, &provider.Error{
			Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 300),
		}
	}
	var out pointsBalanceResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, nil, fmt.Errorf("raccoon 积分响应解析失败: %w (%s)", err, truncate(string(raw), 200))
	}
	// 业务码非 0 视为失败：points 服务用 `100002` 表示**参数错误**（与鉴权无关 ——
	// 这也是本渠道不按业务码判会话失效、只看 HTTP 状态的原因）。
	if out.Code != 0 {
		kind := provider.ErrClient
		if resp.StatusCode == http.StatusUnauthorized {
			kind = provider.ErrSessionDead
		}
		return 0, nil, &provider.Error{Kind: kind, Status: resp.StatusCode, Msg: truncate(string(raw), 300)}
	}
	remain, items := parsePointsBalance(out.Data)
	return remain, items, nil
}

// parsePointsBalance 把上游余额结构转成 (可用余额, 明细条目)。
// 抽成纯函数便于单测（Client 的 baseURL 是常量，不便在测试里换 host）。
func parsePointsBalance(d pointsBalanceData) (int64, []provider.ResourceItem) {
	items := []provider.ResourceItem{
		{Name: "每日额度", Total: d.DailyPoints, Remain: d.DailyPoints, Usable: true},
		{Name: "奖励积分", Total: d.RewardPoints, Remain: d.RewardPoints, Usable: true},
		{Name: "充值积分", Total: d.TopupPoints, Remain: d.TopupPoints, Usable: !d.TopupFrozen},
		{Name: "月度积分", Total: d.MonthlyPoints, Remain: d.MonthlyPoints, Usable: true},
	}
	return d.AvailablePoints, items
}

// DailyCheckin 实现 provider.Upstream：小浣熊无签到活动。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	return fmt.Errorf("raccoon 无签到活动")
}

// ---------------------------------------------------------------------------
// 错误分类
// ---------------------------------------------------------------------------

// Classify 按 HTTP 状态码 + body 判定错误类别（实现 provider.Upstream）。
//
// 上游错误信封为 LiteLLM 风格（阶段 C 实测）：
//
//	401 {"code":200001,"message":"authorization_empty_error"}
//	401 {"code":200003,"message":"authorization_verify_error"}
//	400 {"error":{"code":"400","message":"litellm.BadRequestError: Custom_raccoonException - ..."}}
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// Classify 包级分类（便于单测与复用）。
func Classify(status int, body string) provider.ErrKind {
	lower := strings.ToLower(body)

	// 本地拒绝的未知模型（见 ChatStream）：请求级错误，不罚号。
	if strings.Contains(lower, "model_not_found") {
		return provider.ErrBadParams
	}
	// 鉴权：上游用 401 + 200001/200003（缺 token / 校验失败）。
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return provider.ErrSessionDead
	}
	if status == http.StatusPaymentRequired {
		return provider.ErrHardCredit
	}
	// 429 优先于 hardMarkers（限流 body 常带 quota 字样，见 AGENTS §6.15）。
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
		// 上下文超限 / 内容拦截 / 参数错误都是请求级：不罚号、原文透传（AGENTS §6.25）。
		switch {
		case strings.Contains(lower, "message is bigger than"), strings.Contains(lower, "context length"),
			strings.Contains(lower, "context_length_exceeded"), strings.Contains(lower, "too long"):
			return provider.ErrPromptTooLong
		case strings.Contains(lower, "content policy"), strings.Contains(lower, "content filter"),
			strings.Contains(lower, "sensitive"):
			return provider.ErrContentBlocked
		case strings.Contains(lower, "invalid image"), strings.Contains(lower, "image_url"):
			return provider.ErrImageInvalid
		}
		return provider.ErrBadParams
	}
	return provider.ErrNone
}

// hardMarkers 余额/权益不足关键词。
var hardMarkers = []string{
	"insufficient credit", "no credit", "credit exhausted", "out of credit",
	"quota exceeded", "quota exhaust", "payment required", "credit not enough",
	"not enough credit", "积分不足", "额度不足", "余额不足", "积分用完", "额度用尽",
}

// hasTag 判断 tags 是否含指定标签（大小写不敏感）。
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}

// truncate 字符串截断（rune 安全）。
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
