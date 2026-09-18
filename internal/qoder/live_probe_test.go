//go:build live

// live_probe_test.go Qoder「思考档位」真实账号探针。
//
// 目的：验证参考实现（oh-my-pi-plugin-qoder / Peer-Agent）给出的结论在真实上游是否成立：
//  1. 上游模型目录里每个模型是否返回 thinking_config（含 effort ladder 与 is_default）；
//  2. is_reasoning=true/false 是否真的改变思考链长度（这是「思考到底开没开」的唯一硬证据）；
//  3. 老版 api3（COSY / agent_chat_generation）是否接受 parameters.reasoning_effort，
//     以及取值非法时是 400 还是静默忽略；
//  4. 顶层 reasoningEffort（camelCase）是否需要与 parameters.reasoning_effort 双写；
//  5. 关闭语义是否成立（none + max_thinking_tokens=0）。
//
// 本文件带 `live` build tag，**默认不参与编译**（go build ./... / go test ./... 都不会碰它），
// 也不改任何生产代码路径。测完即可删除。
//
// 运行方式（在 wild-work 仓库根目录）：
//
//	WILDWORK_AUTHDIR=./auths go test -tags live ./internal/qoder/ -run TestLiveProbe -v -timeout 1800s
//
// 可调环境变量：
//
//	WILDWORK_AUTHDIR       账号目录（默认 ./auths），读取 qoder*.json
//	WILDWORK_PROBE_MODEL   客户端模型名（默认 deepseek-v4-pro）
//	WILDWORK_PROBE_KEY     直接指定上游 model key，跳过映射（排查「模型名没映射上」时用）
//	WILDWORK_PROBE_PROMPT  探测用 prompt（默认见 defaultProbePrompt）
//	WILDWORK_PROBE_REPEAT  每组重复次数（默认 1，可 1~5，取平均以降低方差）
//	WILDWORK_PROBE_CASES   只跑指定用例 id（逗号分隔，如 "1,2,3"；默认全跑）
//	WILDWORK_PROBE_DUMP    设为目录时把每次请求的原始 SSE 落盘（probe-<id>-<n>.sse）
//
// # 关于 prompt（重要）
//
// **必须用「需要多步推理」的题**。像 "1+1=?" 这种题，模型各档位几乎都不产生思考链，
// reasoning_content 全是空的，只能验证字段是否被接受（状态码），验证不了强度差异。
//
// # 关于计时的坑（2026-09-19 修正）
//
// ChatStream 只返回 resp.Body —— 响应头一到就返回，真正的生成发生在 aggregate() 读流阶段。
// 旧版把 time.Since(start) 卡在 ChatStream 返回处，量到的是 TTFB（≈1s），于是
// 「总耗时 395s / 12 次 = 33s 一次」却显示成「1.11s」。现在分别记录 TTFB 与总耗时。
//
// # 关于模型 key 的坑（2026-09-19 修正）
//
// 静态表 staticModelKeys 只有老模型（无 qwen3.8-flash 等新名），客户端名要靠
// FetchModels 拉到 display_name 才能映射成上游 key（如 qwen3.8-flash → qfmodel）。
// 旧版探针没先调 FetchModels，未命中就回退成「把客户端名原样当 key 发」，
// 上游 200 兜底但内容/思考双空 —— 会把「模型选错」误判成「档位不生效」。
// 现在先调 FetchModels 建映射，未命中会显式告警。
//
// 成本：目录探测 0 次对话；档位探测 = 用例数 × 重复次数 次对话。日志会先打印预估耗时。
package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

// catalogEntry 上游目录里一个模型的探针视图。
type catalogEntry struct {
	Key         string
	DisplayName string
	ClientName  string // NormalizeModelName(DisplayName)
	Enable      bool
	IsReasoning bool
	MaxInput    int64
	HasTC       bool
	TC          json.RawMessage
	Caps        thinkCaps
}

