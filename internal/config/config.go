// Package config 加载 JSON 配置 + 环境变量覆盖，并支持原子写回（GUI 面板修改）。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wild-work/internal/reasoning"
)

// Listen 监听地址：Host 为空表示全部接口。
type Listen struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Addr 返回 net.Listen 使用的地址串，如 "127.0.0.1:7863" / ":7863"。
func (l Listen) Addr() string {
	host := l.Host
	port := l.Port
	if port <= 0 {
		port = 7863
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// SameAddr 判断两个 net.Listen 地址串是否指向同一个监听地址。
// 空 host、0.0.0.0、:: 在 net.Listen 语义下都是「全部接口」，但字符串形式不同：
// 旧版配置写 ":7863"（或 WILDWORK_LISTEN=":7777"）时 Host 为空，面板会把监听主机
// 回显成 0.0.0.0，保存时回传 "0.0.0.0:7863"——字符串不等会被误判成「改了监听地址」，
// 于是在同一端口上重新 net.Listen，Windows 下撞自身报「端口已被占用」。
func SameAddr(a, b string) bool {
	ah, ap, errA := net.SplitHostPort(a)
	bh, bp, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return a == b // 解析失败（裸端口等）退化为字符串比较
	}
	return ap == bp && normalizeListenHost(ah) == normalizeListenHost(bh)
}

// normalizeListenHost 把「全部接口」的各种写法归一化，其余 host 原样返回。
func normalizeListenHost(host string) string {
	switch strings.TrimSpace(host) {
	case "", "0.0.0.0", "::", "[::]", "*":
		return ""
	}
	return host
}

// UnmarshalJSON 兼容旧版字符串形式（":7863" / "127.0.0.1:9999" / "9999"）。
func (l *Listen) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		l2, err := ParseListen(s)
		if err != nil {
			return err
		}
		*l = l2
		return nil
	}
	var o struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	l.Host, l.Port = o.Host, o.Port
	return nil
}

// ParseListen 解析 "host:port" / ":port" / "port" 三种形式。
func ParseListen(s string) (Listen, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Listen{}, fmt.Errorf("empty listen address")
	}
	host, portStr := s, ""
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host, portStr = s[:i], s[i+1:]
	} else if n, err := strconv.Atoi(host); err == nil {
		// 无冒号且整体是数字 → 仅端口（兼容旧配置 "7863"）
		return Listen{Port: n}, nil
	}
	port := 0
	if portStr != "" {
		n, err := strconv.Atoi(portStr)
		if err != nil || n <= 0 || n > 65535 {
			return Listen{}, fmt.Errorf("bad listen port %q", portStr)
		}
		port = n
	}
	return Listen{Host: host, Port: port}, nil
}

