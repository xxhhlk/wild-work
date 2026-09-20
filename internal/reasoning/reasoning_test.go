// 单测覆盖 Buddy2api/reasoning_controls.py 的行为契约（同一批用例移植 + Go 侧补充）。
package reasoning

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustResolve(t *testing.T, payload map[string]any, preferNested bool) Control {
	t.Helper()
	c, err := Resolve(payload, preferNested)
	if err != nil {
		t.Fatalf("Resolve(%v) 意外报错: %v", payload, err)
	}
	return c
}

func mustNormalize(t *testing.T, payload map[string]any, preferNested bool) map[string]any {
	t.Helper()
	body, _, err := NormalizeChat(payload, preferNested)
	if err != nil {
		t.Fatalf("NormalizeChat(%v) 意外报错: %v", payload, err)
	}
	return body
}

// 顶层 reasoning_effort 与 thinking.type 冲突时，强度写法优先（Python: higher_priority_switch）。
func TestEffortOverridesCrossObjectDisableSwitch(t *testing.T) {
	body := mustNormalize(t, map[string]any{
		"reasoning_effort": "high",
		"thinking":         map[string]any{"type": "disabled"},
	}, false)
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", body["reasoning_effort"])
	}
	if _, has := body["thinking"]; has {
		t.Fatalf("thinking 应被移除，实际: %v", body["thinking"])
	}
}

func TestOutputConfigEffortOverridesThinkingDisable(t *testing.T) {
	body := mustNormalize(t, map[string]any{
		"output_config": map[string]any{"effort": "high"},
		"thinking":      map[string]any{"type": "disabled"},
	}, false)
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", body["reasoning_effort"])
	}
	if _, has := body["output_config"]; has {
		t.Fatalf("output_config 仅含 effort，应被整体移除，实际: %v", body["output_config"])
	}
}

// thinking.type 属于原生对象写法，优先级高于 enable_thinking 这类跨方言开关。
func TestNativeSwitchOverridesCrossDialectSwitch(t *testing.T) {
	body := mustNormalize(t, map[string]any{
		"thinking":        map[string]any{"type": "enabled"},
		"enable_thinking": false,
	}, false)
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", body["reasoning_effort"])
	}
	if _, has := body["enable_thinking"]; has {
		t.Fatalf("enable_thinking 应被移除")
	}
}

// 同一原生对象内既给强度又给反向开关 → 400。
func TestRejectsConflictInsideNativeObject(t *testing.T) {
	_, _, err := NormalizeChat(map[string]any{
		"reasoning": map[string]any{"effort": "high", "enabled": false},
	}, false)
	if !IsInvalid(err) {
		t.Fatalf("期望 InvalidError，实际: %v", err)
	}
}

// thinking.type=enabled 与 thinking.budget_tokens=0 同组矛盾 → 400。
func TestRejectsConflictInsideThinkingObject(t *testing.T) {
	_, err := Resolve(map[string]any{
		"thinking": map[string]any{"type": "enabled", "budget_tokens": float64(0)},
	}, false)
	if !IsInvalid(err) {
		t.Fatalf("期望 InvalidError，实际: %v", err)
	}
}

// reasoning_effort=default 视为「未表达」，让位给优先级更低的显式开关。
func TestDefaultEffortDefersToExplicitSwitch(t *testing.T) {
	body := mustNormalize(t, map[string]any{
		"reasoning_effort": "default",
		"thinking":         map[string]any{"type": "disabled"},
	}, false)
	if body["reasoning_effort"] != "none" {
		t.Fatalf("reasoning_effort = %v, want none", body["reasoning_effort"])
	}
}