// loadCatalog 拉目录并解析出探针需要的字段（同时打印）。
func loadCatalog(t *testing.T, c *Client, a *auth.Auth) []catalogEntry {
	t.Helper()
	raw, err := c.fetchModelsRaw(a)
	if err != nil {
		t.Fatalf("拉模型目录失败: %v", err)
	}
	out := make([]catalogEntry, 0, len(raw))
	for _, e := range raw {
		var m map[string]any
		if err := json.Unmarshal(e, &m); err != nil {
			continue
		}
		ce := catalogEntry{}
		ce.Key, _ = m["key"].(string)
		ce.DisplayName, _ = m["display_name"].(string)
		ce.ClientName = NormalizeModelName(ce.DisplayName)
		ce.Enable, _ = m["enable"].(bool)
		ce.IsReasoning, _ = m["is_reasoning"].(bool)
		if v, ok := m["max_input_tokens"].(float64); ok {
			ce.MaxInput = int64(v)
		}
		if tc, ok := m["thinking_config"]; ok && tc != nil {
			b, _ := json.Marshal(tc)
			ce.HasTC = true
			ce.TC = b
			ce.Caps = parseThinkingConfig(b)
		}
		out = append(out, ce)
	}
	return out
}

// TestLiveProbeModelCatalog 第 1 步：打印上游模型目录里的思考相关字段。
// 零对话成本，先跑这个 —— 若上游压根不返回 thinking_config，档位就只能硬编码。
func TestLiveProbeModelCatalog(t *testing.T) {
	a := liveAuth(t)
	c := New()
	entries := loadCatalog(t, c, a)
	t.Logf("目录共 %d 个模型（客户端名 = display_name 规范化，生产环境用这个名字调）", len(entries))

	withTC := 0
	for _, e := range entries {
		if !e.Enable {
			continue
		}
		if !e.HasTC {
			t.Logf("  %-16s client=%-16s is_reasoning=%-5v thinking_config=<无>", e.Key, e.ClientName, e.IsReasoning)
			continue
		}
		withTC++
		t.Logf("  %-16s client=%-16s is_reasoning=%-5v max_in=%-7d ladder=%v default_on=%v disabled=%v",
			e.Key, e.ClientName, e.IsReasoning, e.MaxInput, e.Caps.Efforts, e.Caps.EnabledDefault, e.Caps.HasDisabled)
		t.Logf("      raw=%s", truncate(string(e.TC), 220))
	}
	t.Logf("==> %d 个模型带 thinking_config", withTC)
	if withTC == 0 {
		t.Log("==> 上游未返回 thinking_config：档位无上游依据，只能按模型硬编码或用默认档")
	} else {
		t.Log("==> ladder 就是该模型真实支持的档位集合；探针第 2 步会自动从这里挑档位来测")
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

// ---- 思考能力解析 ----

// thinkCaps 从 thinking_config 提取的能力。
type thinkCaps struct {
	HasDisabled    bool     // 带 disabled 节点 → 支持显式关闭
	EnabledDefault bool     // enabled.is_default → 默认就开思考
	Efforts        []string // enabled.efforts 的键（真实支持的档位集合）
	DefaultEffort  string   // efforts 里标了 is_default 的那个（上游的默认档）
}

// effortRank 档位强度排序（与 internal/reasoning 的档位表对齐）。
var effortRank = map[string]int{
	"none": 0, "minimal": 1, "low": 2, "medium": 3,
	"high": 4, "xhigh": 5, "max": 6, "ultra": 7,
}

// parseThinkingConfig 解析 thinking_config：
//
//	{"disabled":{...},"enabled":{"is_default":true,"efforts":{"low":{},"medium":{"is_default":true},"xhigh":{}}}}
//
// efforts 是对象（键即档位名，值里可能带 is_default/description），不是数组。
func parseThinkingConfig(raw json.RawMessage) thinkCaps {
	var tc struct {
		Disabled json.RawMessage `json:"disabled"`
		Enabled  struct {
			IsDefault bool `json:"is_default"`
			Efforts   map[string]struct {
				IsDefault bool `json:"is_default"`
			} `json:"efforts"`
		} `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return thinkCaps{}
	}
	caps := thinkCaps{
		HasDisabled:    len(tc.Disabled) > 0 && string(tc.Disabled) != "null",
		EnabledDefault: tc.Enabled.IsDefault,
	}
	for k, v := range tc.Enabled.Efforts {
		caps.Efforts = append(caps.Efforts, k)
		if v.IsDefault {
			caps.DefaultEffort = k
		}
	}
	sort.SliceStable(caps.Efforts, func(i, j int) bool {
		ri, oki := effortRank[caps.Efforts[i]]
		rj, okj := effortRank[caps.Efforts[j]]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki // 已知档位排前面
		}
		return caps.Efforts[i] < caps.Efforts[j]
	})
	return caps
}

// ---- 原始流抓取与诊断 ----

// captureReader 边读边留一份原始字节（诊断上游到底发了什么）。
type captureReader struct {
	r    io.Reader
	buf  bytes.Buffer
	max  int
	file *os.File
}

func newCaptureReader(r io.Reader, max int, path string) (*captureReader, error) {
	cr := &captureReader{r: r, max: max}
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		cr.file = f
	}
	return cr, nil
}

func (c *captureReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		if c.buf.Len() < c.max {
			room := c.max - c.buf.Len()
			if n < room {
				room = n
			}
			c.buf.Write(p[:room])
		}
		if c.file != nil {
			_, _ = c.file.Write(p[:n])
		}
	}
	return n, err
}

func (c *captureReader) Close() error {
	if c.file != nil {
		err := c.file.Close()
		c.file = nil
		return err
	}
	return nil
}

func (c *captureReader) Bytes() []byte { return c.buf.Bytes() }

// streamStats 原始流的解析统计 —— 用来区分「上游没发思考」与「我们没解析出来」。
type streamStats struct {
	Bytes     int
	DataLines int
	Chunks    int
	BadLines  int
	DeltaKeys map[string]int
	ContentIn int // 非空 delta.content 块数
	ReasonIn  int // 非空 delta.reasoning_content 块数
	ChoiceRsn int // 非空 choice.reasoning_content（思考挂在 choice 层而非 delta 层）
	MsgField  int // 出现 choice.message（非流式形态）
	First     []string
}

// scanStream 用生产同款解析器扫一遍原始字节，统计各字段出现情况。
// 若 Chunks==0 但 DataLines>0 → 上游 SSE 信封不是 data:{"body":"..."} 这个形状。
func scanStream(raw []byte) streamStats {
	st := streamStats{DeltaKeys: map[string]int{}, Bytes: len(raw)}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			if strings.Contains(payload, "[DONE]") {
				continue
			}
			st.DataLines++
			if len(st.First) < 3 {
				st.First = append(st.First, truncate(line, 160))
			}
		}
	}
	_ = parseNestedSSE(bytes.NewReader(raw), func(chunk map[string]any) error {
		st.Chunks++
		if choices, ok := chunk["choices"].([]any); ok {
			for _, ci := range choices {
				ch, _ := ci.(map[string]any)
				if ch == nil {
					continue
				}
				if _, ok := ch["message"]; ok {
					st.MsgField++
				}
				if v, ok := ch["reasoning_content"].(string); ok && v != "" {
					st.ChoiceRsn++
				}
				delta, _ := ch["delta"].(map[string]any)
				if delta == nil {
					continue
				}
				for k, v := range delta {
					if s, ok := v.(string); !ok || s != "" {
						st.DeltaKeys[k]++
					}
				}
				if v, ok := delta["content"].(string); ok && v != "" {
					st.ContentIn++
				}
				if v, ok := delta["reasoning_content"].(string); ok && v != "" {
					st.ReasonIn++
				}
			}
		}
		return nil
	})
	if st.DataLines > st.Chunks {
		st.BadLines = st.DataLines - st.Chunks
	}
	return st
}

func (s streamStats) String() string {
	keys := make([]string, 0, len(s.DeltaKeys))
	for k, n := range s.DeltaKeys {
		keys = append(keys, fmt.Sprintf("%s×%d", k, n))
	}
	sort.Strings(keys)
	return fmt.Sprintf("原始%dB data行%d chunk%d 未解析%d content块%d reasoning块%d choice层思考%d 键=[%s]",
		s.Bytes, s.DataLines, s.Chunks, s.BadLines, s.ContentIn, s.ReasonIn, s.ChoiceRsn, strings.Join(keys, " "))
}

// ---- 用例 ----

// probeCase 一组档位注入方式。
type probeCase struct {
	id     string
	name   string
	inject string // 实际注入的字段（打印出来对照）
	// shortPrompt 为 true 时用最简 prompt：只关心状态码的用例（如非法值），
	// 不必花长思考的钱。
	shortPrompt bool
	// reasoning 传给 buildAgentBody 的 is_reasoning 值。
	reasoning bool
	mutate    func(obj map[string]any)
}

// probeResult 单组结果（重复多次时长度与耗时取平均）。
type probeResult struct {
	id        string
	name      string
	status    int
	ttfb      time.Duration // 响应头到达耗时
	total     time.Duration // 含读完整条流的耗时
	reasoning int           // 平均思考长度（rune 数）
	content   int           // 平均回答长度（rune 数）
	stats     streamStats
	note      string
}

// buildProbeCases 按模型能力生成用例表。
//
// 关键：**必须包含 is_reasoning=false / true 的对照**。旧版所有用例都硬编码
// is_reasoning=true，只比了 parameters 层，一旦上游根本不吐思考链就完全看不出来。
func buildProbeCases(caps thinkCaps) []probeCase {
	def := "high"
	maxEff := "high"
	if len(caps.Efforts) > 0 {
		def = caps.Efforts[len(caps.Efforts)/2]
		maxEff = caps.Efforts[len(caps.Efforts)-1]
	}
	if caps.DefaultEffort != "" {
		def = caps.DefaultEffort // 优先用上游标了 is_default 的那个
	}
	setParam := func(k string, v any) func(map[string]any) {
		return func(o map[string]any) {
			p, _ := o["parameters"].(map[string]any)
			if p == nil {
				p = map[string]any{}
				o["parameters"] = p
			}
			p[k] = v
		}
	}
	cases := []probeCase{
		{id: "1", name: "is_reasoning=false（关闭基线）", inject: "model_config.is_reasoning=false", reasoning: false},
		{id: "2", name: "is_reasoning=true（当前生产行为）", inject: "model_config.is_reasoning=true", reasoning: true},
		{id: "3", name: fmt.Sprintf("parameters.reasoning_effort=%s（默认档）", def),
			inject: "is_reasoning=true + parameters.reasoning_effort=" + def, reasoning: true,
			mutate: setParam("reasoning_effort", def)},
		{id: "5", name: fmt.Sprintf("顶层 reasoningEffort=%s（单写）", def),
			inject: "is_reasoning=true + 顶层 reasoningEffort=" + def, reasoning: true,
			mutate: func(o map[string]any) { o["reasoningEffort"] = def }},
		{id: "6", name: fmt.Sprintf("顶层 + parameters 双写 %s", def),
			inject: "is_reasoning=true + 顶层 reasoningEffort=" + def + " + parameters.reasoning_effort=" + def, reasoning: true,
			mutate: func(o map[string]any) {
				o["reasoningEffort"] = def
				setParam("reasoning_effort", def)(o)
			}},
		{id: "7", name: "none + max_thinking_tokens=0（关闭语义）",
			inject: "is_reasoning=true + parameters.reasoning_effort=none + max_thinking_tokens=0", reasoning: true,
			mutate: func(o map[string]any) {
				p, _ := o["parameters"].(map[string]any)
				if p == nil {
					p = map[string]any{}
					o["parameters"] = p
				}
				p["reasoning_effort"] = "none"
				p["max_thinking_tokens"] = 0
			}},
		{id: "8", name: "非法值 bogus（只看状态码）", inject: "parameters.reasoning_effort=bogus",
			shortPrompt: true, reasoning: true,
			mutate: setParam("reasoning_effort", "bogus")},
	}
	if maxEff != def {
		cases = append(cases[:3], append([]probeCase{{
			id: "4", name: fmt.Sprintf("parameters.reasoning_effort=%s（最高档）", maxEff),
			inject: "is_reasoning=true + parameters.reasoning_effort=" + maxEff, reasoning: true,
			mutate: setParam("reasoning_effort", maxEff),
		}}, cases[3:]...)...)
	}
	return cases
}

// filterCases 按 WILDWORK_PROBE_CASES（逗号分隔 id）过滤。
func filterCases(all []probeCase) []probeCase {
	sel := strings.TrimSpace(os.Getenv("WILDWORK_PROBE_CASES"))
	if sel == "" {
		return all
	}
	want := map[string]bool{}
	for _, s := range strings.Split(sel, ",") {
		want[strings.TrimSpace(s)] = true
	}
	out := make([]probeCase, 0, len(all))
	for _, c := range all {
		if want[c.id] {
			out = append(out, c)
		}
	}
	return out
}

// TestLiveProbeCaseTableDryRun 零成本自检：用真实抓到的 thinking_config 样本
// 验证 ladder 解析与用例生成，并打印某个用例实际发出的 body 片段。
// 不联网、不消耗积分 —— 先跑这个确认逻辑，再去花 8×33s 跑真实探测。
func TestLiveProbeCaseTableDryRun(t *testing.T) {
	samples := map[string]string{
		"qwen3.8 系（low/medium/xhigh）": `{"disabled":{},"enabled":{"efforts":{"low":{},"medium":{"is_default":true},"xhigh":{}},"is_default":true}}`,
		"deepseek/glm 系（high/max）":    `{"disabled":{"description":"Disable thinking"},"enabled":{"description":"Enable thinking","efforts":{"high":{"description":"High thinking intensity"},"max":{"description":"Maximum thinking intensity","is_default":true}},"is_default":true}}`,
		"无 ladder（只有开关）":              `{"disabled":{"description":"Disable thinking"},"enabled":{"description":"Enable thinking","is_default":true}}`,
	}
	names := make([]string, 0, len(samples))
	for k := range samples {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, name := range names {
		caps := parseThinkingConfig(json.RawMessage(samples[name]))
		t.Logf("== %s", name)
		t.Logf("   ladder=%v default_on=%v disabled=%v", caps.Efforts, caps.EnabledDefault, caps.HasDisabled)
		for _, tc := range buildProbeCases(caps) {
			t.Logf("   [%s] %-34s 注入: %s", tc.id, tc.name, tc.inject)
		}
	}

	// 打印一个真实 body 的思考相关字段，确认字段位置对得上参考实现。
	caps := parseThinkingConfig(json.RawMessage(samples["qwen3.8 系（low/medium/xhigh）"]))
	tc := buildProbeCases(caps)[2]
	raw, err := buildAgentBody([]map[string]any{{"role": "user", "content": "hi"}}, "qfmodel", nil, tc.reasoning)
	if err != nil {
		t.Fatalf("构造 body 失败: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("解析 body 失败: %v", err)
	}
	tc.mutate(obj)
	mc, _ := obj["model_config"].(map[string]any)
	p, _ := obj["parameters"].(map[string]any)
	t.Logf("== 用例 %s 的请求体关键字段：model_config=%v parameters=%v 顶层 reasoningEffort=%v",
		tc.id, mc, p, obj["reasoningEffort"])
	if p == nil {
		t.Log("!! parameters 缺失：档位无处可放（生产实现若要支持档位需先补这个对象）")
	}
}

// TestLiveProbeEffort 第 2 步：同一 prompt 下横向对比不同字段注入的行为。
//
// 判读：
//   - 用例 2（is_reasoning=true）的思考长度显著大于用例 1（false）→ 开关有效
//   - 用例 3/4 之间出现长度梯度           → 档位字段生效
//   - 用例 7 归零或显著变小               → 关闭语义成立
//   - 用例 8 非 200 → 上游严格校验（客户端须白名单）；200 → 静默忽略（不能盲信）
//
// 若所有用例的「回答」列都是 0，说明流根本没解析出来 —— 先看每行下方的
// 「原始流」诊断与首行样本，多半是模型 key 没映射上或 SSE 信封变了。
func TestLiveProbeEffort(t *testing.T) {
	a := liveAuth(t)
	c := New()

	clientModel := os.Getenv("WILDWORK_PROBE_MODEL")
	if clientModel == "" {
		clientModel = "deepseek-v4-pro"
	}

	// 关键：先建动态映射表，否则新模型名（如 qwen3.8-flash）映射不到上游 key。
	entries := loadCatalog(t, c, a)
	if _, err := c.FetchModels(a); err != nil {
		t.Logf("警告：FetchModels 建映射失败（%v），将退回静态表", err)
	}

	modelKey := strings.TrimSpace(os.Getenv("WILDWORK_PROBE_KEY"))
	if modelKey == "" {
		modelKey = c.modelKey(clientModel)
	}
	if modelKey == "" {
		modelKey = clientModel
		t.Logf("警告：客户端名 %q 没映射到任何上游 key（静态表与动态目录都没有）。", clientModel)
		t.Logf("     会把 %q 原样当作 key 发给上游 —— 上游通常 200 兜底但内容/思考双空，结论不可用。", modelKey)
		t.Logf("     可用 WILDWORK_PROBE_MODEL 指定目录里的 client 名，或用 WILDWORK_PROBE_KEY 直接指定 key。")
	}

	var caps thinkCaps
	for _, e := range entries {
		if e.Key == modelKey {
			caps = e.Caps
			break
		}
	}
	if len(caps.Efforts) == 0 {
		t.Logf("警告：上游目录里 %s 没有 effort ladder，档位用例会退化为 high 单档", modelKey)
	}

	prompt := probePrompt()
	repeat := probeRepeat()
	t.Logf("探测模型：客户端名=%s 上游 key=%s；每组重复 %d 次", clientModel, modelKey, repeat)
	t.Logf("模型 ladder：%v（默认档=%v，支持关闭=%v）", caps.Efforts, caps.EnabledDefault, caps.HasDisabled)
	t.Logf("探测 prompt（%d 字）：%s", len([]rune(prompt)), truncate(strings.ReplaceAll(prompt, "\n", " "), 90))

	cases := filterCases(buildProbeCases(caps))
	if len(cases) == 0 {
		t.Fatalf("WILDWORK_PROBE_CASES=%q 没匹配到任何用例", os.Getenv("WILDWORK_PROBE_CASES"))
	}
	dumpDir := strings.TrimSpace(os.Getenv("WILDWORK_PROBE_DUMP"))
	t.Logf("用例 %d 组 × 重复 %d 次 = %d 次对话；上次实测约 33s/次 → 预计 %.1f 分钟",
		len(cases), repeat, len(cases)*repeat, float64(len(cases)*repeat)*33/60)

	results := make([]probeResult, 0, len(cases))
	for _, tc := range cases {
		usePrompt := prompt
		if tc.shortPrompt {
			usePrompt = "1+1=?"
		}
		t.Logf("[用例 %s] %s | 注入: %s", tc.id, tc.name, tc.inject)
		res := probeResult{id: tc.id, name: tc.name}
		var sumReason, sumContent int
		var sumTTFB, sumTotal time.Duration
		okCount := 0
		for i := 0; i < repeat; i++ {
			messages := []map[string]any{{"role": "user", "content": usePrompt}}
			raw, err := buildAgentBody(messages, modelKey, nil, tc.reasoning)
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
			ttfb := time.Since(start)
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

			dumpPath := ""
			if dumpDir != "" {
				dumpPath = filepath.Join(dumpDir, fmt.Sprintf("probe-%s-%d.sse", tc.id, i))
			}
			cr, cerr := newCaptureReader(rc, 8<<20, dumpPath)
			if cerr != nil {
				t.Logf("%s: 落盘失败（忽略）: %v", tc.name, cerr)
				cr, _ = newCaptureReader(rc, 8<<20, "")
			}
			// 关键修正：计时必须覆盖读完整条流，ChatStream 返回的只是响应头。
			msg, aggErr := aggregate(cr, clientModel)
			total := time.Since(start)
			_ = cr.Close()
			_ = rc.Close()
			res.total, res.ttfb = total, ttfb
			res.stats = scanStream(cr.Bytes())
			if dumpPath != "" {
				t.Logf("  [dump] %s → %s", tc.name, dumpPath)
			}
			if aggErr != nil {
				res.note = "流解析失败: " + aggErr.Error()
				break
			}
			if v, ok := msg["reasoning_content"].(string); ok {
				sumReason += len([]rune(v))
			}
			if v, ok := msg["content"].(string); ok {
				sumContent += len([]rune(v))
			}
			sumTTFB += ttfb
			sumTotal += total
			okCount++
		}
		if okCount > 0 {
			res.reasoning = sumReason / okCount
			res.content = sumContent / okCount
			res.ttfb = sumTTFB / time.Duration(okCount)
			res.total = sumTotal / time.Duration(okCount)
		}
		results = append(results, res)
	}

	// 判读按用例 id 找，避免用 WILDWORK_PROBE_CASES 过滤后下标错位。
	byID := map[string]probeResult{}
	for _, r := range results {
		byID[r.id] = r
	}
	base := results[0].reasoning
	if on, ok := byID["2"]; ok {
		base = on.reasoning // 倍数基线取「思考已开、未注入档位」的那组
	}
	t.Logf("%-34s %-5s %-8s %-9s %-10s %-10s %s", "用例", "状态", "TTFB", "总耗时", "思考(字)", "回答(字)", "备注")
	for _, r := range results {
		ratio := "-"
		if base > 0 && r.reasoning > 0 {
			ratio = fmt.Sprintf("%.2fx", float64(r.reasoning)/float64(base))
		}
		note := r.note
		if note == "" && r.status == 200 {
			note = r.stats.String()
		}
		t.Logf("%-34s %-5d %-8s %-9s %-10s %-10s %s", r.name, r.status,
			r.ttfb.Round(time.Millisecond), r.total.Round(time.Millisecond),
			fmt.Sprintf("%d (%s)", r.reasoning, ratio), strconv.Itoa(r.content), note)
	}

	// 失败定位：全部为空时把原始流首行打出来，避免又一轮盲猜。
	for _, r := range results {
		if r.status == 200 && r.content == 0 && r.reasoning == 0 && r.note == "" {
			t.Logf("!! %s：状态 200 但内容/思考双空 —— 原始流诊断：%s", r.name, r.stats)
			for i, l := range r.stats.First {
				t.Logf("     line%d: %s", i+1, l)
			}
			break
		}
	}

	// 判读按用例 id 找。
	offCase, hasOff := byID["1"]
	onCase, hasOn := byID["2"]
	if hasOff && hasOn {
		switch {
		case offCase.reasoning == 0 && onCase.reasoning > 0:
			t.Logf("==> 开关有效：is_reasoning=false 思考 0 字，=true 思考 %d 字（对照成立）。", onCase.reasoning)
		case offCase.reasoning == 0 && onCase.reasoning == 0:
			t.Log("==> 两组都是 0：上游对本次请求没吐思考链 —— 换模型（目录里 is_reasoning=true 且带 ladder 的）或换更难的题重试。")
		case offCase.reasoning > 0:
			t.Logf("==> 关闭态竟然也有思考（%d 字）：该模型可能默认开思考，is_reasoning 关不掉。", offCase.reasoning)
		}
	} else if hasOn && onCase.reasoning == 0 {
		t.Log("==> 用例 2（is_reasoning=true）思考 0 字：上游没吐思考链，先看上面的原始流诊断。")
	}
	t.Log("==> 看「思考(字)」列：用例 2 明显大于 1 → 开关有效；3/4 之间有梯度 → 档位生效；7 归零 → 关闭语义成立。")
	t.Log("==> 用例 8 非 200 → 上游严格校验（客户端必须白名单）；200 → 静默忽略，不能盲信参考实现。")
}
