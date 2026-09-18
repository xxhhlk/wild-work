// Package reasoning 归一化客户端协议里的「思考强度」控制。
//
// 移植自 Buddy2api 的 reasoning_controls.py：客户端（Claude Code / Codex / OpenCode /
// Cherry Studio / DSH …）各用不同字段表达思考强度，本包把它们统一成一个四态控制量，
// 再由调用方投影成目标渠道协议支持的形态（见 ChatEffort / WorkBuddyEffort）。
//
// 四态：
//   - default   客户端未表达意图 → 由调用方决定是否注入默认档
//   - disabled  明确关闭
//   - enabled   明确开启但不指定强度
//   - effort    明确指定强度（minimal/low/medium/high/xhigh/max/ultra）
//
// 兼容字段（同一字段出现矛盾取值时返回 InvalidError）：
//
//	reasoning_effort / reasoningEffort / reasoning.effort / reasoning.enabled /
//	reasoning.budget_tokens / thinking.type / thinking.effort /
//	thinking.budget_tokens / output_config.effort / enable_thinking /
//	disable_reasoning / think
//
// 本包只依赖标准库，便于脱离账号池单测。
package reasoning

import (
	"encoding/json"
	"errors"
	"strings"
)

// Mode 思考控制状态。
type Mode string

const (
	// ModeDefault 客户端未表达任何思考意图。
	ModeDefault Mode = "default"
	// ModeDisabled 明确关闭思考。
	ModeDisabled Mode = "disabled"
	// ModeEnabled 明确开启思考，但不指定强度。
	ModeEnabled Mode = "enabled"
	// ModeEffort 明确指定强度。
	ModeEffort Mode = "effort"
)

// Control 归一化后的思考控制。
type Control struct {
	Mode Mode
	// Effort 仅在 Mode == ModeEffort 时有值，取值 minimal/low/medium/high/xhigh/max/ultra。
	Effort string
	// Source 命中的字段名（如 "reasoning_effort" / "thinking.type"），用于日志与冲突提示。
	Source string
	// BudgetTokens 客户端给出的思考预算（thinking.budget_tokens / reasoning.budget_tokens）。
	// 多数渠道协议没有对应字段，保留是为了在 native 对象内传递与后续扩展。
	BudgetTokens *float64
}

// Enabled 返回三态开关：true/false 为明确值，ok=false 表示客户端未表达。
func (c Control) Enabled() (value bool, ok bool) {
	switch c.Mode {
	case ModeDisabled:
		return false, true
	case ModeEnabled, ModeEffort:
		return true, true
	}
	return false, false
}

// IsDefault 判断客户端是否未表达思考意图。
func (c Control) IsDefault() bool { return c.Mode == ModeDefault }

// Default 未表达思考控制时的零值。
var Default = Control{Mode: ModeDefault}

// InvalidError 客户端给出的思考控制非法：取值不在允许集合内，或同一对象内自相矛盾。
// 调用方应转成 HTTP 400（code=invalid_reasoning_control），而不是当上游故障处理。
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

// IsInvalid 判断 err 是否为 InvalidError。
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func invalid(source, message string) error { return &InvalidError{msg: source + " " + message} }

// 各写法的取值集合（与 reasoning_controls.py 保持一致）。
var (
	disabledValues = map[string]bool{
		"none": true, "off": true, "disable": true, "disabled": true, "false": true,
	}
	enabledValues = map[string]bool{
		"auto": true, "enable": true, "enabled": true, "adaptive": true, "on": true, "true": true,
	}
	effortAliases = map[string]string{
		"minimal": "minimal", "low": "low", "medium": "medium", "high": "high",
		"xhigh": "xhigh", "x-high": "xhigh", "extra_high": "xhigh", "extra-high": "xhigh",
		"max": "max", "ultra": "ultra",
	}
)