// 扩展字段与预算保留，且归一化幂等。
func TestNormalizationPreservesExtensionsAndBudgetIdempotently(t *testing.T) {
	payload := map[string]any{
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": float64(4096),
			"display":       "hidden",
		},
		"reasoning": map[string]any{
			"exclude":    true,
			"max_tokens": float64(8192),
		},
	}
	body, control, err := NormalizeChat(payload, false)
	if err != nil {
		t.Fatalf("NormalizeChat: %v", err)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", body["reasoning_effort"])
	}
	if !reflect.DeepEqual(body["thinking"], map[string]any{"budget_tokens": float64(4096), "display": "hidden"}) {
		t.Fatalf("thinking = %v", body["thinking"])
	}
	if !reflect.DeepEqual(body["reasoning"], map[string]any{"exclude": true, "max_tokens": float64(8192)}) {
		t.Fatalf("reasoning = %v", body["reasoning"])
	}
	if control.BudgetTokens == nil || *control.BudgetTokens != 4096 {
		t.Fatalf("budget_tokens = %v, want 4096", control.BudgetTokens)
	}
	again := mustNormalize(t, body, false)
	if !reflect.DeepEqual(again, body) {
		t.Fatalf("二次归一化不幂等:\n first=%v\nsecond=%v", body, again)
	}
	// 归一化不得修改入参
	if _, has := payload["reasoning_effort"]; has {
		t.Fatalf("入参被修改: %v", payload)
	}
	if tm := payload["thinking"].(map[string]any); tm["type"] != "enabled" {
		t.Fatalf("入参 thinking 被修改: %v", tm)
	}
}

// 档位降级：按模型能力表把请求档位压到该模型支持的档位（Catalog.Clamp）。
func TestCatalogClamp(t *testing.T) {
	cases := []struct {
		name   string
		realm  string
		model  string
		effort string
		want   string
	}{
		{"国内版 deepseek-v4-pro 支持 xhigh（无 max）", RealmCN, "deepseek-v4-pro", "max", "xhigh"},
		{"国内版 deepseek-v4-pro 支持 high", RealmCN, "deepseek-v4-pro", "high", "high"},
		{"国内版 deepseek-v4-pro medium 降级到 low", RealmCN, "deepseek-v4-pro", "medium", "low"},
		{"国内版 deepseek-v4-flash 支持 max", RealmCN, "deepseek-v4-flash", "max", "max"},
		{"国内版 deepseek-v4-flash minimal 降级到 low", RealmCN, "deepseek-v4-flash", "minimal", "low"},
		{"国内版 hy3 只认 low/high", RealmCN, "hy3", "ultra", "high"},
		{"国内版 glm-5.1 只认 medium（低于请求档时取最低支持档）", RealmCN, "glm-5.1", "low", "medium"},
		{"国内版 glm-5.2 只认 high/xhigh", RealmCN, "glm-5.2", "low", "high"},
		{"国际版 deepseek-v4.1-flash 只认 high", RealmGlobal, "deepseek-v4.1-flash", "low", "high"},
		{"国际版 deepseek-v4.1-flash 请求 max 也压到 high", RealmGlobal, "deepseek-v4.1-flash", "max", "high"},
		{"国际版 gpt-5.6-luna 支持五档", RealmGlobal, "gpt-5.6-luna", "xhigh", "xhigh"},
		{"大小写与空白不影响命中", RealmCN, " DeepSeek-V4-Pro ", "max", "xhigh"},
		{"未知模型不改档位", RealmCN, "unknown-model", "ultra", "ultra"},
		{"未知档位不改写", RealmCN, "deepseek-v4-pro", "very-high", "very-high"},
		{"关闭类档位不被降级", RealmCN, "deepseek-v4-pro", "none", "none"},
		{"空档位原样返回", RealmCN, "deepseek-v4-pro", "", ""},
	}
	for _, tc := range cases {
		if got := Caps.Clamp(tc.realm, tc.model, tc.effort); got != tc.want {
			t.Errorf("%s: Clamp(%s, %s, %s) = %s, want %s", tc.name, tc.realm, tc.model, tc.effort, got, tc.want)
		}
	}
}

// 远端能力权威：覆盖静态兜底表。
func TestCatalogRemoteOverridesStatic(t *testing.T) {
	defer Caps.SetRemote(RealmCN, map[string]Cap{"deepseek-v4-pro": {Efforts: []string{"low", "high", "xhigh"}, DefaultEffort: "high"}})
	Caps.SetRemote(RealmCN, map[string]Cap{"deepseek-v4-pro": {Efforts: []string{"high", "max"}, DefaultEffort: "max"}})
	if got := Caps.Clamp(RealmCN, "deepseek-v4-pro", "max"); got != "max" {
		t.Fatalf("远端声明支持 max 时应原样保留，实际 %s", got)
	}
	if got := Caps.DefaultEffort(RealmCN, "deepseek-v4-pro"); got != "max" {
		t.Fatalf("远端默认档应生效，实际 %s", got)
	}
	// 空 map 不覆盖（防失败探测清空）
	Caps.SetRemote(RealmCN, nil)
	if got := Caps.Clamp(RealmCN, "deepseek-v4-pro", "max"); got != "max" {
		t.Fatalf("空 map 不应清空既有能力，实际 %s", got)
	}
}

