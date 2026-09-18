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
//	WILDWORK_AUTHDIR=./auths go test -tags live ./internal/qoder/ -run TestLiveProbe -v -timeout 600s
//
// 账号来源：auths/qoder*.json（与面板登录 Qoder 产生的文件一致）；
// 也可用 WILDWORK_AUTHDIR 指向别处的目录。
//
// 成本：目录探测 0 次对话；档位探测 5 次极短对话（prompt 只有 "1+1"）。
package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// probeCase 一组档位注入方式。
type probeCase struct {
	name   string
	mutate func(obj map[string]any)
}

// TestLiveProbeEffort 第 2 步：对比不同字段注入下上游的行为。
//
// 判读：
//   - 200 且 reasoning_content 明显变长  → 字段生效
//   - 200 但 reasoning_content 为空/不变 → 字段被静默忽略（不可依赖）
//   - 非 200                            → 上游严格校验（字段名/取值需修正）
func TestLiveProbeEffort(t *testing.T) {
	a := liveAuth(t)
	c := New()

	// 模型：默认 dmodel（deepseek-v4-pro）；可用 WILDWORK_PROBE_MODEL 覆盖成
	// 客户端名（如 qwen3.8-max），探针会自动映射成上游 key。
	clientModel := os.Getenv("WILDWORK_PROBE_MODEL")
	if clientModel == "" {
		clientModel = "deepseek-v4-pro"
	}
	modelKey := c.modelKey(clientModel)
	if modelKey == "" {
		modelKey = clientModel
	}
	t.Logf("探测模型：客户端名=%s 上游 key=%s", clientModel, modelKey)

	params := func(o map[string]any) map[string]any {
		p, _ := o["parameters"].(map[string]any)
		if p == nil {
			p = map[string]any{}
			o["parameters"] = p
		}
		return p
	}

	cases := []probeCase{
		{"A 基线（无 parameters）", nil},
		{"B parameters.reasoning_effort=high", func(o map[string]any) {
			params(o)["reasoning_effort"] = "high"
		}},
		{"C none + max_thinking_tokens=0（关闭语义）", func(o map[string]any) {
			params(o)["reasoning_effort"] = "none"
			params(o)["max_thinking_tokens"] = 0
		}},
		{"D 非法值 bogus（测严格性）", func(o map[string]any) {
			params(o)["reasoning_effort"] = "bogus"
		}},
		{"E 顶层 reasoningEffort=high（双写验证）", func(o map[string]any) {
			o["reasoningEffort"] = "high"
		}},
		{"F 顶层+parameters 双写 high", func(o map[string]any) {
			o["reasoningEffort"] = "high"
			params(o)["reasoning_effort"] = "high"
		}},
	}

	t.Logf("%-38s %-6s %-8s %-10s %s", "用例", "状态", "耗时", "思考长度", "正文摘要")
	for _, tc := range cases {
		messages := []map[string]any{{"role": "user", "content": "1+1=?"}}
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
		elapsed := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Errorf("%-38s 传输层失败: %v", tc.name, err)
			continue
		}
		if rc == nil {
			// 非 2xx：上游拒绝，响应体是判读依据
			t.Logf("%-38s %-6d %-8s %-10s %s", tc.name, status, elapsed, "-", truncate(string(respBody), 220))
			continue
		}
		msg, aggErr := aggregate(rc, clientModel)
		_ = rc.Close()
		if aggErr != nil {
			t.Logf("%-38s %-6d %-8s 流解析失败: %v", tc.name, status, elapsed, aggErr)
			continue
		}
		reasoning := ""
		if v, ok := msg["reasoning_content"].(string); ok {
			reasoning = v
		}
		content := ""
		if v, ok := msg["content"].(string); ok {
			content = v
		}
		t.Logf("%-38s %-6d %-8s %-10d %s", tc.name, status, elapsed, len(reasoning),
			truncate(strings.ReplaceAll(content, "\n", " "), 60))
	}

	t.Log("判读：B/E/F 的「思考长度」明显大于 A → 档位字段生效；")
	t.Log("      C 的思考长度为 0 或明显变小 → 关闭语义成立；")
	t.Log("      D 返回非 200 → 上游严格校验（客户端必须白名单）；D 返回 200 → 静默忽略（不能盲信）。")
}
