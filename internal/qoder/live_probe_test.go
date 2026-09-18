//go:build live

// live_probe_test.go Qoder「思考档位」真实账号探针。
//
// 目的：验证参考实现（oh-my-pi-plugin-qoder / Peer-Agent）给出的结论在真实上游是否成立：
//  1. 上游模型目录里每个模型是否返回 thinking_config（含 effort ladder 与 is_default）；
//  2. 老版 api3（COSY / agent_chat_generation）是否接受 parameters.reasoning_effort，
//     以及取值非法时是 400 还是静默忽略；
//  3. 顶层 reasoningEffort（camelCase）是否需要与 parameters.reasoning_effort 双写；
//  4. 关闭语义是否成立（none + max_thinking_tokens=0）。
//
// 本文件带 `live` build tag，**默认不参与编译**（go build ./... / go test ./... 都不会碰它），
// 也不改任何生产代码路径。测完即可删除。
//
// 运行方式（在 wild-work 仓库根目录）：
//
//	WILDWORK_AUTHDIR=./auths go test -tags live ./internal/qoder/ -run TestLiveProbe -v -timeout 900s
//
// 可调环境变量：
//
//	WILDWORK_AUTHDIR       账号目录（默认 ./auths），读取 qoder*.json
//	WILDWORK_PROBE_MODEL   客户端模型名（默认 deepseek-v4-pro）
//	WILDWORK_PROBE_PROMPT  探测用 prompt（默认见 defaultProbePrompt）
//	WILDWORK_PROBE_REPEAT  每组重复次数（默认 1，可 1~5，取平均以降低方差）
//
// 关于 prompt（重要）：**必须用「需要多步推理」的题**。像 "1+1=?" 这种题，
// 模型各档位几乎都不产生思考链，reasoning_content 全是空的，只能验证字段是否
// 被接受（状态码），验证不了强度差异。默认题见 defaultProbePrompt。
//
// 成本：目录探测 0 次对话；档位探测 = 用例数 × 重复次数 次对话（默认 6 次）。
package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"wild-work/internal/auth"
)

// liveAuth 从 WILDWORK_AUTHDIR（默认 ./auths）取第一个 Qoder 账号。
func liveAuth(t *testing.T) *auth.Auth {
	t.Helper()
	dir := os.Getenv("WILDWORK_AUTHDIR")
	if dir == "" {
		dir = "auths"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("解析目录失败: %v", err)
	}
	as, err := auth.LoadQoderDir(abs)
	if err != nil {
		t.Fatalf("加载 %s 下的 qoder*.json 失败: %v", abs, err)
	}
	if len(as) == 0 {
		t.Skipf("在 %s 没找到 qoder*.json —— 先用面板登录一个 Qoder 账号，或用 WILDWORK_AUTHDIR 指向账号目录", abs)
	}
	a := as[0]
	t.Logf("账号：uid=%s nickname=%s file=%s", a.UID, a.Nickname, a.FilePath)
	return a
}