// 静态兜底表开关：关闭后只认远端下发值（不降级、不暴露档位、不补默认档）。
func TestCatalogStaticFallbackSwitch(t *testing.T) {
	if !StaticEffortFallback() {
		t.Fatal("默认应启用静态兜底表")
	}
	defer SetStaticEffortFallback(true)
	cat := NewCatalog()

	if _, ok := cat.Lookup(RealmCN, "deepseek-v4-pro"); !ok {
		t.Fatal("启用时静态表应命中 deepseek-v4-pro")
	}
	SetStaticEffortFallback(false)
	if _, ok := cat.Lookup(RealmCN, "deepseek-v4-pro"); ok {
		t.Fatal("关闭后不应回落到静态表")
	}
	if got := cat.Clamp(RealmCN, "deepseek-v4-pro", "max"); got != "max" {
		t.Fatalf("关闭后档位应原样透传，实际 %s", got)
	}
	if got := cat.DefaultEffort(RealmCN, "deepseek-v4-pro"); got != "" {
		t.Fatalf("关闭后不应补默认档，实际 %q", got)
	}
	if e, d := cat.Listing(RealmCN, "deepseek-v4-pro", nil, ""); len(e) != 0 || d != "" {
		t.Fatalf("关闭后不应暴露档位，实际 %v/%q", e, d)
	}

	// 远端下发值不受开关影响：关闭状态下仍参与降级
	cat.SetRemote(RealmCN, map[string]Cap{"deepseek-v4-pro": {Efforts: []string{"low", "high"}, DefaultEffort: "high"}})
	if !cat.HasRemote(RealmCN) {
		t.Fatal("HasRemote 应报告已有远端能力")
	}
	if cat.HasRemote(RealmGlobal) {
		t.Fatal("未下发过的产品面不应报告 HasRemote")
	}
	if got := cat.Clamp(RealmCN, "deepseek-v4-pro", "max"); got != "high" {
		t.Fatalf("远端能力应参与降级，实际 %s", got)
	}
}

// 默认档：仅「开思考但没给档位」时按模型能力补，不硬编码 high。
func TestCatalogDefaultEffort(t *testing.T) {
	if got := Caps.DefaultEffort(RealmCN, "deepseek-v4-pro"); got != "high" {
		t.Errorf("deepseek-v4-pro 默认档应为 high，实际 %q", got)
	}
	if got := Caps.DefaultEffort(RealmCN, "unknown-model"); got != "" {
		t.Errorf("未知模型不应有默认档，实际 %q", got)
	}
}

// /v1/models 暴露的档位能力：远端优先、静态兜底、皆无则省略。
func TestCatalogListing(t *testing.T) {
	efforts, def := Caps.Listing(RealmCN, "deepseek-v4-flash", nil, "")
	if len(efforts) != 3 || efforts[0] != "low" || def != "" {
		t.Errorf("静态兜底：得到 %v / %q", efforts, def)
	}
	efforts, def = Caps.Listing(RealmCN, "deepseek-v4-flash", []string{"low", "high"}, "high")
	if len(efforts) != 2 || def != "high" {
		t.Errorf("远端优先：得到 %v / %q", efforts, def)
	}
	// 默认档不在档位集合内时不得宣称
	if _, def := Caps.Listing(RealmCN, "deepseek-v4-flash", []string{"low", "high"}, "max"); def != "" {
		t.Errorf("默认档越界应省略，实际 %q", def)
	}
	if efforts, def := Caps.Listing(RealmCN, "unknown-model", nil, ""); efforts != nil || def != "" {
		t.Errorf("未知模型应省略字段，实际 %v / %q", efforts, def)
	}
}

