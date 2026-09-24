//go:build live

// live_probe_test.go WorkBuddy 国际版「思考档位」真实账号探针 —— 静态表复核（B1）。
//
// 背景：internal/reasoning/catalog.go 的 globalEffortFallback 静态表来自对官方客户端
// 的逆向记录，代码注释自认「本仓库未独立复现」。本探针用真实账号拉上游目录，
// 逐条比对静态表与远端 supportedEfforts/defaultEffort，输出差异清单。
//
// 判据（R18）：远端目录接口是**权威**，静态表仅兜底。因此差异方向分两类：
//   - 静态表缺项（远端有、静态无）→ 影响：远端下发时无影响；远端缺失时该模型不降级。
//   - 静态表与远端不一致 → 影响：远端下发时以远端为准（静态表被覆盖）；
//     但**远端目录拉取失败**时静态表生效，此时错值会发出非法档位。
//
// 本探针**只读**：只调 EpCatalog，不刷新 token、不改账号文件。
// 因此可与运行中的实例共用账号目录 —— 但仍建议用副本，避免上游把只读探测也计入风控。
//
// 本文件带 `live` build tag，**默认不参与编译**，不改任何生产路径。
//
// 运行方式（仓库根目录）：
//
//	WILDWORK_AUTHDIR=./auths go test -tags live ./internal/workbuddyai/ -run TestLiveProbeEffortCatalog -v -timeout 300s
//
// 可调环境变量：
//
//	WILDWORK_AUTHDIR  账号目录（默认 ./auths），读取 workbuddyai-*.json
package workbuddyai

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"wild-work/internal/auth"
	"wild-work/internal/reasoning"
)

// liveAuth 从 WILDWORK_AUTHDIR（默认 ./auths）加载第一个 workbuddyai 账号。
func liveAuth(t *testing.T) *auth.Auth {
	t.Helper()
	dir := os.Getenv("WILDWORK_AUTHDIR")
	if dir == "" {
		dir = "./auths"
	}
	as, err := auth.LoadWorkBuddyAiDir(dir)
	if err != nil {
		t.Fatalf("加载账号失败 dir=%s: %v", dir, err)
	}
	if len(as) == 0 {
		t.Skipf("目录 %s 下无 workbuddyai-*.json，跳过 live 探针", dir)
	}
	a := as[0]
	t.Logf("使用账号 uid=%s nickname=%s domain=%s file=%s",
		a.UID, a.Nickname, a.Domain, filepath.Base(a.FilePath))
	return a
}

// staticGlobalCap 读静态表里的国际版条目（供对比；静态表在 reasoning 包内不导出，
// 用 Lookup 走「远端为空 + 静态兜底开启」路径拿到等价结果）。
func staticGlobalCap(model string) (reasoning.Cap, bool) {
	return reasoning.Caps.Lookup(reasoning.RealmGlobal, model)
}