// Config 顶层配置。
type Config struct {
	Listen    Listen `json:"listen"`
	APIKey    string `json:"api_key"`    // 空 = 不鉴权
	AuthDir   string `json:"auth_dir"`   // ./auths
	StateFile string `json:"state_file"` // ./data/state.json
	Region    string `json:"region"`     // 只收 "cn"

	Cooldown struct {
		HardCredit  string `json:"hard_credit"`   // "12h"
		SoftRate    string `json:"soft_rate"`     // "60s"
		ErrThresh   int    `json:"err_threshold"` // 默认 3
		ErrCooldown string `json:"err_cooldown"`  // "10m"
	} `json:"cooldown"`

	Schedule struct {
		CheckinHours   []int    `json:"checkin_hours,omitempty"` // 旧格式：[9,21]
		CheckinTimes   []string `json:"checkin_times,omitempty"` // 新格式：["09:00","21:30"]
		KeepaliveHours []int    `json:"keepalive_hours"`         // [22]
		// ExpiringThresholdHours 临期阈值（小时）：到期日在此范围内的积分视为「临期」,
		// Pick() 临期优先消耗。默认 24，下限 24（低于 24 会被钳到 24）。
		ExpiringThresholdHours int `json:"expiring_threshold_hours,omitempty"`
	} `json:"schedule"`

	Upstream struct {
		TimeoutSeconds int `json:"timeout_seconds"` // 默认 120
	} `json:"upstream"`

	// Compat 三接口兼容层（OpenAI Responses / Anthropic Messages）配置。
	// 零值即关闭模型名映射，仅接受 "channel/model" 形式。
	Compat struct {
		// DefaultChannel 无前缀模型名的傅底渠道，如 "workbuddy"。
		DefaultChannel string `json:"default_channel"`
		// ModelMap 裸模型名 → "channel/model"。key 以 * 结尾时按前缀通配匹配。
		ModelMap map[string]string `json:"model_map"`
		// MaxTokensCap 转发上游前对 max_tokens 封顶（0 = 不限制）。
		// Anthropic 客户端常发 64000，而多数上游上限更低，导致直接 400。
		MaxTokensCap int `json:"max_tokens_cap"`
		// ReasoningEffort 思考强度默认档：客户端未表达思考意图时注入的兜底值。
		// 取值 none/minimal/low/medium/high/xhigh/max/ultra；空串或 off/none 表示不注入。
		// 对 WorkBuddy 国内版/国际版与 Qoder 生效（两者档位表独立，同名模型 ladder 可能不同）；
		// TraeWork 协议没有可验证的档位字段。档位按模型能力就近降级，客户端显式指定时始终以客户端为准。
		ReasoningEffort string `json:"reasoning_effort"`
		// ResponsesReasoningSummary 是否把上游思考链转成 Responses 的 reasoning item。
		// 取值 auto（默认）/ on / off：
		//   - auto：仅当客户端显式索要摘要（reasoning.summary 非 none，或 include 含
		//     reasoning.encrypted_content）时才下发，避免给不关心思考的客户端多发事件
		//   - on  ：只要上游给了 reasoning_content 就下发
		//   - off ：从不下发（保持旧行为，丢弃思考链）
		ResponsesReasoningSummary string `json:"responses_reasoning_summary"`
		// DeepseekThinking WorkBuddy（CodeBuddy）上游 DeepSeek 系的思考改写开关。
		// true / 未设置（默认）：客户端要开思考时，除 reasoning_effort 外同时下发
		// thinking:{type:"enabled"}，并给 assistant 消息回填 reasoning_content
		// （对齐官方客户端行为，见 internal/upstream/thinking.go）；
		// false：完全不碰这两个字段（回退旧行为，排障用）。
		DeepseekThinking *bool `json:"deepseek_thinking"`
		// StaticEffortFallback 思考档位静态兜底表开关。
		// true / 未设置（默认）：上游目录未下发档位能力的模型，用内置静态表补齐
		// 并参与就近降级（见 internal/reasoning/catalog.go）；
		// false：只认上游目录下发值，未下发的模型不降级（实测对比用）。
		StaticEffortFallback *bool `json:"static_effort_fallback"`
		// ContextWindows 逐模型的上下文窗口档位，key 为 "渠道/模型"（与 /v1/models
		// 的 id 同格式，如 "qoder/qwen3.8-flash"），值为选定的 token 数。
		// 请求时按该值从上游允许的档位里取不超过它的最高档
		// （如选 1000000，支持 1M 的模型发 1M，只到 200K 的模型发 200K）；
		// 未列出的模型用上游模型目录里标了 is_default 的那档。
		// 目前仅 Qoder 生效：其它渠道的请求体是 OpenAI 透传，没有可指定窗口的字段。
		ContextWindows map[string]int64 `json:"context_windows"`
	} `json:"compat"`

	// Proxies 单渠道上游代理：key 为渠道 kind（workbuddy/traework/qoder/qodercn/
	// qodercom/workbuddyai/qwenwork/oczen），value 为代理 URL（http/https/socks5）。
	// 空串或未列出的渠道直连。主要用途：给 oczen 等有区域限制的渠道走代理。
	Proxies map[string]string `json:"proxies,omitempty"`

	// OczenAPIKey OpenCodeZen 渠道自定义 API key（sk-...）；空 = 匿名凭证（public）。
	// 自定义 key 有独立配额（不受匿名通道共享限流），且可调用付费模型（需账户余额）。
	OczenAPIKey string `json:"oczen_api_key,omitempty"`

	// 解析后
	HardCreditDur  time.Duration `json:"-"`
	SoftRateDur    time.Duration `json:"-"`
	ErrCooldownDur time.Duration `json:"-"`
	// ExpiringThresholdDur 临期阈值解析结果（normalize 里钳到 >= 24h）。
	ExpiringThresholdDur time.Duration `json:"-"`
}