// 面板费率表与 /v1/models 共用同一入口：无档位能力的渠道必须为空。
func TestListingForKind(t *testing.T) {
	if efforts, def := ListingForKind("traework", "deepseek-v4-pro", nil, ""); efforts != nil || def != "" {
		t.Errorf("TraeWork 协议无档位字段，不应透出：%v / %q", efforts, def)
	}
	if efforts, def := ListingForKind("qoder", "glm-5.3", []string{"low", "high", "max"}, "max"); len(efforts) != 3 || def != "max" {
		t.Errorf("Qoder 应取远端 ladder：%v / %q", efforts, def)
	}
	if efforts, _ := ListingForKind("workbuddy", "glm-5.1", nil, ""); len(efforts) != 1 || efforts[0] != "medium" {
		t.Errorf("国内版应回落静态表（glm-5.1 只认 medium）：%v", efforts)
	}
	if efforts, _ := ListingForKind("workbuddyai", "deepseek-v4.1-flash", nil, ""); len(efforts) != 1 || efforts[0] != "high" {
		t.Errorf("国际版静态表应为单档 high：%v", efforts)
	}
}

func TestSupportsEffortKind(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{"workbuddy", true}, {"workbuddyai", true}, {"qoder", true},
		{"WorkBuddy", true}, {" Qoder ", true},
		{"traework", false}, {"", false}, {"unknown", false},
	} {
		if got := SupportsEffortKind(tc.kind); got != tc.want {
			t.Errorf("SupportsEffortKind(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

// 预算换算档位（移植 lingma-proxy 分桶）。
func TestBudgetEffort(t *testing.T) {
	cases := map[float64]string{
		4096: "high", 8000: "high",
		512: "low", 1: "low",
		1024: "medium", 2000: "medium", 4095: "medium", 0: "medium",
	}
	for budget, want := range cases {
		if got := BudgetEffort(budget); got != want {
			t.Errorf("BudgetEffort(%v) = %s, want %s", budget, got, want)
		}
	}
	// 只说开思考 + 给预算：ChatEffort 应走预算分桶而不是中性档
	body := mustNormalize(t, map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 512}}, false)
	c, err := Resolve(body, false)
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if got := ChatEffort(c); got != "low" {
		t.Fatalf("thinking.type=enabled + budget 512 应投影为 low，实际 %s", got)
	}
}

// 关闭思考时不回填默认档（渠道层据此删除 reasoning_effort / thinking 字段）。
func TestDisabledReasoningDropsField(t *testing.T) {
	for _, explicit := range []string{"none", "off", "disable", "disabled"} {
		body := mustNormalize(t, map[string]any{"reasoning_effort": explicit}, false)
		c, _ := Resolve(body, false)
		if c.Mode != ModeDisabled {
			t.Fatalf("%s 应解析为 disabled，实际 %s", explicit, c.Mode)
		}
		if got := ChatEffort(c); got != "none" {
			t.Fatalf("ChatEffort(%s) = %q, want none", explicit, got)
		}
	}
}

// 非 WorkBuddy 方言模型保留标准档位（ChatEffort 不做三档投影）。
func TestChatEffortPreservesStandardLevels(t *testing.T) {
	cases := map[string]string{
		"none":    "none",
		"minimal": "minimal",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"max":     "max",
		"ultra":   "ultra",
	}
	for explicit, want := range cases {
		body := mustNormalize(t, map[string]any{"reasoning_effort": explicit}, false)
		c, _ := Resolve(body, false)
		if got := ChatEffort(c); got != want {
			t.Fatalf("ChatEffort(%s) = %s, want %s", explicit, got, want)
		}
	}
}

func TestEnabledThinkingMapsToHigh(t *testing.T) {
	for _, typ := range []string{"enabled", "adaptive"} {
		body := mustNormalize(t, map[string]any{"thinking": map[string]any{"type": typ}}, false)
		if body["reasoning_effort"] != "high" {
			t.Fatalf("thinking.type=%s → reasoning_effort=%v, want high", typ, body["reasoning_effort"])
		}
	}
}

// thinking.type=disabled 归一化为 reasoning_effort=none（标准 Chat 方言）；
// WorkBuddy 侧再用「不传字段」表达关闭，见 TestDisabledReasoningDropsField。
func TestDisabledThinkingNormalizesToNone(t *testing.T) {
	body := mustNormalize(t, map[string]any{"thinking": map[string]any{"type": "disabled"}}, false)
	if _, has := body["thinking"]; has {
		t.Fatalf("thinking 应被移除: %v", body)
	}
	if body["reasoning_effort"] != "none" {
		t.Fatalf("reasoning_effort = %v, want none", body["reasoning_effort"])
	}
}

// 兼容字段全量：各写法都能命中同一控制量。
func TestCompatibleFieldForms(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"reasoning_effort", map[string]any{"reasoning_effort": "high"}, "high"},
		{"reasoningEffort", map[string]any{"reasoningEffort": "high"}, "high"},
		{"reasoning.effort", map[string]any{"reasoning": map[string]any{"effort": "high"}}, "high"},
		{"output_config.effort", map[string]any{"output_config": map[string]any{"effort": "high"}}, "high"},
		{"thinking.effort", map[string]any{"thinking": map[string]any{"effort": "high"}}, "high"},
		{"thinking.type", map[string]any{"thinking": map[string]any{"type": "enabled"}}, "high"},
		{"enable_thinking", map[string]any{"enable_thinking": true}, "high"},
		{"think", map[string]any{"think": true}, "high"},
		{"enable_thinking=1", map[string]any{"enable_thinking": float64(1)}, "high"},
		{"reasoning.enabled", map[string]any{"reasoning": map[string]any{"enabled": true}}, "high"},
		{"reasoning.budget_tokens 2048 → medium（预算分桶）", map[string]any{"reasoning": map[string]any{"budget_tokens": float64(2048)}}, "medium"},
		{"x-high", map[string]any{"reasoning_effort": "x-high"}, "xhigh"},
		{"extra_high", map[string]any{"reasoning_effort": "extra_high"}, "xhigh"},
		{"大写+空格", map[string]any{"reasoning_effort": "  HIGH  "}, "high"},
		{"reasoning 标量", map[string]any{"reasoning": "low"}, "low"},
		{"thinking 标量", map[string]any{"thinking": "low"}, "low"},
	}
	for _, tc := range cases {
		body := mustNormalize(t, tc.payload, false)
		if body["reasoning_effort"] != tc.want {
			t.Fatalf("%s: reasoning_effort = %v, want %v", tc.name, body["reasoning_effort"], tc.want)
		}
	}
}

