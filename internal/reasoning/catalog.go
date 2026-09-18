// catalog.go 模型思考档位能力表（per-model × realm）与「就近降级」。
//
// 背景：上游对不同模型只接受特定档位。例如国内版 deepseek-v4-pro 只认
// low/high/xhigh（**没有 max**），国际版 deepseek-v4.1-flash 只认 high，
// glm-5.1 / kimi-* / minimax-m3 只认 medium。客户端发别的档位会被上游拒绝
// （非法参数）或静默忽略，此前 wild-work 只对 2 个模型做固定三档压缩、
// 其余模型把标准档位原样透传，因此存在真实错配。
//
// 数据来源三级（语义对齐 workbuddy2api/internal/upstream/effort_catalog.go）：
//  1. 远端目录接口返回的 reasoning.supportedEfforts / defaultEffort（权威，优先）；
//  2. 本文件按 realm 分开的静态兜底表（远端缺失时补齐）；
//  3. 两者皆无 → 不降级（档位原样透传），/v1/models 也不暴露档位字段。
//
// ⚠️ 静态表数值来自对官方客户端（codebuddy.js / product.ts）的逆向记录，
// 本仓库未独立复现；远端返回值始终优先，实测不符时只改本文件即可。
package reasoning

import (
	"strings"
	"sync"
)

// Realm 上游产品面。同一模型在不同面档位可能不同，**绝不混用**。
const (
	// RealmCN WorkBuddy 国内版（CodeBuddy）。
	RealmCN = "cn"
	// RealmGlobal WorkBuddy 国际版（www.workbuddy.ai）。
	RealmGlobal = "global"
)

// Cap 一个模型的档位能力。
type Cap struct {
	// Efforts 可枚举档位；空表示未知（未知不做降级、不暴露）。
	Efforts []string
	// DefaultEffort 上游声明的默认档；可能为空。
	DefaultEffort string
}

// effortRank 档位从低到高。ultra 是客户端可能发的最强档（上游无此值，
// 降级时会落到该模型支持的最高档）。
var effortRank = map[string]int{
	"off": 0, "none": 0, "minimal": 1, "low": 2, "medium": 3,
	"high": 4, "xhigh": 5, "max": 6, "ultra": 7,
}