// DeepseekThinkingEnabled DeepSeek 思考改写是否启用（字段未设置视为启用）。
func (c *Config) DeepseekThinkingEnabled() bool {
	return c.Compat.DeepseekThinking == nil || *c.Compat.DeepseekThinking
}

// StaticEffortFallbackEnabled 档位静态兜底表是否启用（字段未设置视为启用）。
func (c *Config) StaticEffortFallbackEnabled() bool {
	return c.Compat.StaticEffortFallback == nil || *c.Compat.StaticEffortFallback
}

// Default 默认配置。
func Default() *Config {
	c := &Config{
		Listen:    Listen{Host: "127.0.0.1", Port: 7863},
		APIKey:    "WildWorkAPI",
		AuthDir:   "./auths",
		StateFile: "./data/state.json",
		Region:    "cn",
	}
	c.Cooldown.HardCredit = "12h"
	c.Cooldown.SoftRate = "60s"
	c.Cooldown.ErrThresh = 3
	c.Cooldown.ErrCooldown = "10m"
	c.Schedule.CheckinHours = []int{9, 21}
	c.Schedule.CheckinTimes = []string{"09:00", "21:00"}
	c.Schedule.KeepaliveHours = []int{22}
	c.Schedule.ExpiringThresholdHours = 24
	c.Upstream.TimeoutSeconds = 120
	c.Compat.DefaultChannel = "workbuddy"
	c.Compat.MaxTokensCap = 32000
	c.Proxies = map[string]string{}
	return c
}

// Load 从文件读，再用 WILDWORK_* env 覆盖。
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := json.Unmarshal(raw, c); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
		// Default() 带有新字段默认值；老配置没有 checkin_times 时必须让旧的
		// checkin_hours 生效，而不能被 Default 的 [09:00,21:00] 覆盖。
		var shape struct {
			Schedule map[string]json.RawMessage `json:"schedule"`
		}
		if json.Unmarshal(raw, &shape) == nil {
			if _, ok := shape.Schedule["checkin_times"]; !ok {
				c.Schedule.CheckinTimes = nil
			}
		}
	}
	applyEnv(c)
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save 以 0600 原子写回配置。
func Save(c *Config, path string) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ParseClockTimes 将 HH:MM 列表转换为当天分钟数（0..1439）。
func ParseClockTimes(values []string) ([]int, error) {
	seen := map[int]bool{}
	out := make([]int, 0, len(values))
	for _, v := range values {
		parts := strings.Split(strings.TrimSpace(v), ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid time %q", v)
		}
		h, errH := strconv.Atoi(parts[0])
		m, errM := strconv.Atoi(parts[1])
		if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return nil, fmt.Errorf("invalid time %q", v)
		}
		minute := h*60 + m
		if !seen[minute] {
			seen[minute] = true
			out = append(out, minute)
		}
	}
	sort.Ints(out)
	return out, nil
}