func TestDisableForms(t *testing.T) {
	cases := []map[string]any{
		{"disable_reasoning": true},
		{"disable_reasoning": float64(1)},
		{"enable_thinking": false},
		{"enable_thinking": float64(0)},
		{"thinking": map[string]any{"budget_tokens": float64(0)}},
	}
	for _, payload := range cases {
		body := mustNormalize(t, payload, false)
		if body["reasoning_effort"] != "none" {
			t.Fatalf("%v → reasoning_effort = %v, want none", payload, body["reasoning_effort"])
		}
	}
	// disable_reasoning=false / 0 属于「未表达」
	for _, v := range []any{false, float64(0)} {
		body := mustNormalize(t, map[string]any{"disable_reasoning": v}, false)
		if _, has := body["reasoning_effort"]; has {
			t.Fatalf("disable_reasoning=%v 不应产生控制: %v", v, body)
		}
	}
}

func TestInvalidValues(t *testing.T) {
	cases := []map[string]any{
		{"reasoning_effort": "very-high"},
		{"reasoning_effort": ""},
		{"reasoning_effort": float64(3)},
		{"reasoning_effort": map[string]any{}},
		{"thinking": map[string]any{"type": "maybe"}},
		{"thinking": map[string]any{"type": float64(1)}},
		{"thinking": map[string]any{"budget_tokens": -1.0}},
		{"thinking": map[string]any{"budget_tokens": true}},
		{"enable_thinking": "sometimes"},
		{"disable_reasoning": "yes"},
		{"reasoning": map[string]any{"enabled": "sometimes"}},
	}
	for _, payload := range cases {
		if _, err := Resolve(payload, false); !IsInvalid(err) {
			t.Fatalf("%v 期望 InvalidError，实际: %v", payload, err)
		}
	}
}

// preferNested：Responses 用嵌套 reasoning.effort，Chat 用顶层 reasoning_effort。
func TestPreferNestedOrdering(t *testing.T) {
	payload := map[string]any{
		"reasoning_effort": "low",
		"reasoning":        map[string]any{"effort": "max"},
	}
	if got := mustResolve(t, payload, false).Effort; got != "low" {
		t.Fatalf("preferNested=false 应取顶层，实际 %s", got)
	}
	if got := mustResolve(t, payload, true).Effort; got != "max" {
		t.Fatalf("preferNested=true 应取嵌套，实际 %s", got)
	}
}