// number 把 JSON 解出的数值统一成 float64。
// encoding/json 默认给 float64，手写 map（测试、内部构造）可能是 int/int64/json.Number。
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// fromEffort 解析「强度/开关」混合写法：bool、档位字符串、开关别名。
// 返回 nil 表示该字段未参与（缺省或空值）。
func fromEffort(v any, source string) (*Control, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.(bool); ok {
		if b {
			return &Control{Mode: ModeEnabled, Source: source}, nil
		}
		return &Control{Mode: ModeDisabled, Source: source}, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, invalid(source, "must be a string")
	}
	n := strings.ToLower(strings.TrimSpace(s))
	if n == "" {
		return nil, invalid(source, "must not be empty")
	}
	if n == "default" {
		return &Control{Mode: ModeDefault, Source: source}, nil
	}
	if disabledValues[n] {
		return &Control{Mode: ModeDisabled, Source: source}, nil
	}
	if enabledValues[n] {
		return &Control{Mode: ModeEnabled, Source: source}, nil
	}
	effort, ok := effortAliases[n]
	if !ok {
		return nil, invalid(source, "must be one of none, minimal, low, medium, high, xhigh, max, or ultra")
	}
	return &Control{Mode: ModeEffort, Effort: effort, Source: source}, nil
}

// fromSwitch 解析纯开关写法（只接受布尔或开关别名，不接受档位）。
func fromSwitch(v any, source string) (*Control, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.(bool); ok {
		if b {
			return &Control{Mode: ModeEnabled, Source: source}, nil
		}
		return &Control{Mode: ModeDisabled, Source: source}, nil
	}
	if n, ok := number(v); ok && (n == 0 || n == 1) {
		mode := ModeDisabled
		if n == 1 {
			mode = ModeEnabled
		}
		return &Control{Mode: mode, Source: source}, nil
	}
	if s, ok := v.(string); ok {
		n := strings.ToLower(strings.TrimSpace(s))
		if disabledValues[n] {
			return &Control{Mode: ModeDisabled, Source: source}, nil
		}
		if enabledValues[n] {
			return &Control{Mode: ModeEnabled, Source: source}, nil
		}
	}
	return nil, invalid(source, "must be a boolean or an enabled/disabled value")
}

// fromDisableSwitch 解析「反向开关」（disable_reasoning）：false/0/缺省视为未参与。
func fromDisableSwitch(v any, source string) (*Control, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.(bool); ok {
		if !b {
			return nil, nil
		}
		return &Control{Mode: ModeDisabled, Source: source}, nil
	}
	if n, ok := number(v); ok {
		if n == 0 {
			return nil, nil
		}
		if n == 1 {
			return &Control{Mode: ModeDisabled, Source: source}, nil
		}
	}
	return nil, invalid(source, "must be a boolean")
}

// fromBudget 解析思考预算：0 → 关闭，>0 → 开启（带预算）。
func fromBudget(v any, source string) (*Control, error) {
	if v == nil {
		return nil, nil
	}
	if _, isBool := v.(bool); isBool {
		return nil, invalid(source, "must be a non-negative number")
	}
	n, ok := number(v)
	if !ok || n < 0 {
		return nil, invalid(source, "must be a non-negative number")
	}
	budget := n
	mode := ModeEnabled
	if n == 0 {
		mode = ModeDisabled
	}
	return &Control{Mode: mode, Source: source, BudgetTokens: &budget}, nil
}

// appendNestedEffort 从嵌套对象里取 effort 字段。
func appendNestedEffort(dst *[]Control, container any, containerName string) error {
	m, ok := container.(map[string]any)
	if !ok {
		return nil
	}
	v, has := m["effort"]
	if !has {
		return nil
	}
	c, err := fromEffort(v, containerName+".effort")
	if err != nil {
		return err
	}
	if c != nil {
		*dst = append(*dst, *c)
	}
	return nil
}

// nativeControlGroup 判断来源字段属于哪个原生对象（reasoning / thinking），
// 用于「同一对象内的开关与强度必须一致」的冲突检测。
func nativeControlGroup(source string) string {
	if source == "reasoning" || strings.HasPrefix(source, "reasoning.") {
		return "reasoning"
	}
	if source == "thinking" || strings.HasPrefix(source, "thinking.") {
		return "thinking"
	}
	return source
}

// validateNativeObjectConflicts 同一原生对象里同时表达了开启与关闭时判为冲突
// （如 reasoning:{effort:"high",enabled:false}）。跨对象的矛盾不报错，按优先级取一。
func validateNativeObjectConflicts(efforts, switches []Control) error {
	for _, group := range []string{"reasoning", "thinking"} {
		distinct := map[bool]bool{}
		var sources []string
		for _, c := range append(append([]Control{}, efforts...), switches...) {
			if nativeControlGroup(c.Source) != group {
				continue
			}
			v, ok := c.Enabled()
			if !ok {
				continue
			}
			distinct[v] = true
			sources = append(sources, c.Source)
		}
		if len(distinct) > 1 {
			return &InvalidError{msg: "conflicting reasoning controls: " + strings.Join(sources, ", ")}
		}
	}
	return nil
}