// FormatClockTimes 将当天分钟数格式化为排序后的 HH:MM 列表。
func FormatClockTimes(minutes []int) []string {
	out := make([]int, 0, len(minutes))
	seen := map[int]bool{}
	for _, m := range minutes {
		if m >= 0 && m < 24*60 && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Ints(out)
	formatted := make([]string, 0, len(out))
	for _, m := range out {
		formatted = append(formatted, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	return formatted
}

func applyEnv(c *Config) {
	if v := os.Getenv("WILDWORK_LISTEN"); v != "" {
		if l, err := ParseListen(v); err == nil {
			c.Listen = l
		}
	}
	if v := os.Getenv("WILDWORK_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("WILDWORK_AUTH_DIR"); v != "" {
		c.AuthDir = v
	}
	if v := os.Getenv("WILDWORK_STATE_FILE"); v != "" {
		c.StateFile = v
	}
	if v := os.Getenv("WILDWORK_REGION"); v != "" {
		c.Region = v
	}
	if v := os.Getenv("WILDWORK_HARD_CREDIT"); v != "" {
		c.Cooldown.HardCredit = v
	}
	if v := os.Getenv("WILDWORK_SOFT_RATE"); v != "" {
		c.Cooldown.SoftRate = v
	}
	if v := os.Getenv("WILDWORK_ERR_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Cooldown.ErrThresh = n
		}
	}
	if v := os.Getenv("WILDWORK_ERR_COOLDOWN"); v != "" {
		c.Cooldown.ErrCooldown = v
	}
	if v := os.Getenv("WILDWORK_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Upstream.TimeoutSeconds = n
		}
	}
	if v := os.Getenv("WILDWORK_DEFAULT_CHANNEL"); v != "" {
		c.Compat.DefaultChannel = v
	}
	if v := os.Getenv("WILDWORK_MAX_TOKENS_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Compat.MaxTokensCap = n
		}
	}
	if v := os.Getenv("WILDWORK_REASONING_EFFORT"); v != "" {
		c.Compat.ReasoningEffort = v
	}
	if v := os.Getenv("WILDWORK_RESPONSES_REASONING_SUMMARY"); v != "" {
		c.Compat.ResponsesReasoningSummary = v
	}
	if v := os.Getenv("WILDWORK_DEEPSEEK_THINKING"); v != "" {
		if b, ok := parseBoolLoose(v); ok {
			c.Compat.DeepseekThinking = &b
		}
	}
	if v := os.Getenv("WILDWORK_STATIC_EFFORT_FALLBACK"); v != "" {
		if b, ok := parseBoolLoose(v); ok {
			c.Compat.StaticEffortFallback = &b
		}
	}
	// 逐模型上下文档位："渠道/模型=token 数" 逗号分隔
	if v := os.Getenv("WILDWORK_CONTEXT_WINDOWS"); v != "" {
		if m := parseContextWindows(v); len(m) > 0 {
			c.Compat.ContextWindows = m
		}
	}
}

// parseContextWindows 解析 "channel/model=123,channel/model2=456" 形式的环境变量。
// 非法片段（缺 =、缺 /、非数字）整条跳过，不阻塞启动。
func parseContextWindows(v string) map[string]int64 {
	out := map[string]int64{}
	for _, part := range strings.Split(v, ",") {
		key, val, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		if !ok || strings.Index(key, "/") <= 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		out[key] = n
	}
	return out
}

// parseBoolLoose 宽松布尔解析（环境变量用）：1/true/yes/on 与 0/false/no/off。
// 无法识别时返回 ok=false，调用方保持原值（不因拼错而静默改语义）。
func parseBoolLoose(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true, true
	case "0", "false", "no", "off", "disable", "disabled":
		return false, true
	}
	return false, false
}