func TestEmptyPayloadIsDefault(t *testing.T) {
	if c := mustResolve(t, nil, false); !c.IsDefault() {
		t.Fatalf("nil payload 应为 default，实际 %s", c.Mode)
	}
	if c := mustResolve(t, map[string]any{"messages": []any{}}, false); !c.IsDefault() {
		t.Fatalf("无控制字段应为 default，实际 %s", c.Mode)
	}
	if v, ok := Default.Enabled(); ok {
		t.Fatalf("default 的 Enabled 应为未表达，实际 (%v,%v)", v, ok)
	}
}

func TestParseDefault(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"off":     "",
		"none":    "",
		"default": "",
		"low":     "low",
		"HIGH":    "high",
		"max":     "max",
		"ultra":   "ultra",
		"enabled": "high",
		"xhigh":   "xhigh",
	}
	for in, want := range cases {
		got, err := ParseDefault(in)
		if err != nil {
			t.Fatalf("ParseDefault(%q) 报错: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseDefault(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseDefault("very-high"); err == nil {
		t.Fatalf("非法档位应报错")
	}
}

// JSON 往返：确保从网络解出的 float64 数值形态与手写 map 行为一致。
func TestJSONRoundTrip(t *testing.T) {
	raw := []byte(`{"reasoning":{"effort":"high","summary":"auto"},"thinking":{"type":"enabled","budget_tokens":4096}}`)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body := mustNormalize(t, payload, true)
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", body["reasoning_effort"])
	}
	if body["reasoning_summary"] != "auto" {
		t.Fatalf("summary 应提升为顶层 reasoning_summary: %v", body["reasoning_summary"])
	}
	if _, has := body["reasoning"]; has {
		t.Fatalf("reasoning 仅剩 effort/summary，应被移除: %v", body["reasoning"])
	}
}

// ---------------------------------------------------------------------------
// 思考摘要下发策略（compat.responses_reasoning_summary）
// ---------------------------------------------------------------------------

func TestParseSummaryMode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", SummaryAuto, false},
		{"auto", SummaryAuto, false},
		{"AUTO", SummaryAuto, false},
		{" auto ", SummaryAuto, false},
		{"on", SummaryOn, false},
		{"always", SummaryOn, false},
		{"off", SummaryOff, false},
		{"OFF", SummaryOff, false},
		{"never", SummaryOff, false},
		{"bogus", "", true},
	}
	for _, tc := range cases {
		got, err := ParseSummaryMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseSummaryMode(%q) 应报错，实际 %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSummaryMode(%q) 意外报错: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSummaryMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWantsSummary(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"未表达", `{"model":"gpt-5","input":"hi"}`, false},
		{"Codex 风格 summary=auto", `{"reasoning":{"effort":"high","summary":"auto"}}`, true},
		{"summary=concise", `{"reasoning":{"summary":"concise"}}`, true},
		{"summary=none", `{"reasoning":{"effort":"high","summary":"none"}}`, false},
		{"summary=off", `{"reasoning":{"summary":"off"}}`, false},
		{"summary=false", `{"reasoning":{"summary":false}}`, false},
		{"summary=null", `{"reasoning":{"summary":null}}`, false},
		{"仅 effort 无 summary", `{"reasoning":{"effort":"high"}}`, false},
		{"顶层 reasoning_summary", `{"reasoning_summary":"auto"}`, true},
		{"include 索要加密思考", `{"include":["reasoning.encrypted_content"]}`, true},
		{"include 其他项", `{"include":["message.output_text.logprobs"]}`, false},
		{"thinking 对象（Anthropic 混用）", `{"thinking":{"type":"enabled"}}`, true},
		{"thinking 空对象", `{"thinking":{}}`, false},
	}
	for _, tc := range cases {
		var payload map[string]any
		if err := json.Unmarshal([]byte(tc.payload), &payload); err != nil {
			t.Fatalf("%s: 解析测试载荷失败: %v", tc.name, err)
		}
		if got := WantsSummary(payload); got != tc.want {
			t.Errorf("%s: WantsSummary(%s) = %v, want %v", tc.name, tc.payload, got, tc.want)
		}
	}
}
