// payload.go 改写发往上游的 chat 请求体：
//  1. 强制 stream:true（上游拒绝非流式）
//  2. tool_choice 归一化（上游该字段是 string，对象形式会 400 code=11101）
package upstream

import (
	"encoding/json"
	"log"
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
	// 输出上限字段收敛（吸收 PR #116，Closes #117）：上游只认正整数 max_tokens，
	// 别名透传要么被忽略后回落默认上限（长流被截），要么被上游按参数校验拒绝
	//（实测 11133 param=max_output_tokens）。见 NormalizeOutputLimits。
	NormalizeOutputLimits(obj)
	// stream_options 仅当 body 未显式带时补 {include_usage: true}（D7）：
	// 官方 CLI 流式必发该字段，上游据此在末帧返回 usage 用量；显式带则不覆盖。
	if _, has := obj["stream_options"]; !has {
		obj["stream_options"] = map[string]any{"include_usage": true}
	}
	normalizeToolChoice(obj)
	normalizeRoles(obj) // developer → system（上游对 developer 角色触发内容过滤误杀）
	ProjectReasoning(obj, reasoning.RealmCN)

	// tool 配对两步（见 tool_pairing.go）：先重排再清理。所有模型一律执行（独立于
	// deepseek-only 的 sanitize 开关）。这是「让请求通过」的安全网——不完整配对的
	// tool_calls/tool 结果会让上游对之后每条消息都返 400，必须先行剔除；
	// 插在结果中间的非 tool 消息（Codex image_resize_notice）同样判配对断裂，
	// 先 repack 挪后，再 cleanup 删孤儿，两侧同口径。
	if msgs, ok := obj["messages"].([]any); ok {
		msgs, _ = repackToolResultBlocks(msgs)
		msgs, _ = cleanupOrphanToolCalls(msgs)
		// 无改动时两步都返回原 slice，这里回写等于零操作；任一步重排/删除
		// （哪怕后续步骤零改动）也必须落到 obj——不能只在「最后一步改动」时回写，
		// 否则 repack 单独生效的结果会被原 slice 覆盖丢失。
		obj["messages"] = msgs
	}
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

// NormalizeOutputLimits 把出站请求的输出上限收敛为「至多一个正整数 max_tokens」，
// 并删除全部别名（max_completion_tokens / max_output_tokens）。调用点在预处理管线
// 的 stream 强制之后（同一管线挂载，任务书 prompt-too-long §3）。
//
// OpenAI 规范里 max_tokens 已 deprecated：新客户端用 max_completion_tokens
// （o-series 起），走 Responses 协议的客户端用 max_output_tokens，且常以
// null 表示「未设置」。WorkBuddy 上游（CN /v2 与 global /console 同源）只认
// max_tokens——别名透传会被忽略后回落默认输出上限（实测 32000），长流任务被截；
// 显式带 null/0 的形态更会被上游的整数下限校验直接拒掉（11133
// param=max_output_tokens），故这类形态一律删除而不是原样转发。
//
//   - max_tokens 为正整数 → 保留（显式优先，别名只删不译，整数去整后回写，
//     避免 1.28e5 科学计数法/小数尾巴进上游 body）；
//   - max_tokens 缺失或非正（0/null/负数/非整数/非数值）→ 删除该键，改由别名顶替；
//   - 别名按 max_completion_tokens → max_output_tokens 顺序取首个正整数值；
//   - 三者都没有正整数值 → 不写 max_tokens，走上游默认。
//
// 两域同口径：CN /v2 与 global /console 是同一套 API 的两次部署，翻译不分 realm。
func NormalizeOutputLimits(obj map[string]any) {
	var alias int64
	hasAlias := false
	for _, k := range []string{"max_completion_tokens", "max_output_tokens"} {
		v, has := obj[k]
		delete(obj, k) // 无论翻译与否，别名一律删（上游 Go struct 未知字段宽松，留着徒增噪音）
		if !has || hasAlias {
			continue
		}
		if n, ok := positiveInt64(v); ok {
			alias, hasAlias = n, true
		}
	}
	if n, ok := positiveInt64(obj["max_tokens"]); ok {
		obj["max_tokens"] = n
		return
	}
	delete(obj, "max_tokens")
	if hasAlias {
		obj["max_tokens"] = alias
	}
}

// positiveInt64 取正整数值。json.Unmarshal 的数字是 float64，需去整；int 家族为
// 手构造 map 的调用方防御性兼容。非正/非整/非数值一律视为「没有」。
func positiveInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 && n == float64(int64(n)) {
			return int64(n), true
		}
	case int64:
		if n > 0 {
			return n, true
		}
	case int:
		if n > 0 {
			return int64(n), true
		}
	}
	return 0, false
}

// normalizeRoles 把 messages 里的 developer 角色归一为 system。
//
// 背景：上游对 messages 的 role 字段做白名单校验，developer 不在白名单内，
// 命中即 HTTP 400 code=11128。developer 是 OpenAI 新规范里 system 的别名
// （Codex / Cursor 等新客户端用它承载 system 级指令），改写为 system 不丢语义。
//
// 此归一化是「协议兼容」（补上游 role 白名单），不是「内容脱敏」，
// 因此有意与 SanitizeFingerprints / sanitize 参数解耦：即使 sanitize=false 也照常归一。
//
// 只认 developer 这一个值：其余 role（system/user/assistant/tool/任意未知值）一律原样保留，
// 不合并、不重排、不删除任何消息（上游对多 system 的行为尚未实测，合并会引入新变量）。
func normalizeRoles(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for i, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, ok := msg["role"].(string)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(role), "developer") {
			msg["role"] = "system"
			log.Printf("[upstream] role normalized developer->system idx=%d", i)
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