func (c *Config) normalize() error {
	var err error
	if c.HardCreditDur, err = time.ParseDuration(c.Cooldown.HardCredit); err != nil {
		return fmt.Errorf("cooldown.hard_credit: %w", err)
	}
	if c.SoftRateDur, err = time.ParseDuration(c.Cooldown.SoftRate); err != nil {
		return fmt.Errorf("cooldown.soft_rate: %w", err)
	}
	if c.ErrCooldownDur, err = time.ParseDuration(c.Cooldown.ErrCooldown); err != nil {
		return fmt.Errorf("cooldown.err_cooldown: %w", err)
	}
	if c.Cooldown.ErrThresh <= 0 {
		c.Cooldown.ErrThresh = 3
	}
	if c.Upstream.TimeoutSeconds <= 0 {
		c.Upstream.TimeoutSeconds = 120
	}
	// 临期阈值：默认 24h，下限 24h（日期粒度的到期判定低于一天没有意义）。
	if c.Schedule.ExpiringThresholdHours <= 0 {
		c.Schedule.ExpiringThresholdHours = 24
	}
	if c.Schedule.ExpiringThresholdHours < 24 {
		c.Schedule.ExpiringThresholdHours = 24
	}
	c.ExpiringThresholdDur = time.Duration(c.Schedule.ExpiringThresholdHours) * time.Hour
	if c.Compat.MaxTokensCap < 0 {
		c.Compat.MaxTokensCap = 0 // 负数视为「不限制」，避免误用导致 max_tokens 被置 0
	}
	// 逐模型上下文档位：丢掉非法项（key 非 channel/model、值非正），
	// 保留 nil 与空 map 的区别交给消费方，这里只做清洗。
	for k, v := range c.Compat.ContextWindows {
		if strings.Index(k, "/") <= 0 || v <= 0 {
			delete(c.Compat.ContextWindows, k)
		}
	}
	// 思考强度默认档：归一化为标准档位（"" 表示不注入）；非法取值在加载阶段就报错
	effort, err := reasoning.ParseDefault(c.Compat.ReasoningEffort)
	if err != nil {
		return fmt.Errorf("compat.reasoning_effort: %w", err)
	}
	c.Compat.ReasoningEffort = effort
	// Responses 思考摘要下发策略：空串按 auto 归一，非法取值在加载阶段报错
	mode, err := reasoning.ParseSummaryMode(c.Compat.ResponsesReasoningSummary)
	if err != nil {
		return err
	}
	c.Compat.ResponsesReasoningSummary = mode
	if c.Listen.Port <= 0 {
		c.Listen.Port = 7863
	}
	// 代理配置清洗：剔除空值，校验 URL 形态（http/https/socks5）。
	for k, v := range c.Proxies {
		v = strings.TrimSpace(v)
		if v == "" {
			delete(c.Proxies, k)
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("proxies[%s]: 无效代理地址 %q（应为 http://host:port 或 socks5://host:port）", k, v)
		}
		switch u.Scheme {
		case "http", "https", "socks5":
		default:
			return fmt.Errorf("proxies[%s]: 不支持的代理协议 %q（仅 http/https/socks5）", k, u.Scheme)
		}
		c.Proxies[k] = v
	}
	if c.Region == "" {
		c.Region = "cn"
	}
	c.Region = strings.ToLower(c.Region)
	if c.Region != "cn" && c.Region != "global" {
		return fmt.Errorf("region must be cn or global, got %q", c.Region)
	}
	// 兼容旧版 checkin_hours；新版本统一规范化为 HH:MM。
	if len(c.Schedule.CheckinTimes) == 0 {
		c.Schedule.CheckinTimes = make([]string, 0, len(c.Schedule.CheckinHours))
		for _, h := range c.Schedule.CheckinHours {
			if h < 0 || h > 23 {
				return fmt.Errorf("schedule.checkin_hours: hour out of range: %d", h)
			}
			c.Schedule.CheckinTimes = append(c.Schedule.CheckinTimes, fmt.Sprintf("%02d:00", h))
		}
	}
	mins, err := ParseClockTimes(c.Schedule.CheckinTimes)
	if err != nil {
		return fmt.Errorf("schedule.checkin_times: %w", err)
	}
	c.Schedule.CheckinTimes = FormatClockTimes(mins)
	if len(c.Schedule.CheckinHours) == 0 {
		for _, m := range mins {
			if m%60 == 0 {
				c.Schedule.CheckinHours = append(c.Schedule.CheckinHours, m/60)
			}
		}
	}
	return nil
}