// Resolve 从请求体解析出唯一的思考控制。
//
// preferNested 控制「嵌套 reasoning.effort」与「顶层 reasoning_effort」同时出现时的优先级：
// Responses 协议用 true（标准写法是嵌套），Chat 协议用 false。
func Resolve(payload map[string]any, preferNested bool) (Control, error) {
	if payload == nil {
		return Default, nil
	}

	reasoning, hasReasoning := payload["reasoning"]
	thinking, hasThinking := payload["thinking"]
	outputConfig := payload["output_config"]

	var effortCandidates []Control
	addTopLevel := func(key string) error {
		v, has := payload[key]
		if !has {
			return nil
		}
		c, err := fromEffort(v, key)
		if err != nil {
			return err
		}
		if c != nil {
			effortCandidates = append(effortCandidates, *c)
		}
		return nil
	}

	if preferNested {
		if err := appendNestedEffort(&effortCandidates, reasoning, "reasoning"); err != nil {
			return Default, err
		}
		if err := addTopLevel("reasoning_effort"); err != nil {
			return Default, err
		}
	} else {
		if err := addTopLevel("reasoning_effort"); err != nil {
			return Default, err
		}
		if err := appendNestedEffort(&effortCandidates, reasoning, "reasoning"); err != nil {
			return Default, err
		}
	}
	if err := addTopLevel("reasoningEffort"); err != nil {
		return Default, err
	}
	if err := appendNestedEffort(&effortCandidates, outputConfig, "output_config"); err != nil {
		return Default, err
	}
	if err := appendNestedEffort(&effortCandidates, thinking, "thinking"); err != nil {
		return Default, err
	}

	// reasoning / thinking 写成标量（字符串或布尔）时同样当作强度写法
	if hasReasoning {
		if _, isMap := reasoning.(map[string]any); !isMap {
			c, err := fromEffort(reasoning, "reasoning")
			if err != nil {
				return Default, err
			}
			if c != nil {
				effortCandidates = append(effortCandidates, *c)
			}
		}
	}
	if hasThinking {
		if _, isMap := thinking.(map[string]any); !isMap {
			c, err := fromEffort(thinking, "thinking")
			if err != nil {
				return Default, err
			}
			if c != nil {
				effortCandidates = append(effortCandidates, *c)
			}
		}
	}

	var switchCandidates []Control
	if tm, ok := thinking.(map[string]any); ok {
		if v, has := tm["type"]; has {
			s, isStr := v.(string)
			if !isStr {
				return Default, invalid("thinking.type", "must be a string")
			}
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "disabled":
				switchCandidates = append(switchCandidates, Control{Mode: ModeDisabled, Source: "thinking.type"})
			case "enabled", "adaptive":
				switchCandidates = append(switchCandidates, Control{Mode: ModeEnabled, Source: "thinking.type"})
			default:
				return Default, invalid("thinking.type", "must be enabled, adaptive, or disabled")
			}
		}
		if v, has := tm["budget_tokens"]; has {
			c, err := fromBudget(v, "thinking.budget_tokens")
			if err != nil {
				return Default, err
			}
			if c != nil {
				switchCandidates = append(switchCandidates, *c)
			}
		}
	}
	if rm, ok := reasoning.(map[string]any); ok {
		if v, has := rm["enabled"]; has {
			c, err := fromSwitch(v, "reasoning.enabled")
			if err != nil {
				return Default, err
			}
			if c != nil {
				switchCandidates = append(switchCandidates, *c)
			}
		}
		if v, has := rm["budget_tokens"]; has {
			c, err := fromBudget(v, "reasoning.budget_tokens")
			if err != nil {
				return Default, err
			}
			if c != nil {
				switchCandidates = append(switchCandidates, *c)
			}
		}
	}
	for _, key := range []string{"enable_thinking", "think"} {
		v, has := payload[key]
		if !has {
			continue
		}
		c, err := fromSwitch(v, key)
		if err != nil {
			return Default, err
		}
		if c != nil {
			switchCandidates = append(switchCandidates, *c)
		}
	}
	if v, has := payload["disable_reasoning"]; has {
		c, err := fromDisableSwitch(v, "disable_reasoning")
		if err != nil {
			return Default, err
		}
		if c != nil {
			switchCandidates = append(switchCandidates, *c)
		}
	}

	if err := validateNativeObjectConflicts(effortCandidates, switchCandidates); err != nil {
		return Default, err
	}

	// 强度优先于开关：第一个非 default 的强度候选胜出
	for i := range effortCandidates {
		if effortCandidates[i].Mode == ModeDefault {
			continue
		}
		selected := effortCandidates[i]
		group := nativeControlGroup(selected.Source)
		if group == "reasoning" || group == "thinking" {
			if b, ok := budgetInGroup(switchCandidates, group); ok {
				selected.BudgetTokens = b
			}
		}
		return selected, nil
	}

	if len(switchCandidates) > 0 {
		selected := switchCandidates[0]
		group := nativeControlGroup(selected.Source)
		if selected.BudgetTokens == nil {
			if b, ok := budgetInGroup(switchCandidates, group); ok {
				selected.BudgetTokens = b
			}
		}
		return selected, nil
	}
	return Default, nil
}

