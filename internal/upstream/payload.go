// payload.go 改写发往上游的 chat 请求体：
//  1. 强制 stream:true（上游拒绝非流式）
//  2. tool_choice 归一化（上游该字段是 string，对象形式会 400 code=11101）
package upstream

import (
	"encoding/json"
	"strings"

	"wild-work/internal/reasoning"
	"wild-work/internal/sanitize"
)

// PrepareBody 单 pass 改写；无法解析时原样返回。
func PrepareBody(src []byte) []byte {
	return sanitize.Messages(prepareBodyInner(src))
}

// prepareBodyInner 同 PrepareBody 但不含脱敏（供内部调用）。
func prepareBodyInner(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	translateMaxCompletionTokens(obj)
	if _, has := obj["stream_options"]; !has {
		obj["stream_options"] = map[string]any{"include_usage": true}
	}
	normalizeToolChoice(obj)
	normalizeRoles(obj) // developer → system（上游对 developer 角色触发内容过滤误杀）
	ProjectReasoning(obj, reasoning.RealmCN)
	// 出站脱敏（全改写完成后、Marshal 前）：剥离上游内容审核黑名单指纹
	// （Claude Code / Codex CLI 注入的模板句、billing header、11128 等，见 sanitize.go）。
	if msgs, ok := obj["messages"].([]any); ok {
		sanitizeMessages(msgs)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// translateMaxCompletionTokens OpenAI 别名 max_completion_tokens → max_tokens。
// 显式 max_tokens 优先（别名只删不译）；非正数值不翻译；翻译后删别名。
func translateMaxCompletionTokens(obj map[string]any) {
	alias, has := obj["max_completion_tokens"]
	delete(obj, "max_completion_tokens")
	if !has {
		return
	}
	if _, explicit := obj["max_tokens"]; explicit {
		return
	}
	switch v := alias.(type) {
	case float64:
		if v > 0 && v == float64(int64(v)) {
			obj["max_tokens"] = int64(v)
		}
	case int64:
		if v > 0 {
			obj["max_tokens"] = v
		}
	case int:
		if v > 0 {
			obj["max_tokens"] = int64(v)
		}
	}
}

// normalizeRoles 将 OpenAI 的 developer 角色改写为 system。
// 上游对 role=developer 的消息一律命中内容过滤（finish_reason=content_filter，
// 返回“检测到敏感内容”），而相同内容用 system 角色则完全正常。
func normalizeRoles(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if r, _ := mm["role"].(string); r == "developer" {
			mm["role"] = "system"
		}
	}
}

// ProjectReasoning 把归一化后的思考控制投影成上游能识别的形态：
//  1. 客户端只说「开思考」没给档位时，补该模型声明的默认档
//     （reasoning.Caps.DefaultEffort），而不是硬编码 high；
//  2. 按模型能力就近降级（reasoning.Caps.Clamp）：上游只认特定档位的模型
//     （如国内版 deepseek-v4-pro 只认 low/high/xhigh、国际版 deepseek-v4.1-flash
//     只认 high）不会再被塞进非法档位；
//  3. DeepSeek 系补 thinking:{type:"enabled"} 并回填 assistant 消息的
//     reasoning_content（见 thinking.go：缺这个开关上游按「不思考」应答）；
//  4. 未表达 / 关闭：删掉 reasoning_effort（上游用「无字段」表示不启用思考），
//     DeepSeek 系同时删 thinking。
//
// realm 区分国内版/国际版（同一模型两面档位可能不同，绝不混用）。
// 调用方须保证入参已经过 internal/reasoning 归一化（internal/server 的
// prepareChatBody 负责）；这里只做投影，不重复做兼容字段解析。
func ProjectReasoning(obj map[string]any, realm string) {
	control, err := reasoning.Resolve(obj, false)
	if err != nil {
		return // 非法控制已在 server 层拦下；此处兜底，不因解析失败破坏请求
	}
	model, _ := obj["model"].(string)
	effort := reasoning.ChatEffort(control)
	if control.Mode == reasoning.ModeEnabled && control.BudgetTokens == nil {
		if d := reasoning.Caps.DefaultEffort(realm, model); d != "" {
			effort = d
		}
	}
	if effort != "" && effort != "none" {
		effort = reasoning.Caps.Clamp(realm, model, effort)
	}
	deepseek := DeepseekThinking() && isDeepSeekModel(model)
	if effort == "" || effort == "none" {
		delete(obj, "reasoning_effort")
		delete(obj, "reasoningEffort")
		if deepseek {
			delete(obj, "thinking")
		}
		return
	}
	obj["reasoning_effort"] = effort
	delete(obj, "reasoningEffort")
	if deepseek {
		ensureThinkingEnabled(obj)
		backfillReasoningContent(obj)
	}
}

// normalizeToolChoice 按上游 Go struct（string 类型）改写 OpenAI tool_choice。
//   - "none"            → 删 tool_choice + 删 tools/functions
//   - {"type":"none"}   → 同上
//   - {"type":"auto"/"required"} → 字符串 "auto"/"required"
//   - {"type":"function","function":{"name":"x"}} → 字符串 "x"
//   - 其他对象/非标量 → 删 tool_choice
func normalizeToolChoice(obj map[string]any) {
	suppress := func() {
		delete(obj, "tools")
		delete(obj, "functions")
	}
	tc, present := obj["tool_choice"]
	if !present {
		return
	}
	switch v := tc.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			delete(obj, "tool_choice")
			suppress()
		}
	case map[string]any:
		typ, _ := v["type"].(string)
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "none":
			delete(obj, "tool_choice")
			suppress()
		case "auto", "required":
			obj["tool_choice"] = typ
		case "function":
			name := ""
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
			if name == "" {
				name, _ = v["name"].(string)
			}
			if name = strings.TrimSpace(name); name != "" {
				obj["tool_choice"] = name
			} else {
				obj["tool_choice"] = "auto"
			}
		default:
			delete(obj, "tool_choice")
		}
	default:
		delete(obj, "tool_choice")
	}
}