// TestLiveProbeEffortCatalog 拉真实目录，与静态表逐条比对。
func TestLiveProbeEffortCatalog(t *testing.T) {
	a := liveAuth(t)
	c := New()

	raws, err := c.fetchCatalog(a)
	if err != nil {
		t.Fatalf("拉取目录失败: %v", err)
	}
	t.Logf("目录返回 %d 个 cli 模型", len(raws))

	// 收集远端下发的档位能力。
	type remoteCap struct {
		model   string
		efforts []string
		def     string
	}
	var remotes []remoteCap
	byModel := make(map[string]remoteCap, len(raws))
	for _, m := range raws {
		eff := m.Reasoning.SupportedEfforts
		def := m.Reasoning.DefaultEffort
		if len(eff) == 0 && def == "" {
			continue
		}
		rc := remoteCap{model: m.ID, efforts: reasoning.SortEfforts(eff), def: strings.ToLower(strings.TrimSpace(def))}
		remotes = append(remotes, rc)
		byModel[strings.ToLower(m.ID)] = rc
	}

	fmt.Printf("\n=== 远端下发了档位能力的模型（%d 个）===\n", len(remotes))
	for _, rc := range remotes {
		fmt.Printf("  %-24s efforts=%v default=%q\n", rc.model, rc.efforts, rc.def)
	}

	// 逐条对比：静态表 vs 远端。
	var onlyRemote, onlyStatic, mismatch []string
	for _, rc := range remotes {
		local, ok := staticGlobalCap(rc.model)
		if !ok {
			onlyRemote = append(onlyRemote, fmt.Sprintf("%s（远端 %v/%q，静态表无此条目）", rc.model, rc.efforts, rc.def))
			continue
		}
		if !sameEfforts(local.Efforts, rc.efforts) || strings.ToLower(local.DefaultEffort) != rc.def {
			mismatch = append(mismatch, fmt.Sprintf("%s\n      远端  = %v / default=%q\n      静态表 = %v / default=%q",
				rc.model, rc.efforts, rc.def, reasoning.SortEfforts(local.Efforts), local.DefaultEffort))
		}
	}
	// 静态表有、远端没下发的条目（远端缺失时静态表生效，值得留意）。
	for _, m := range staticGlobalModels() {
		if _, ok := byModel[strings.ToLower(m)]; !ok {
			local, _ := staticGlobalCap(m)
			onlyStatic = append(onlyStatic, fmt.Sprintf("%s（静态表 %v/%q，远端未下发）",
				m, reasoning.SortEfforts(local.Efforts), local.DefaultEffort))
		}
	}

	fmt.Printf("\n=== 差异清单 ===\n")
	fmt.Printf("① 仅远端有（静态表缺条目）: %d\n", len(onlyRemote))
	for _, s := range onlyRemote {
		fmt.Printf("   - %s\n", s)
	}
	fmt.Printf("② 仅静态表有（远端未下发）: %d\n", len(onlyStatic))
	for _, s := range onlyStatic {
		fmt.Printf("   - %s\n", s)
	}
	fmt.Printf("③ 两侧不一致（远端权威）: %d\n", len(mismatch))
	for _, s := range mismatch {
		fmt.Printf("   - %s\n", s)
	}

	// 重点：deepseek-v4.1-flash 在国际版「只认 high」是静态表的核心断言。
	if rc, ok := byModel["deepseek-v4.1-flash"]; ok {
		fmt.Printf("\n=== 重点核验 deepseek-v4.1-flash ===\n")
		fmt.Printf("  远端   : %v / default=%q\n", rc.efforts, rc.def)
		if local, ok2 := staticGlobalCap("deepseek-v4.1-flash"); ok2 {
			fmt.Printf("  静态表 : %v / default=%q\n", reasoning.SortEfforts(local.Efforts), local.DefaultEffort)
			if sameEfforts(local.Efforts, rc.efforts) {
				fmt.Printf("  结论   : ✅ 一致（静态表「只有 high」成立）\n")
			} else {
				fmt.Printf("  结论   : ❌ 不一致，静态表需更正\n")
			}
		}
	} else {
		fmt.Printf("\n⚠️ 远端未下发 deepseek-v4.1-flash 的档位（该模型走 extraModels 硬编码补入，无上游能力数据）\n")
	}

	if len(onlyRemote) == 0 && len(onlyStatic) == 0 && len(mismatch) == 0 {
		fmt.Printf("\n✅ 静态表与远端完全一致\n")
	}
}

// staticGlobalModels 静态表国际版条目名（用于「仅静态表有」的反向比对）。
// 静态表在 reasoning 包内不导出，这里维护一份**探针专用**的名单；
// 与 catalog.go 的 globalEffortFallback 键集合保持一致（改动时需同步）。
func staticGlobalModels() []string {
	out := []string{
		"fast-model", "balanced-model", "primary-model", "hy4-preview-f", "hy3",
		"deepseek-v4.1-flash", "gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-5.5", "gpt-5.4", "gpt-5.3-codex", "gemini-3.5-flash", "glm-5.3", "glm-5.2",
		"kimi-k3", "kimi-k2.6",
	}
	sort.Strings(out)
	return out
}

// probeModel 一个待探测的模型与其档位用例。
type probeModel struct {
	model   string
	note    string
	efforts []string // 空串代表「不传档位」基线
}