// budgetInGroup 取同组开关候选里第一个预算值。
func budgetInGroup(candidates []Control, group string) (*float64, bool) {
	for _, c := range candidates {
		if nativeControlGroup(c.Source) == group && c.BudgetTokens != nil {
			return c.BudgetTokens, true
		}
	}
	return nil, false
}

// ChatEffort 投影到标准 Chat Completions 的 reasoning_effort。
// 返回空串表示不下发该字段（default 交给调用方的默认档，disabled 由渠道决定如何表达）。
func ChatEffort(c Control) string {
	switch c.Mode {
	case ModeDefault:
		return ""
	case ModeDisabled:
		return "none"
	case ModeEnabled:
		return "high"
	}
	return c.Effort
}

// WorkBuddyEffort 投影到 WorkBuddy 的 low/high/max 三档方言。
// disabled 返回空串：WorkBuddy 用「不传字段」表达关闭（与 Buddy2api 一致）。
func WorkBuddyEffort(c Control) string {
	switch c.Mode {
	case ModeDefault, ModeDisabled:
		return ""
	case ModeEnabled:
		return "high"
	}
	switch c.Effort {
	case "minimal", "low":
		return "low"
	case "medium", "high":
		return "high"
	case "xhigh", "max", "ultra":
		return "max"
	}
	return c.Effort
}

// ParseDefault 解析配置项里的默认思考档，返回可直接下发的标准档位值。
// 空串 / off / none / default 都表示「不注入默认」（返回空串）；无法识别时报错，
// 便于配置加载阶段暴露问题而不是静默忽略。
func ParseDefault(value string) (string, error) {
	s := strings.TrimSpace(value)
	if s == "" {
		return "", nil
	}
	c, err := fromEffort(s, "compat.reasoning_effort")
	if err != nil {
		return "", err
	}
	if c == nil || c.Mode == ModeDefault || c.Mode == ModeDisabled {
		return "", nil
	}
	if c.Mode == ModeEnabled {
		return "high", nil
	}
	return c.Effort, nil
}

// Reasoning summary 下发策略（配置项 compat.responses_reasoning_summary）。
const (
	// SummaryAuto 仅在客户端显式索要思考摘要时下发（默认）。
	SummaryAuto = "auto"
	// SummaryOn 只要上游给了思考链就下发。
	SummaryOn = "on"
	// SummaryOff 从不下发，丢弃思考链。
	SummaryOff = "off"
)

// ParseSummaryMode 归一化 reasoning summary 下发策略。
// 空串按 auto 处理（默认值），无法识别时报错，便于配置加载阶段暴露问题。
func ParseSummaryMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return SummaryAuto, nil
	case SummaryAuto, "true", "1", "yes":
		return SummaryAuto, nil
	case SummaryOn, "always", "force":
		return SummaryOn, nil
	case SummaryOff, "false", "0", "no", "never":
		return SummaryOff, nil
	default:
		return "", errors.New("compat.responses_reasoning_summary: 取值需为 auto / on / off")
	}
}

