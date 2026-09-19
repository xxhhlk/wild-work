// body.go 构造 agent_chat_generation 请求体（纯透传模式）。
// 移植自 qoderwork2api internal/upstream/body.go：客户端消息全量转发。
//
// 字段形状以 2026-09-20 抓到的 Qoder CN 桌面版**真实线上请求体**为准
// （_spy/http-bodies/*.json，7 个样本），并与 worker 源码 A6e()/GJc()/jJc() 交叉印证。
// 关键差异（相对早期实现）：顶层 system 数组、task_id/source/version/is_retry、
// session_type=app、aliyun_user_type 空串、完整 model_config、parameters 恒下发。
package qoder

import (
	"encoding/json"
	"time"
)

// clientVersion 桌面客户端版本。同时出现在请求头 cosy-version 与
// business.version —— 两处必须一致，否则上游看到自相矛盾的客户端身份。
const clientVersion = "1.1.57"

// model_config 的默认值。实测上游模型目录里 14 个模型的 format/source 全为
// openai/system，max_input_tokens 多为 180000，max_output_tokens 全为 32000。
const (
	defaultModelFormat     = "openai"
	defaultModelSource     = "system"
	defaultMaxInputTokens  = 180000
	defaultMaxOutputTokens = 32000
)

// reasoningSpec Qoder 请求里的思考控制。
//
// 官方桌面版 SDK（@qoder-ai/qoder-cn-agent-sdk）就是这样写的：档位与开关
// **同时**落在 model_config 与 parameters 两处，且 parameters.enable_thinking
// 必须与 model_config.is_reasoning 同源，否则上游会收到自相矛盾的组合
// （is_reasoning=true + enable_thinking=false）。
type reasoningSpec struct {
	// Enabled 投影到 model_config.is_reasoning。
	Enabled bool
	// Effort 投影到 parameters.reasoning_effort；空串表示不下发档位字段
	// （客户端没给档位、或该模型没有可用的 ladder）。
	Effort string
}

// modelMeta 上游模型元数据：model_config 与 parameters 的取值来源。
// 由 FetchModels 从上游模型目录填充；零值字段在构造请求体时回落默认值。
type modelMeta struct {
	Key                  string // 上游 model key（如 qfmodel）
	DisplayName          string // 目录里的 display_name（如 Qwen3.8-Flash）
	IsVL                 bool   // is_vl
	MaxInputTokens       int64  // max_input_tokens
	MaxOutputTokens      int64  // max_output_tokens（→ parameters.max_tokens）
	DefaultContextWindow int64  // 默认上下文窗口（→ parameters.context_length，>0 才下发）
}

// buildAgentBody 构造请求体（无模型元数据时的兼容入口，元数据按默认值补）。
func buildAgentBody(messages []map[string]any, modelKey string, tools []any, spec reasoningSpec) ([]byte, error) {
	return buildAgentBodyMeta(messages, modelMeta{Key: modelKey}, tools, spec)
}