// TestLiveProbeEffortOnWire 用真实对话检验静态表的两条核心断言：
//
//  1. 「国际版只认 high，发 low/max 是非法参数」（catalog.go 注释）——
//     实测各档的 HTTP 状态码即可判定：静默接受（200）还是拒绝（400）。
//  2. 「档位改变思考量」——用 reasoning_content 的**字符数**度量。
//     不用块数：块数取决于上游分块粒度，与档位无关，会引入噪声。
//
// 覆盖三类模型，用以区分「上游整体忽略档位」与「个别模型不敏感」：
//   - deepseek-v4.1-flash：静态表断言「只认 high」，但目录不返回它（走 extraModels）；
//   - hy3 / hy4-preview：目录**明确声明**了档位，是可靠的对照基线；
//     且都在 freeModels 内，零成本。
//
// 绕开 ProjectReasoning：生产路径会按静态表把档位降级，那样永远发不出
// low/max，也就测不出「上游是否真的拒绝」。这里手工构造 body 直发。
//
// 每档多轮（WILDWORK_PROBE_ROUNDS，默认 3）：单次样本的思考量噪声大。
func TestLiveProbeEffortOnWire(t *testing.T) {
	a := liveAuth(t)
	c := New()

	rounds := 3
	if v := os.Getenv("WILDWORK_PROBE_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rounds = n
		}
	}

	targets := []probeModel{
		{"deepseek-v4.1-flash", "静态表断言「只认 high」；目录未下发（extraModels）", []string{"high", "low", "max", "medium", ""}},
		{"hy3", "目录声明 [low high]；max/xhigh 为声明外", []string{"low", "high", "max", "xhigh", ""}},
		{"hy4-preview", "目录声明 [high]；low/max 为声明外", []string{"high", "low", "max", ""}},
	}

	for _, tg := range targets {
		fmt.Printf("\n=== %s（%s，每档 %d 轮）===\n", tg.model, tg.note, rounds)
		fmt.Printf("%-10s %-22s %-24s %-22s\n", "effort", "状态", "reasoning字符(min/avg/max)", "content字符(min/avg/max)")
		for _, effort := range tg.efforts {
			var statuses []string
			var rsnVals, ctVals []int
			var errText string
			for r := 0; r < rounds; r++ {
				obj := map[string]any{
					"model": tg.model,
					"messages": []any{
						map[string]any{"role": "user", "content": probePrompt()},
					},
					"max_tokens": 4096,
					"stream":     true,
				}
				if effort != "" {
					obj["reasoning_effort"] = effort
				}
				body, _ := json.Marshal(obj)
				rc, status, respBody, err := c.ChatStream(a, body)
				if err != nil {
					errText = err.Error()
					break
				}
				if status >= 400 {
					statuses = append(statuses, fmt.Sprintf("%d:%s", status, truncate(string(respBody), 50)))
					continue
				}
				raw, _ := io.ReadAll(io.LimitReader(rc, 4<<20))
				rc.Close()
				st := countStreamFields(raw)
				rsnVals = append(rsnVals, st.RsnChars)
				ctVals = append(ctVals, st.CtChars)
				statuses = append(statuses, strconv.Itoa(status))
			}
			label := effort
			if label == "" {
				label = "(无)"
			}
			if errText != "" {
				fmt.Printf("%-10s %s\n", label, "传输错误: "+errText)
				continue
			}
			fmt.Printf("%-10s %-22s %-24s %-22s\n", label, strings.Join(statuses, ","),
				stats(rsnVals), stats(ctVals))
		}
	}

	fmt.Printf("\n判读要点：\n")
	fmt.Printf("  - 状态全为 200 → 上游静默接受任意档位，「非法参数」的表述不准确。\n")
	fmt.Printf("  - reasoning 字符数随档位单调变化 → 档位真的生效（上游按档位调节思考量）。\n")
	fmt.Printf("  - 若目录已声明档位的模型也不变化 → 上游整体忽略该字段。\n")
}

// stats 输出 min/avg/max。
func stats(vals []int) string {
	if len(vals) == 0 {
		return "(无数据)"
	}
	min, max, sum := vals[0], vals[0], 0
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	return fmt.Sprintf("%d / %.1f / %d", min, float64(sum)/float64(len(vals)), max)
}

// probePrompt 需要多步推理的题（1+1 各档位都不产生思考链，测不出差异）。
func probePrompt() string {
	return "一个水池有甲乙两个进水管。甲管单独注满需 6 小时，乙管单独注满需 4 小时，" +
		"池底有一个排水管，单独排空满池需 12 小时。三管同时开启，注满空池需多少小时？" +
		"请分步推理，最后给出精确到分钟的答案。"
}

// streamStat 流式响应的可度量指标。
// ⚠️ 只统计**字符数**：块数取决于上游分块粒度（同一内容可能分成 10 块或 100 块），
// 用它比较档位会引入与档位无关的噪声。
type streamStat struct {
	RsnChars  int // reasoning_content 总字符
	CtChars   int // content 总字符
	RsnChunks int
}

// countStreamFields 统计原始 SSE 里 reasoning_content / content 的字符数。
func countStreamFields(raw []byte) streamStat {
	var st streamStat
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var env struct {
			Choices []struct {
				Delta map[string]any `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &env) != nil {
			continue
		}
		for _, ch := range env.Choices {
			if v, ok := ch.Delta["reasoning_content"].(string); ok && v != "" {
				st.RsnChars += len([]rune(v))
				st.RsnChunks++
			}
			if v, ok := ch.Delta["content"].(string); ok && v != "" {
				st.CtChars += len([]rune(v))
			}
		}
	}
	return st
}

// sameEfforts 档位集合是否等价（顺序无关，大小写不敏感）。
func sameEfforts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[strings.ToLower(strings.TrimSpace(s))] = true
	}
	for _, s := range b {
		if !seen[strings.ToLower(strings.TrimSpace(s))] {
			return false
		}
	}
	return true
}