// WantsSummary 判断客户端是否显式索要思考摘要（用于 SummaryAuto 策略）。
//
// 识别依据（任一命中即视为索要）：
//   - reasoning.summary 存在且不为 none / off / false（Codex 发 "auto"）
//   - reasoning_summary 顶层写法（已被 NormalizeChat 提升的形态）
//   - include 数组含 reasoning.encrypted_content
//   - thinking 对象存在（Anthropic 风格混用，语义上等价于要思考过程）
func WantsSummary(payload map[string]any) bool {
	if rm, ok := payload["reasoning"].(map[string]any); ok {
		if v, has := rm["summary"]; has && !summaryDisabled(v) {
			return true
		}
	}
	if v, has := payload["reasoning_summary"]; has && !summaryDisabled(v) {
		return true
	}
	if list, ok := payload["include"].([]any); ok {
		for _, item := range list {
			if strings.Contains(strings.ToLower(asStringAny(item)), "reasoning") {
				return true
			}
		}
	}
	if tm, ok := payload["thinking"].(map[string]any); ok && len(tm) > 0 {
		return true
	}
	return false
}

// summaryDisabled 判断 summary 取值是否表示「不要摘要」。
// 字符串按取值判断（none/off/false/disabled/no/空）；布尔按真假；null 视为不要。
func summaryDisabled(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case bool:
		return !t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "", "none", "off", "false", "disabled", "no":
			return true
		}
		return false
	default:
		return false
	}
}

// asStringAny 宽松取字符串（本包内避免依赖 encoding/json 之外的辅助函数）。
func asStringAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// NormalizeChat 返回归一化后的请求体副本（不修改入参），并把兼容写法收敛为
// 顶层 reasoning_effort。同时返回解析出的控制量，避免调用方二次解析。
//
// 处理规则（与 reasoning_controls.py 一致）：
//   - reasoning 对象：摘掉 effort/enabled，summary 提升为顶层 reasoning_summary，
//     其余扩展字段（budget_tokens / exclude / max_tokens …）原样保留；
//   - thinking 对象：摘掉 type/effort，budget_tokens 等扩展字段保留；
//   - output_config 对象：摘掉 effort；
//   - 顶层 reasoningEffort / enable_thinking / disable_reasoning / think 一律移除；
//   - 最终按控制量写回 reasoning_effort（default/无效则移除该字段）。
func NormalizeChat(payload map[string]any, preferNested bool) (map[string]any, Control, error) {
	control, err := Resolve(payload, preferNested)
	if err != nil {
		return nil, Default, err
	}
	body := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		body[k] = v
	}

	if rm, ok := body["reasoning"].(map[string]any); ok {
		remaining := make(map[string]any, len(rm))
		for k, v := range rm {
			remaining[k] = v
		}
		if summary, has := rm["summary"]; has && summary != nil {
			if _, exists := body["reasoning_summary"]; !exists {
				body["reasoning_summary"] = summary
			}
		}
		delete(remaining, "effort")
		delete(remaining, "enabled")
		delete(remaining, "summary")
		if len(remaining) > 0 {
			body["reasoning"] = remaining
		} else {
			delete(body, "reasoning")
		}
	} else if _, has := body["reasoning"]; has {
		delete(body, "reasoning")
	}

	if tm, ok := body["thinking"].(map[string]any); ok {
		remaining := make(map[string]any, len(tm))
		for k, v := range tm {
			remaining[k] = v
		}
		delete(remaining, "type")
		delete(remaining, "effort")
		if len(remaining) > 0 {
			body["thinking"] = remaining
		} else {
			delete(body, "thinking")
		}
	} else if _, has := body["thinking"]; has {
		delete(body, "thinking")
	}

	if oc, ok := body["output_config"].(map[string]any); ok {
		if _, has := oc["effort"]; has {
			remaining := make(map[string]any, len(oc))
			for k, v := range oc {
				if k != "effort" {
					remaining[k] = v
				}
			}
			if len(remaining) > 0 {
				body["output_config"] = remaining
			} else {
				delete(body, "output_config")
			}
		}
	}

	for _, key := range []string{"reasoningEffort", "enable_thinking", "disable_reasoning", "think"} {
		delete(body, key)
	}

	if effort := ChatEffort(control); effort != "" {
		body["reasoning_effort"] = effort
	} else {
		delete(body, "reasoning_effort")
	}
	return body, control, nil
}
