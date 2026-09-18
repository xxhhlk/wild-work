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
	ProjectReasoning(obj)
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

// reasoningDialectModels 上游认 low/high/max 三档方言的模型。
// 其余模型按标准档位（none/minimal/low/medium/high/xhigh/max/ultra）原样透传。
var reasoningDialectModels = map[string]bool{
	"deepseek-v4-pro":   true,
	"deepseek-v4-flash": true,
}

// ProjectReasoning 把归一化后的思考控制投影成上游能识别的形态：
//   - low/high/max 方言模型（reasoningDialectModels）：标准档位压到三档；
//   - 其他模型：标准档位原样透传；
//   - 未表达 / 关闭：不传该字段（上游用「无字段」表示不启用思考）。
//
// 调用方须保证入参已经过 internal/reasoning 归一化（internal/server 的
// prepareChatBody 负责）；这里只做投影，不重复做兼容字段解析。
func ProjectReasoning(obj map[string]any) {
	control, err := reasoning.Resolve(obj, false)
	if err != nil {
		return // 非法控制已在 server 层拦下；此处兜底，不因解析失败破坏请求
	}
	effort := reasoning.ChatEffort(control)
	if model, _ := obj["model"].(string); reasoningDialectModels[model] {
		effort = reasoning.WorkBuddyEffort(control)
	}
	if effort == "" {
		delete(obj, "reasoning_effort")
		return
	}
	obj["reasoning_effort"] = effort
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