// cnEffortFallback 国内版静态兜底表。
var cnEffortFallback = map[string]Cap{
	"deepseek-v4-flash":   {Efforts: []string{"low", "high", "max"}},
	"deepseek-v4.1-flash": {Efforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	"deepseek-v4-pro":     {Efforts: []string{"low", "high", "xhigh"}, DefaultEffort: "high"},
	"hy4-preview":         {Efforts: []string{"high"}, DefaultEffort: "high"},
	"hy4-preview-x":       {Efforts: []string{"high"}},
	"hy3":                 {Efforts: []string{"low", "high"}, DefaultEffort: "high"},
	"hy3-x":               {Efforts: []string{"low", "high"}, DefaultEffort: "high"},
	"glm-5.3":             {Efforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	"glm-5.3-flash":       {Efforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	"glm-5.2":             {Efforts: []string{"high", "xhigh"}, DefaultEffort: "high"},
	"glm-5.1":             {Efforts: []string{"medium"}},
	"glm-5v-turbo":        {Efforts: []string{"medium"}},
	"kimi-k3-1":           {Efforts: []string{"medium"}},
	"kimi-k2.7":           {Efforts: []string{"medium"}},
	"kimi-k2.6":           {Efforts: []string{"medium"}},
	"minimax-m3":          {Efforts: []string{"medium"}},
}

// globalEffortFallback 国际版静态兜底表。
// 注意 deepseek-v4.1-flash 在国际版**只有 high**，与国内版三档刻意不同：
// 往国际版上游发 low/max 是非法参数。
var globalEffortFallback = map[string]Cap{
	"fast-model":          {Efforts: []string{"medium"}},
	"balanced-model":      {Efforts: []string{"medium"}},
	"primary-model":       {Efforts: []string{"high"}},
	"hy4-preview-f":       {Efforts: []string{"high"}, DefaultEffort: "high"},
	"hy3":                 {Efforts: []string{"low", "high"}, DefaultEffort: "high"},
	"deepseek-v4.1-flash": {Efforts: []string{"high"}},
	"gpt-6-astra":         {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultEffort: "high"},
	"gpt-5.6-sol":         {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultEffort: "high"},
	"gpt-5.6-terra":       {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultEffort: "high"},
	"gpt-5.6-luna":        {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultEffort: "high"},
	"gpt-5.5":             {Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high"},
	"gpt-5.4":             {Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high"},
	"gpt-5.3-codex":       {Efforts: []string{"medium"}},
	"gemini-3.5-flash":    {Efforts: []string{"medium"}},
	"glm-5.3":             {Efforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	"glm-5.2":             {Efforts: []string{"high", "xhigh"}, DefaultEffort: "high"},
	"kimi-k3":             {Efforts: []string{"medium"}},
	"kimi-k2.6":           {Efforts: []string{"medium"}},
}

// Catalog 档位能力表：静态兜底 + 远端覆盖（并发安全）。
type Catalog struct {
	mu     sync.RWMutex
	remote map[string]map[string]Cap // realm → 归一化模型名 → Cap
}

// NewCatalog 构造空目录（仅静态兜底生效）。
func NewCatalog() *Catalog {
	return &Catalog{remote: map[string]map[string]Cap{}}
}

// Caps 进程级共享目录：渠道拉取模型目录时写入，投影时读取。
var Caps = NewCatalog()

// normalizeRealm 归一化产品面：空/未知一律按国内版处理（渠道缺省）。
func normalizeRealm(realm string) string {
	if strings.EqualFold(strings.TrimSpace(realm), RealmGlobal) {
		return RealmGlobal
	}
	return RealmCN
}

// normalizeModel 归一化模型名（大小写与空白差异不应导致漏查）。
func normalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// staticCap 取静态兜底条目。
func staticCap(realm, model string) Cap {
	table := cnEffortFallback
	if normalizeRealm(realm) == RealmGlobal {
		table = globalEffortFallback
	}
	return table[normalizeModel(model)]
}

// SetRemote 用远端目录返回的档位能力覆盖某产品面的缓存。
// 空 map 不覆盖（防一次失败探测清空既有能力）。
func (c *Catalog) SetRemote(realm string, caps map[string]Cap) {
	if c == nil || len(caps) == 0 {
		return
	}
	bucket := make(map[string]Cap, len(caps))
	for model, cap := range caps {
		if len(cap.Efforts) == 0 && cap.DefaultEffort == "" {
			continue
		}
		bucket[normalizeModel(model)] = cap
	}
	if len(bucket) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remote == nil {
		c.remote = map[string]map[string]Cap{}
	}
	c.remote[normalizeRealm(realm)] = bucket
}

// Lookup 三级查找：远端 → 静态兜底。第二个返回值为 false 表示未知。
func (c *Catalog) Lookup(realm, model string) (Cap, bool) {
	key := normalizeModel(model)
	if key == "" {
		return Cap{}, false
	}
	r := normalizeRealm(realm)
	if c != nil {
		c.mu.RLock()
		if bucket, ok := c.remote[r]; ok {
			if cap, ok := bucket[key]; ok && len(cap.Efforts) > 0 {
				c.mu.RUnlock()
				return cap, true
			}
		}
		c.mu.RUnlock()
	}
	if cap := staticCap(r, key); len(cap.Efforts) > 0 {
		return cap, true
	}
	return Cap{}, false
}

// Clamp 把请求档位降级到该模型支持的档位：
//   - 支持该档 → 原样返回；
//   - 不支持 → 取「≤ 请求档位」里最高的支持档（不超发强度）；
//   - 支持档全部高于请求档 → 取最低支持档（偏离最小）；
//   - 关闭类档位（off/none）、未知模型、未知档位 → 原样返回（不做降级）。
func (c *Catalog) Clamp(realm, model, effort string) string {
	want := normalizeModel(effort)
	if want == "" {
		return effort
	}
	wantIdx, known := effortRank[want]
	if !known || wantIdx == 0 {
		return effort // 未知档位或「关闭」：交调用方按原语义处理
	}
	cap, ok := c.Lookup(realm, model)
	if !ok {
		return effort
	}
	best, bestIdx := "", -1
	for _, s := range cap.Efforts {
		idx, k := effortRank[normalizeModel(s)]
		if k && idx <= wantIdx && idx > bestIdx {
			best, bestIdx = normalizeModel(s), idx
		}
	}
	if best != "" {
		return best
	}
	lowest, lowestIdx := "", 1<<30
	for _, s := range cap.Efforts {
		idx, k := effortRank[normalizeModel(s)]
		if k && idx < lowestIdx {
			lowest, lowestIdx = normalizeModel(s), idx
		}
	}
	if lowest != "" {
		return lowest
	}
	return effort
}

// DefaultEffort 返回该模型声明的默认档（未命中返回空串）。
// 用于「客户端只说开思考、没给档位」时补一个该模型认的档，而不是硬编码 high。
func (c *Catalog) DefaultEffort(realm, model string) string {
	cap, ok := c.Lookup(realm, model)
	if !ok {
		return ""
	}
	if cap.DefaultEffort != "" {
		return normalizeModel(cap.DefaultEffort)
	}
	return ""
}

// Listing 计算某模型在 /v1/models 应暴露的档位能力：
// 远端值非空时以其为权威，否则回落静态兜底表；两者皆无 → 返回 nil（调用方省略字段）。
// 默认档仅在「命中档位集合」时才返回（不宣称不支持的默认档）。
func (c *Catalog) Listing(realm, model string, remoteEfforts []string, remoteDefault string) (efforts []string, defaultEffort string) {
	if len(remoteEfforts) > 0 {
		efforts = append([]string(nil), remoteEfforts...)
		if containsEffort(efforts, remoteDefault) {
			defaultEffort = normalizeModel(remoteDefault)
		}
		return efforts, defaultEffort
	}
	cap, ok := c.Lookup(realm, model)
	if !ok {
		return nil, ""
	}
	efforts = append([]string(nil), cap.Efforts...)
	if containsEffort(efforts, cap.DefaultEffort) {
		defaultEffort = normalizeModel(cap.DefaultEffort)
	}
	return efforts, defaultEffort
}

// containsEffort 档位成员判定（精确匹配）。
func containsEffort(efforts []string, want string) bool {
	want = normalizeModel(want)
	if want == "" {
		return false
	}
	for _, e := range efforts {
		if normalizeModel(e) == want {
			return true
		}
	}
	return false
}

// RealmForKind 把渠道名映射成产品面（仅两个 WorkBuddy 渠道有档位能力）。
// 放在 reasoning 包是为了让渠道层与模型列表共用同一映射，避免两处漂移。
func RealmForKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "workbuddyai") {
		return RealmGlobal
	}
	return RealmCN
}