// buildAgentBodyMeta 构造请求体。
//   - messages：客户端原始消息列表（可含 system/assistant/tool 多轮）
//   - meta：上游模型元数据（model_config 字段来源）
//   - tools：客户端传来的 OpenAI tools 数组；nil/空也会下发空数组
//   - spec：思考控制（开关 + 档位），见 reasoningSpec
//
// 思考字段按官方 A6e() 的写法投影：
//
//	model_config.is_reasoning            = spec.Enabled
//	parameters.reasoning_effort          = spec.Effort     （仅非空时写）
//	parameters.enable_thinking           = spec.Enabled    （仅非空时写）
//	parameters.max_tokens                = meta.MaxOutputTokens（恒下发）
//	parameters.context_length            = meta.DefaultContextWindow（已知才写）
//
// 注意 1：developer 角色必须改写为 system。
// 注意 2：桌面端把 system 文本块**同时**放在顶层 system 与 messages[0]
// （A6e() 里 `E.unshift({role:"system", content:d})`），这里保持一致。
func buildAgentBodyMeta(messages []map[string]any, meta modelMeta, tools []any, spec reasoningSpec) ([]byte, error) {
	// developer → system（浅拷贝消息避免污染调用方数据）
	msgs := make([]map[string]any, len(messages))
	for i, m := range messages {
		cp := make(map[string]any, len(m)+1)
		for k, v := range m {
			cp[k] = v
		}
		if r, _ := cp["role"].(string); r == "developer" {
			cp["role"] = "system"
		}
		msgs[i] = cp
	}

	// 最后一条 user 消息文本（chat_context.text 上游协议要求必填）
	prompt := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "user" {
			if c, ok := msgs[i]["content"].(string); ok && c != "" {
				prompt = c
				break
			}
		}
	}

	displayName := meta.DisplayName
	if displayName == "" {
		displayName = meta.Key
	}
	maxInput := meta.MaxInputTokens
	if maxInput <= 0 {
		maxInput = defaultMaxInputTokens
	}
	maxOutput := meta.MaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputTokens
	}
	if tools == nil {
		tools = []any{}
	}

	now := time.Now()
	requestID := uuid4()
	requestSetID := uuid4()

	// parameters 恒下发（A6e() 里 h 始终非空，至少带 max_tokens）。
	params := map[string]any{"max_tokens": maxOutput}
	if spec.Effort != "" {
		params["reasoning_effort"] = spec.Effort
		params["enable_thinking"] = spec.Enabled
	}
	if meta.DefaultContextWindow > 0 {
		params["context_length"] = meta.DefaultContextWindow
	}

	base := map[string]any{
		"request_id":       requestID,
		"request_set_id":   requestSetID,
		"chat_record_id":   requestID,
		"session_id":       uuid4(),
		"stream":           true,
		"chat_task":        "FREE_INPUT",
		"is_reply":         true,
		"is_retry":         false,
		"source":           1,
		"version":          "3",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"session_type":     "app",
		"aliyun_user_type": "",
		"chat_context": map[string]any{
			// text/originalContent 都是**字符串**（桌面端实测），不是 text 块对象。
			"text":     prompt,
			"features": []any{},
			"extra": map[string]any{
				"context":         []any{},
				"modelConfig":     map[string]any{"key": meta.Key, "is_reasoning": spec.Enabled},
				"originalContent": prompt,
			},
			"chatPrompt": "",
			"imageUrls":  nil,
		},
		"model_config": map[string]any{
			"key":              meta.Key,
			"display_name":     displayName,
			"model":            "",
			"format":           defaultModelFormat,
			"is_vl":            meta.IsVL,
			"is_reasoning":     spec.Enabled,
			"api_key":          "",
			"url":              "",
			"source":           defaultModelSource,
			"max_input_tokens": maxInput,
		},
		"system":     systemBlocks(msgs),
		"messages":   msgs,
		"tools":      tools,
		"parameters": params,
		"business": map[string]any{
			"product":  "app",
			"version":  clientVersion,
			"type":     "agent",
			"id":       requestSetID,
			"name":     truncateRunes(prompt, 30),
			"begin_at": now.UnixMilli(),
			"stage":    "start",
		},
	}

	return json.Marshal(base)
}

// systemBlocks 抽出所有 system 消息的文本块，供顶层 system 字段使用。
// 桌面端的 system 是 `[{type:"text",text:…}]`，与 messages[0].content 同构。
func systemBlocks(msgs []map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, m := range msgs {
		if r, _ := m["role"].(string); r != "system" {
			continue
		}
		switch c := m["content"].(type) {
		case string:
			if c != "" {
				out = append(out, map[string]any{"type": "text", "text": c})
			}
		case []any:
			for _, blk := range c {
				b, ok := blk.(map[string]any)
				if !ok {
					continue
				}
				if t, ok := b["type"].(string); ok && t != "" && t != "text" {
					continue
				}
				if txt, ok := b["text"].(string); ok && txt != "" {
					out = append(out, map[string]any{"type": "text", "text": txt})
				}
			}
		}
	}
	return out
}

// truncateRunes 截断到 n 个 rune。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
