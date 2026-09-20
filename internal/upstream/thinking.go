// thinking.go WorkBuddy（CodeBuddy）上游的 DeepSeek 思维链开关与多轮回填。
//
// 背景（对官方客户端 codebuddy.js 的逆向结论，参考实现 Sliverkiss/workbuddy2api
// 的 internal/upstream/thinking.go）：
//   - 官方客户端对 deepseek 系模型标记 thinkingFormat:"deepseek"，「开思考」=
//     thinking:{type:"enabled"} **+** 某档 reasoning_effort；缺任一半，上游按
//     「不思考」应答（reasoning_content 长度为 0）。
//   - 官方判定式：isThinkingEnabled = !!(reasoning_summary || reasoning_effort || reasoning?.effort)；
//     默认档 = reasoning.defaultEffort ?? "high"。
//   - 多轮一致性：requiresReasoningContentOnAssistantMessages —— 每条 assistant
//     消息都要带 reasoning_content，且必须是 string（null / 数字不算「已有」）。
//
// 与参考实现的差异（有意）：参考实现对 deepseek **无条件**注入 enabled；本仓库保持
// 「客户端未表达思考意图就不开思考」的既有语义，只在客户端确实要开思考时补
// thinking.type 与默认档，避免给不需要思考的请求增加延迟与额度开销。
package upstream

import (
	"strings"
	"sync/atomic"
)

// defaultDeepSeekEffort 该模型未声明默认档时的兜底（官方客户端 configure 无来源时
// 也 fallback 到 high）。
const defaultDeepSeekEffort = "high"

// deepseekThinking 开关：关闭后本文件所有改写都不生效（排障用）。
// 默认开启；由 main.go 按配置 compat.deepseek_thinking 覆盖。
var deepseekThinking = func() *atomic.Bool {
	b := &atomic.Bool{}
	b.Store(true)
	return b
}()

// SetDeepseekThinking 设置 DeepSeek 思考开关（启动时与面板保存时调用）。
func SetDeepseekThinking(on bool) { deepseekThinking.Store(on) }

// DeepseekThinking 当前是否启用 DeepSeek 思考改写。
func DeepseekThinking() bool { return deepseekThinking.Load() }

// isDeepSeekModel 模型名以 deepseek 为前缀（不区分大小写），
// 对齐官方 thinkingFormat:"deepseek" 的判定口径。
func isDeepSeekModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek")
}

// ensureThinkingEnabled 保证出站请求带 thinking:{type:"enabled"}。
// 客户端已显式给出 thinking.type（enabled/adaptive/disabled）时绝不覆盖；
// thinking 存在但不是对象（非法形态）时同样不动，交给上游判断。
func ensureThinkingEnabled(obj map[string]any) {
	if th, ok := obj["thinking"].(map[string]any); ok {
		if typ, _ := th["type"].(string); strings.TrimSpace(typ) != "" {
			return
		}
		th["type"] = "enabled"
		return
	}
	if _, has := obj["thinking"]; has {
		return
	}
	obj["thinking"] = map[string]any{"type": "enabled"}
}

// backfillReasoningContent 多轮一致性：让每条 assistant 消息都带 string 类型的
// reasoning_content 与 reasoning（对齐官方 ReasoningContentBackfillRule 的门控
// thinkingEnabled || hasTrace）。
//
// reasoning_content：
//
//   - 已有 string 值 → 原样保留，不覆盖；
//   - 有 reasoning（部分客户端用该字段回传）且 reasoning_content 非 string → 复制过去；
//   - 两者皆无 → 补空串（第三方客户端不回传推理痕迹时官方也这么做）；
//   - reasoning_content 为 null / 数字等非 string 值 → 视为「没有」（对齐官方
//     typeof != "string" 语义），落复制/补空分支。
//
// reasoning：镜像归一化，保证存在且非空。部分账号/租户对 thinking 形态校验
// len(reasoning) > 0（缺失/null/空串 → 400，空白串放行），而官方 CLI 本就给每条
// assistant 挂上一轮推理文本（itemsToMessages 的 applyPendingReasoning）——
// 「每条 assistant 的 reasoning 非空」是官方出站形态，非 hack：
//
//   - 已是非空 string → 不动；
//   - 缺失/null/空串 → 写 reasoning_content 的值；两者皆空则补单个空格（该字段是
//     透传校验位、非内容消费位，空白串对模型上下文无语义影响）。
func backfillReasoningContent(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	thinkingEnabled := false
	if th, ok := obj["thinking"].(map[string]any); ok {
		if typ, _ := th["type"].(string); strings.EqualFold(strings.TrimSpace(typ), "enabled") {
			thinkingEnabled = true
		}
	}
	hasTrace := false
	for _, mm := range msgs {
		msg, ok := mm.(map[string]any)
		if !ok {
			continue
		}
		if r, ok := msg["reasoning"].(string); ok && r != "" {
			hasTrace = true
			break
		}
		if _, ok := msg["reasoning_content"]; ok {
			hasTrace = true
			break
		}
	}
	if !thinkingEnabled && !hasTrace {
		return
	}
	for _, mm := range msgs {
		msg, ok := mm.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		rc, hasRC := msg["reasoning_content"].(string)
		if !hasRC {
			if r, ok := msg["reasoning"].(string); ok {
				rc = r
			} else {
				rc = ""
			}
			msg["reasoning_content"] = rc
		}
		if r, ok := msg["reasoning"].(string); ok && r != "" {
			continue // 已非空 → 不覆盖
		}
		if rc != "" {
			msg["reasoning"] = rc
		} else {
			msg["reasoning"] = " "
		}
	}
}
