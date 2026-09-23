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
	"sync/atomic"
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
	Key                  string  // 上游 model key（如 qfmodel）
	DisplayName          string  // 目录里的 display_name（如 Qwen3.8-Flash）
	ClientName           string  // 客户端模型名（如 qwen3.8-flash），逐模型配置的查表键
	IsVL                 bool    // is_vl
	MaxInputTokens       int64   // max_input_tokens
	MaxOutputTokens      int64   // max_output_tokens（→ parameters.max_tokens）
	Format               string  // 上游声明的协议形态（回落 defaultModelFormat）
	Source               string  // 上游声明来源（model_config.source 即思考总开关，回落 defaultModelSource）
	DefaultContextWindow int64   // 上游标了 is_default 的档位
	ContextOptions       []int64 // 上游允许的全部窗口档位（升序）
}

// contextWindows 逐模型的上下文窗口档位（客户端模型名 → token 数）。
// 面板热更新，请求构造时读取；缺省即跟随上游 is_default 档。
var contextWindows atomic.Value // map[string]int64

// SetContextWindows 设置逐模型档位（nil 或空表示全部跟随上游默认档）。
// 存副本：调用方的 map 可能被继续修改。
func SetContextWindows(m map[string]int64) {
	cp := make(map[string]int64, len(m))
	for k, v := range m {
		if v > 0 {
			cp[k] = v
		}
	}
	contextWindows.Store(cp)
}

// contextWindowFor 取该客户端模型的档位目标值；未配置返回 0。
func contextWindowFor(clientModel string) int64 {
	m, _ := contextWindows.Load().(map[string]int64)
	return m[clientModel]
}

// pickContextWindow 按目标值从模型可选档位里取不超过目标的最高档；
// 目标为 0、无可用档位或目标低于最小档时回落上游 is_default 档。
func pickContextWindow(meta modelMeta, target int64) int64 {
	if target > 0 {
		best := int64(0)
		for _, opt := range meta.ContextOptions {
			if opt <= target && opt > best {
				best = opt
			}
		}
		if best > 0 {
			return best
		}
	}
	return meta.DefaultContextWindow
}

// buildAgentBody 构造请求体（无模型元数据时的兼容入口，元数据按默认值补）。
func buildAgentBody(messages []map[string]any, modelKey string, tools []any, spec reasoningSpec, contextWindow int64) ([]byte, error) {
	return buildAgentBodyMeta(messages, modelMeta{Key: modelKey}, tools, spec, contextWindow)
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
//	parameters.enable_thinking           = spec.Enabled    （**恒写**，与 is_reasoning 同源）
//	parameters.max_tokens                = meta.MaxOutputTokens（恒下发）
//	parameters.context_length            = contextWindow（调用方按「客户端提示 → 面板配置 → 上游默认档」解析，>0 才写）
//	model_config.format / source         = meta.Format / meta.Source（上游真值，缺失才回落常量）
//
// ⚠️ enable_thinking 必须恒写，不能只在档位非空时写 —— 详见 buildAgentBodyMeta
// 里 parameters 构造处的实测说明（缺它时上游关不掉思考）。
//
// 注意 1：developer 角色必须改写为 system。
// 注意 2：桌面端把 system 文本块**同时**放在顶层 system 与 messages[0]
// （A6e() 里 `E.unshift({role:"system", content:d})`），这里保持一致。
func buildAgentBodyMeta(messages []map[string]any, meta modelMeta, tools []any, spec reasoningSpec, contextWindow int64) ([]byte, error) {
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
	//
	// enable_thinking 与 model_config.is_reasoning **同源且恒下发**（不只档位非空时）：
	// 2026-09-22 实测（qwen3.8-flash，QoderCN 真实账号；本渠道与它同一上游目录、
	// 同一 qfmodel ladder），只发 is_reasoning=false 而**不带** enable_thinking 时
	// 上游关不掉思考 —— 流里照样吐 3127 个 reasoning 块（1.06MB），180s 都没结束、
	// content 一块没有；补上 enable_thinking=false 后同一请求 43.96s 正常收尾、
	// reasoning 块 0、正文 4797 字。
	// 复现与判读见 live_probe_test.go：用例 1（关不掉）vs 用例 12（能关掉）。
	params := map[string]any{"max_tokens": maxOutput}
	if spec.Effort != "" {
		params["reasoning_effort"] = spec.Effort
	}
	params["enable_thinking"] = spec.Enabled

	// format/source 取上游真值，未下发（或走静态表兜底）时才用兜底常量。
	// ⚠️ model_config.source 即思考总开关：完全不下发该字段会让上游不暴露思考过程（上游 issue #32）。
	format := meta.Format
	if format == "" {
		format = defaultModelFormat
	}
	source := meta.Source
	if source == "" {
		source = defaultModelSource
	}
	modelCfg := map[string]any{
		"key":              meta.Key,
		"display_name":     displayName,
		"model":            "",
		"format":           format,
		"is_vl":            meta.IsVL,
		"is_reasoning":     spec.Enabled,
		"api_key":          "",
		"url":              "",
		"source":           source,
		"max_input_tokens": maxInput,
	}
	// 上下文档位透传（上游 issue #27）：parameters.context_length + model_config.max_input_tokens
	// 双写 —— 只写 parameters 时 model_config 仍停在目录里的 max_input_tokens（多为 180000），
	// 档位选了却不生效。
	if contextWindow > 0 {
		params["context_length"] = contextWindow
		modelCfg["max_input_tokens"] = contextWindow
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
		"model_config": modelCfg,
		"system":       systemBlocks(msgs),
		"messages":     msgs,
		"tools":        tools,
		"parameters":   params,
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

// resolveContextWindow 解析本次请求应使用的上下文档位。
// 客户端值校验对齐官方 TZ；默认档与非法值落点取 max(AvailableWindows)
// （宁高勿低，对齐 workbuddy2api 广告口径），而非官方 FHA 的 is_default：
//  1. requested>0 且校验通过 → 用 requested
//  2. 否则 → max(AvailableWindows)（有档位表时）
//  3. 否则 → ContextWindow → MaxInputTokens → 0（不注入）
func resolveContextWindow(requested int64, mc *DynamicModel) int64 {
	if requested > 0 {
		if mc == nil {
			return requested
		}
		if len(mc.AvailableWindows) > 0 {
			for _, w := range mc.AvailableWindows {
				if w == requested {
					return requested
				}
			}
		} else {
			if mc.MaxInputTokens <= 0 || requested <= mc.MaxInputTokens {
				return requested
			}
		}
	}
	if mc == nil {
		return 0
	}
	if n := len(mc.AvailableWindows); n > 0 {
		return mc.AvailableWindows[n-1] // 升序，取最大档
	}
	if mc.ContextWindow > 0 {
		return mc.ContextWindow
	}
	if mc.MaxInputTokens > 0 {
		return mc.MaxInputTokens
	}
	return 0
}

// parseContextWindowHint 从 OpenAI 请求体读取客户端上下文档位提示。
// 同时认 context_length（上游参数名）与 context_window（官方 SDK 驼峰名）。
func parseContextWindowHint(body []byte) int64 {
	var hint struct {
		ContextLength int64 `json:"context_length"`
		ContextWindow int64 `json:"context_window"`
	}
	if err := json.Unmarshal(body, &hint); err != nil {
		return 0
	}
	if hint.ContextLength > 0 {
		return hint.ContextLength
	}
	return hint.ContextWindow
}