// fetchModelsRaw 拉上游模型目录并返回**原始** entry JSON。
// 与 models.go 的 fetchModels 同构，区别是保留原始 JSON —— 因为 DynamicModel
// 没有 thinking_config 字段，只有原始 JSON 才能看到上游到底返回了什么。
func (c *Client) fetchModelsRaw(a *auth.Auth) ([]json.RawMessage, error) {
	dt := a.JWT()
	if dt == "" {
		return nil, fmt.Errorf("no dt- available")
	}
	rawURL := c.gatewayBase() + EpModels
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	sess, err := NewCosySession(a.MachineID, a.MachineToken, a.MachineType, a.Nickname, a.UID, dt, a.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("cosy session: %w", err)
	}
	if err := sess.ApplyHeaders(req, "", rawURL, a.UID, false, ""); err != nil {
		return nil, fmt.Errorf("cosy headers: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models api status %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var apiResp map[string]json.RawMessage
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("models parse: %w", err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(apiResp["chat"], &entries); err != nil {
		return nil, fmt.Errorf("chat scene parse: %w", err)
	}
	return entries, nil
}

// TestLiveProbeModelCatalog 第 1 步：打印上游模型目录里的思考相关字段。
// 零对话成本，先跑这个 —— 若上游压根不返回 thinking_config，档位就只能硬编码。
func TestLiveProbeModelCatalog(t *testing.T) {
	a := liveAuth(t)
	c := New()
	entries, err := c.fetchModelsRaw(a)
	if err != nil {
		t.Fatalf("拉模型目录失败: %v", err)
	}
	t.Logf("目录共 %d 个模型", len(entries))

	withTC := 0
	for _, e := range entries {
		var m map[string]any
		if err := json.Unmarshal(e, &m); err != nil {
			continue
		}
		key, _ := m["key"].(string)
		if enabled, ok := m["enable"].(bool); ok && !enabled {
			continue
		}
		isReasoning, _ := m["is_reasoning"].(bool)
		tc, hasTC := m["thinking_config"]
		if hasTC {
			withTC++
			b, _ := json.Marshal(tc)
			t.Logf("  %-16s is_reasoning=%-5v thinking_config=%s", key, isReasoning, string(b))
		} else {
			t.Logf("  %-16s is_reasoning=%-5v thinking_config=<无>", key, isReasoning)
		}
	}
	t.Logf("==> %d 个模型带 thinking_config", withTC)
	if withTC == 0 {
		t.Log("==> 上游未返回 thinking_config：档位无上游依据，只能按模型硬编码或用默认档")
	}
}

// defaultProbePrompt 默认探测题：刻意选「需要多步推理、答案短」的题。
// 这样 content 短、reasoning_content 长，档位差异最容易看出来。
const defaultProbePrompt = `9 个外观完全相同的球中有 1 个重量异常（可能偏重也可能偏轻），其余 8 个等重。` +
	`只用一个天平称 3 次，找出这个异常球并判断它是偏重还是偏轻。` +
	`请逐步推理给出完整的称量方案，并说明为什么 3 次一定够。`

// probePrompt 探测用 prompt，可用 WILDWORK_PROBE_PROMPT 覆盖。
func probePrompt() string {
	if p := strings.TrimSpace(os.Getenv("WILDWORK_PROBE_PROMPT")); p != "" {
		return p
	}
	return defaultProbePrompt
}

// probeRepeat 每组重复次数（默认 1），可用 WILDWORK_PROBE_REPEAT 覆盖（1~5）。
// 单次结果有方差，重复 2~3 次取平均更稳（成本线性增加）。
func probeRepeat() int {
	if v := strings.TrimSpace(os.Getenv("WILDWORK_PROBE_REPEAT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 5 {
			return n
		}
	}
	return 1
}

// probeCase 一组档位注入方式。
type probeCase struct {
	name string
	// shortPrompt 为 true 时用最简 prompt：只关心状态码的用例（如非法值），
	// 不必花长思考的钱。
	shortPrompt bool
	mutate      func(obj map[string]any)
}

// probeResult 单组结果（重复多次时长度与耗时取平均）。
type probeResult struct {
	name      string
	status    int
	elapsed   time.Duration
	reasoning int // 平均思考长度（rune 数）
	content   string
	note      string
}

// TestLiveProbeEffort 第 2 步：同一 prompt 下横向对比不同字段注入的行为。
//
// 判读：
//   - B/E/F 的思考长度显著大于 A（倍数列）→ 档位字段生效
//   - C 归零或显著变小                     → 关闭语义成立
//   - D 非 200 → 上游严格校验（客户端须白名单）；200 → 静默忽略（不能盲信）
//
// 若基线（A）思考长度就是 0，说明 prompt 没触发思考链，换更难的题重跑：
//
//	WILDWORK_PROBE_PROMPT='<更难的题>' go test ...
func TestLiveProbeEffort(t *testing.T) {
	a := liveAuth(t)
	c := New()

	clientModel := os.Getenv("WILDWORK_PROBE_MODEL")
	if clientModel == "" {
		clientModel = "deepseek-v4-pro"
	}
	modelKey := c.modelKey(clientModel)
	if modelKey == "" {
		modelKey = clientModel
	}
	prompt := probePrompt()
	repeat := probeRepeat()
	t.Logf("探测模型：客户端名=%s 上游 key=%s；每组重复 %d 次", clientModel, modelKey, repeat)
	t.Logf("探测 prompt（%d 字）：%s", len([]rune(prompt)), truncate(strings.ReplaceAll(prompt, "\n", " "), 90))

	params := func(o map[string]any) map[string]any {
		p, _ := o["parameters"].(map[string]any)
		if p == nil {
			p = map[string]any{}
			o["parameters"] = p
		}
		return p
	}

	cases := []probeCase{
		{name: "A 基线（无 parameters）"},
		{name: "B parameters.reasoning_effort=high", mutate: func(o map[string]any) {
			params(o)["reasoning_effort"] = "high"
		}},
		{name: "C none + max_thinking_tokens=0（关闭语义）", mutate: func(o map[string]any) {
			params(o)["reasoning_effort"] = "none"
			params(o)["max_thinking_tokens"] = 0
		}},
		{name: "D 非法值 bogus（只看状态码）", shortPrompt: true, mutate: func(o map[string]any) {
			params(o)["reasoning_effort"] = "bogus"
		}},
		{name: "E 顶层 reasoningEffort=high（单写）", mutate: func(o map[string]any) {
			o["reasoningEffort"] = "high"
		}},
		{name: "F 顶层 + parameters 双写 high", mutate: func(o map[string]any) {
			o["reasoningEffort"] = "high"
			params(o)["reasoning_effort"] = "high"
		}},
	}

	results := make([]probeResult, 0, len(cases))
	for _, tc := range cases {
		usePrompt := prompt
		if tc.shortPrompt {
			usePrompt = "1+1=?"
		}
		res := probeResult{name: tc.name}
		totalReasoning, okCount := 0, 0
		for i := 0; i < repeat; i++ {
			messages := []map[string]any{{"role": "user", "content": usePrompt}}
			raw, err := buildAgentBody(messages, modelKey, nil, true)
			if err != nil {
				t.Fatalf("%s: 构造 body 失败: %v", tc.name, err)
			}
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatalf("%s: body 解析失败: %v", tc.name, err)
			}
			if tc.mutate != nil {
				tc.mutate(obj)
			}
			body, _ := json.Marshal(obj)

			start := time.Now()
			rc, status, respBody, err := c.ChatStream(a, body)
			res.elapsed += time.Since(start)
			if err != nil {
				res.note = "传输层失败: " + err.Error()
				break
			}
			res.status = status
			if rc == nil {
				// 非 2xx：上游拒绝，响应体是判读依据
				res.note = truncate(string(respBody), 200)
				break
			}
			msg, aggErr := aggregate(rc, clientModel)
			_ = rc.Close()
			if aggErr != nil {
				res.note = "流解析失败: " + aggErr.Error()
				break
			}
			if v, ok := msg["reasoning_content"].(string); ok {
				totalReasoning += len([]rune(v))
			}
			if v, ok := msg["content"].(string); ok {
				res.content = truncate(strings.ReplaceAll(v, "\n", " "), 48)
			}
			okCount++
		}
		if okCount > 0 {
			res.reasoning = totalReasoning / okCount
			res.elapsed /= time.Duration(okCount)
		}
		results = append(results, res)
	}

	base := results[0].reasoning
	t.Logf("%-40s %-6s %-9s %-14s %s", "用例", "状态", "耗时", "思考长度", "备注")
	for _, r := range results {
		ratio := "-"
		if base > 0 && r.reasoning > 0 {
			ratio = fmt.Sprintf("%.2fx", float64(r.reasoning)/float64(base))
		}
		detail := r.content
		if r.note != "" {
			detail = r.note
		}
		t.Logf("%-40s %-6d %-9s %-14s %s", r.name, r.status, r.elapsed.Round(time.Millisecond),
			fmt.Sprintf("%d (%s)", r.reasoning, ratio), detail)
	}

	if base == 0 {
		t.Log("==> 基线思考长度为 0：当前 prompt 没触发思考链，比不出档位差异。")
		t.Log("==> 换更难的题重跑，例如：WILDWORK_PROBE_PROMPT='用 1~9 九个数字各一次组成三个三位数，使第二个是第一个的 2 倍、第三个是 3 倍，求所有解，并逐步推理'")
	} else {
		t.Log("==> 看「思考长度」列的倍数：B/E/F 明显大于 A → 档位生效；C 归零 → 关闭语义成立。")
	}
	t.Log("==> D 非 200 → 上游严格校验（客户端必须白名单）；D 200 → 静默忽略，不能盲信参考实现。")
}
