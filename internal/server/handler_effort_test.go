// handler_effort_test.go /v1/models 的思考档位能力透出 + 远端能力表写入。
package server

import (
	"testing"

	"wild-work/internal/provider"
	"wild-work/internal/reasoning"
)

// 远端声明优先：目录接口返回的档位能力直接透出。
func TestModelEntryReasoningEffortsRemote(t *testing.T) {
	mi := provider.ModelInfo{
		ID: "hy3", SupportsReasoning: true,
		SupportedEfforts: []string{"low", "high"}, DefaultEffort: "high",
	}
	entry := buildModelEntry(provider.WorkBuddy, mi)
	efforts, ok := entry["reasoning_supported_efforts"].([]string)
	if !ok || len(efforts) != 2 || efforts[1] != "high" {
		t.Fatalf("档位应透出远端值，实际 %v", entry["reasoning_supported_efforts"])
	}
	if entry["reasoning_default_effort"] != "high" {
		t.Errorf("默认档应透出，实际 %v", entry["reasoning_default_effort"])
	}
	if entry["supports_reasoning"] != true {
		t.Errorf("应透出 supports_reasoning，实际 %v", entry["supports_reasoning"])
	}
}

// 远端未声明时回落静态兜底表（deepseek-v4-flash 国内版三档）。
func TestModelEntryReasoningEffortsStaticFallback(t *testing.T) {
	entry := buildModelEntry(provider.WorkBuddy, provider.ModelInfo{ID: "deepseek-v4-flash"})
	efforts, ok := entry["reasoning_supported_efforts"].([]string)
	if !ok || len(efforts) != 3 {
		t.Fatalf("应回落静态表三档，实际 %v", entry["reasoning_supported_efforts"])
	}
	// 国际版同一模型档位不同（deepseek-v4.1-flash 只认 high）
	entry = buildModelEntry(provider.WorkBuddyAI, provider.ModelInfo{ID: "deepseek-v4.1-flash"})
	efforts, ok = entry["reasoning_supported_efforts"].([]string)
	if !ok || len(efforts) != 1 || efforts[0] != "high" {
		t.Fatalf("国际版应为单档 high，实际 %v", entry["reasoning_supported_efforts"])
	}
}

// 未收录模型 / 无档位能力渠道：省略档位字段（不输出空数组）。
func TestModelEntryReasoningEffortsOmitted(t *testing.T) {
	entry := buildModelEntry(provider.WorkBuddy, provider.ModelInfo{ID: "unknown-model"})
	if _, has := entry["reasoning_supported_efforts"]; has {
		t.Errorf("未收录模型应省略档位字段，实际 %v", entry["reasoning_supported_efforts"])
	}
	entry = buildModelEntry(provider.TraeWork, provider.ModelInfo{ID: "glm-5.2"})
	if _, has := entry["reasoning_supported_efforts"]; has {
		t.Errorf("TraeWork 无思考档位能力，不应透出，实际 %v", entry["reasoning_supported_efforts"])
	}
}

// Qoder 的档位来自上游目录的 thinking_config：透出远端值，
// 且不与 WorkBuddy 的同名模型互相污染（realm 独立）。
func TestModelEntryReasoningEffortsQoder(t *testing.T) {
	mi := provider.ModelInfo{
		ID: "qwen3.8-flash", SupportsReasoning: true,
		SupportedEfforts: []string{"low", "medium", "xhigh"}, DefaultEffort: "medium",
	}
	entry := buildModelEntry(provider.Qoder, mi)
	efforts, ok := entry["reasoning_supported_efforts"].([]string)
	if !ok || len(efforts) != 3 || efforts[1] != "medium" {
		t.Fatalf("Qoder 档位应透出远端值，实际 %v", entry["reasoning_supported_efforts"])
	}
	if entry["reasoning_default_effort"] != "medium" {
		t.Errorf("Qoder 默认档应透出，实际 %v", entry["reasoning_default_effort"])
	}
	// 目录拉取后能力表应写入 Qoder 面（投影与 /v1/models 共用同一份表）。
	publishEffortCaps(provider.Qoder, []provider.ModelInfo{mi})
	if got := reasoning.Caps.Clamp(reasoning.RealmQoder, "qwen3.8-flash", "ultra"); got != "xhigh" {
		t.Errorf("Qoder 面降级应为 xhigh，实际 %s", got)
	}
	if got := reasoning.Caps.DefaultEffort(reasoning.RealmQoder, "qwen3.8-flash"); got != "medium" {
		t.Errorf("Qoder 默认档应为 medium，实际 %s", got)
	}
	// 同名模型在 WorkBuddy 面走自己的表，不受 Qoder 目录影响。
	if got := reasoning.Caps.Clamp(reasoning.RealmCN, "qwen3.8-flash", "ultra"); got != "ultra" {
		t.Errorf("国内面未被 Qoder 目录污染时应原样透传，实际 %s", got)
	}
}

// 目录拉取后能力表应更新（远端权威），且非 WorkBuddy 渠道不写入。
func TestPublishEffortCaps(t *testing.T) {
	infos := []provider.ModelInfo{
		{ID: "deepseek-v4-flash", SupportedEfforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	}
	publishEffortCaps(provider.TraeWork, infos) // 不应写入
	if got := reasoning.Caps.Clamp(reasoning.RealmCN, "deepseek-v4-flash", "ultra"); got != "max" {
		t.Fatalf("TraeWork 不应影响档位表，实际 %s", got)
	}
	publishEffortCaps(provider.WorkBuddy, infos)
	if got := reasoning.Caps.Clamp(reasoning.RealmCN, "deepseek-v4-flash", "ultra"); got != "max" {
		t.Fatalf("远端能力应生效，实际 %s", got)
	}
	if got := reasoning.Caps.DefaultEffort(reasoning.RealmCN, "deepseek-v4-flash"); got != "high" {
		t.Fatalf("远端默认档应生效，实际 %s", got)
	}
	// 空能力不覆盖既有值
	publishEffortCaps(provider.WorkBuddy, []provider.ModelInfo{{ID: "deepseek-v4-flash"}})
	if got := reasoning.Caps.Clamp(reasoning.RealmCN, "deepseek-v4-flash", "ultra"); got != "max" {
		t.Fatalf("空能力不应清空既有值，实际 %s", got)
	}
}
