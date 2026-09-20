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
//  2. 本文件按 realm 分开的静态兜底表（远端缺失时补齐；可被
//     compat.static_effort_fallback 关闭，关闭后只剩远端一级）；
//  3. 两者皆无 → 不降级（档位原样透传），/v1/models 也不暴露档位字段。
//
// Qoder（RealmQoder）是特例：能力一律来自其模型目录的 thinking_config
// （每模型一条 ladder，见 internal/qoder/models.go），静态表刻意留空 —— 猜错
// ladder 会把「未知」当「已知」而发出非法档位。
//
// ⚠️ 静态表数值来自对官方客户端（codebuddy.js / product.ts）的逆向记录，
// 本仓库未独立复现；远端返回值始终优先，实测不符时只改本文件即可。
package reasoning

import (
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Realm 上游产品面。同一模型在不同面档位可能不同，**绝不混用**。
const (
	// RealmCN WorkBuddy 国内版（CodeBuddy）。
	RealmCN = "cn"
	// RealmGlobal WorkBuddy 国际版（www.workbuddy.ai）。
	RealmGlobal = "global"
	// RealmQoder Qoder（model.chat / agent_chat_generation）。
	// 与 WorkBuddy 的档位表**完全独立**：Qoder 的档位来自上游模型目录的
	// thinking_config（每模型一条 ladder），且不少模型只支持关闭节点而不支持
	// 档位、或反之，与 CodeBuddy 的同名模型并不通用。
	RealmQoder = "qoder"
)

// Cap 一个模型的档位能力。
type Cap struct {
	// Efforts 可枚举档位；空表示未知（未知不做降级、不暴露）。
	Efforts []string
	// DefaultEffort 上游声明的默认档；可能为空。
	DefaultEffort string
	// SupportsDisable 上游声明该模型可显式关闭思考（Qoder 的
	// thinking_config.disabled 节点）。false 表示未知或不可关闭——此时
	// 「客户端要求关闭」不应被当成可满足的请求（详见 qoder 渠道的投影）。
	SupportsDisable bool
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

// qoderEffortFallback Qoder 的静态兜底表**刻意留空**：
// 它的档位能力一律来自上游模型目录的 thinking_config（远端权威），
// 本地猜一条 ladder 反而会把「未知」当成「已知」而降级错档位。
var qoderEffortFallback = map[string]Cap{}

// staticFallback 静态兜底表开关。默认开启；由 main.go 按配置
// compat.static_effort_fallback 覆盖，面板可热更新（排障与实测对比用）。
// 关闭后档位能力只认远端目录下发值，未下发的模型一律不降级。
var staticFallback = func() *atomic.Bool {
	b := &atomic.Bool{}
	b.Store(true)
	return b
}()

// SetStaticEffortFallback 设置静态兜底表开关（启动时与面板保存时调用）。
func SetStaticEffortFallback(on bool) { staticFallback.Store(on) }

// StaticEffortFallback 当前是否启用静态兜底表。
func StaticEffortFallback() bool { return staticFallback.Load() }

// 静态表可观测性：每个 (realm, model) 只记一次，供按实测逐条收敛用。
var (
	staticUseLogged      sync.Map // 走静态兜底的模型
	staticConflictLogged sync.Map // 远端与静态表不一致的模型
)

func logStaticUse(realm, model string, cap Cap) {
	if _, loaded := staticUseLogged.LoadOrStore(realm+"/"+model, struct{}{}); loaded {
		return
	}
	log.Printf("[reasoning] 档位能力走静态兜底 realm=%s model=%s efforts=%v default=%q（远端目录未下发该模型）",
		realm, model, cap.Efforts, cap.DefaultEffort)
}

func logStaticConflict(realm, model string, remote, local Cap) {
	if _, loaded := staticConflictLogged.LoadOrStore(realm+"/"+model, struct{}{}); loaded {
		return
	}
	log.Printf("[reasoning] 远端档位与静态表不一致（以远端为准）realm=%s model=%s remote=%v/%q static=%v/%q",
		realm, model, remote.Efforts, remote.DefaultEffort, local.Efforts, local.DefaultEffort)
}

// sameCap 两份档位能力是否等价（顺序无关；默认档与可关闭标记一并比较）。
func sameCap(a, b Cap) bool {
	if a.DefaultEffort != b.DefaultEffort || a.SupportsDisable != b.SupportsDisable {
		return false
	}
	if len(a.Efforts) != len(b.Efforts) {
		return false
	}
	seen := make(map[string]bool, len(a.Efforts))
	for _, e := range a.Efforts {
		seen[normalizeModel(e)] = true
	}
	for _, e := range b.Efforts {
		if !seen[normalizeModel(e)] {
			return false
		}
	}
	return true
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
	switch {
	case strings.EqualFold(strings.TrimSpace(realm), RealmGlobal):
		return RealmGlobal
	case strings.EqualFold(strings.TrimSpace(realm), RealmQoder):
		return RealmQoder
	}
	return RealmCN
}

// normalizeModel 归一化模型名（大小写与空白差异不应导致漏查）。
func normalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// staticCap 取静态兜底条目。Qoder 无静态表（能力只认上游目录）。
func staticCap(realm, model string) Cap {
	switch normalizeRealm(realm) {
	case RealmGlobal:
		return globalEffortFallback[normalizeModel(model)]
	case RealmQoder:
		return qoderEffortFallback[normalizeModel(model)]
	}
	return cnEffortFallback[normalizeModel(model)]
}

// SetRemote 用远端目录返回的档位能力覆盖某产品面的缓存。
// 空 map 不覆盖（防一次失败探测清空既有能力）。
// 「无档位但有 disabled 节点」的模型（Qoder 的 qwen3.7-max 这类）也要保留：
// 它虽不支持多档，但支持显式关闭，投影时需要这个信息。
func (c *Catalog) SetRemote(realm string, caps map[string]Cap) {
	if c == nil || len(caps) == 0 {
		return
	}
	bucket := make(map[string]Cap, len(caps))
	for model, cap := range caps {
		if len(cap.Efforts) == 0 && cap.DefaultEffort == "" && !cap.SupportsDisable {
			continue
		}
		if len(cap.Efforts) > 0 {
			cap.Efforts = SortEfforts(cap.Efforts)
		}
		cap.DefaultEffort = normalizeModel(cap.DefaultEffort)
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

// Lookup 三级查找：远端 → 静态兜底（开关关闭时跳过）。第二个返回值为 false 表示未知。
func (c *Catalog) Lookup(realm, model string) (Cap, bool) {
	key := normalizeModel(model)
	if key == "" {
		return Cap{}, false
	}
	r := normalizeRealm(realm)
	known := func(cap Cap) bool { return len(cap.Efforts) > 0 || cap.SupportsDisable }
	local := staticCap(r, key)
	if c != nil {
		c.mu.RLock()
		bucket, ok := c.remote[r]
		var remote Cap
		hasRemote := false
		if ok {
			remote, hasRemote = bucket[key]
			hasRemote = hasRemote && known(remote)
		}
		c.mu.RUnlock()
		if hasRemote {
			if known(local) && !sameCap(remote, local) {
				logStaticConflict(r, key, remote, local)
			}
			return remote, true
		}
	}
	if StaticEffortFallback() && known(local) {
		logStaticUse(r, key, local)
		return local, true
	}
	return Cap{}, false
}

// HasRemote 该产品面是否已有远端下发的档位能力。
// 供请求路径判断要不要预热目录（远端一旦成功下发即长期保留，故通常只在
// 进程内首次请求时触发一次）。
func (c *Catalog) HasRemote(realm string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.remote[normalizeRealm(realm)]) > 0
}

// SortEfforts 按强度升序排列档位（未知档位排在已知档位之后，保持原有相对顺序）。
// 上游返回的 efforts 是 JSON 对象（键序在 Go map 里丢失），投影与
// /v1/models 都需要稳定的顺序，否则同一模型每次输出顺序可能不同。
func SortEfforts(efforts []string) []string {
	if len(efforts) <= 1 {
		return append([]string(nil), efforts...)
	}
	out := append([]string(nil), efforts...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := effortRank[normalizeModel(out[i])]
		rj, okj := effortRank[normalizeModel(out[j])]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true // 已知档位排在未知之前
		case okj:
			return false
		}
		return false
	})
	return out
}

// LowestEffort 返回档位集合里最低的一档（空集合返回空串）。
// 用于「客户端要求关闭、但该模型不支持关闭」时降到最低档而非发出非法档位。
func LowestEffort(efforts []string) string {
	sorted := SortEfforts(efforts)
	if len(sorted) == 0 {
		return ""
	}
	return normalizeModel(sorted[0])
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

// RealmForKind 把渠道名映射成产品面（三个渠道有档位能力）。
// 放在 reasoning 包是为了让渠道层与模型列表共用同一映射，避免两处漂移。
// Qoder 与 WorkBuddy 的档位表互不相通（同名模型 ladder 不同），故单独一个面。
func RealmForKind(kind string) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(kind), "workbuddyai"):
		return RealmGlobal
	case strings.EqualFold(strings.TrimSpace(kind), "qoder"):
		return RealmQoder
	}
	return RealmCN
}

// SupportsEffortKind 该渠道是否有可验证的思考档位能力（WorkBuddy 双面 + Qoder）。
// TraeWork 协议没有档位字段，对它声明档位会让客户端发出上游不认的参数。
func SupportsEffortKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "workbuddy", "workbuddyai", "qoder":
		return true
	}
	return false
}

// ListingForKind 按渠道取「对外声明的档位」（无档位能力的渠道返回空）。
// 请求投影与模型列表（/v1/models、面板费率表）共用这一入口，避免两处声明漂移。
func ListingForKind(kind, model string, remoteEfforts []string, remoteDefault string) ([]string, string) {
	if !SupportsEffortKind(kind) {
		return nil, ""
	}
	return Caps.Listing(RealmForKind(kind), model, remoteEfforts, remoteDefault)
}
