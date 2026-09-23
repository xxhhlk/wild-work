package traework

import (
	"encoding/json"
	"testing"
)

// TestNewTraeCodeUsesCodeFunction 钉住 TraeCode 与 TraeWork 的 function 不串。
//
// 两者是同一上游的两个 function，唯一区别就是 function 与定价分组口径。
// 一旦写错，表现为「用 TraeCode 调用却被当作办公版」——模型不可用或计费口径错，
// 且从日志上不容易看出来。
func TestNewTraeCodeUsesCodeFunction(t *testing.T) {
	work := New()
	if work.Function != Function {
		t.Errorf("New() function = %q，期望 %q", work.Function, Function)
	}
	if work.PricingChannel != "traework" {
		t.Errorf("New() PricingChannel = %q，期望 traework", work.PricingChannel)
	}
	if work.PricingPrimary != PricingPrimaryWork {
		t.Errorf("New() PricingPrimary = %q，期望 %q", work.PricingPrimary, PricingPrimaryWork)
	}

	code := NewTraeCode()
	if code.Function != FunctionCode {
		t.Errorf("NewTraeCode() function = %q，期望 %q", code.Function, FunctionCode)
	}
	if code.PricingFunctions != PricingFunctionsCode {
		t.Errorf("NewTraeCode() PricingFunctions = %q，期望 %q", code.PricingFunctions, PricingFunctionsCode)
	}
	if code.PricingChannel != "traecode" {
		t.Errorf("NewTraeCode() PricingChannel = %q，期望 traecode", code.PricingChannel)
	}
	if code.PricingPrimary != PricingPrimaryCode {
		t.Errorf("NewTraeCode() PricingPrimary = %q，期望 %q", code.PricingPrimary, PricingPrimaryCode)
	}
	// TraeCode 不是独立上游：账号体系与 TraeWork 相同，故基址一致。
	if code.AgentHost != work.AgentHost || code.UgHost != work.UgHost || code.OAuthHost != work.OAuthHost {
		t.Error("TraeCode 应与 TraeWork 指向同一上游（共用账号）")
	}
}

// TestPrepareBodyWritesGivenFunction 钉住传入的 function 真的落到请求体里。
// 该函数原先硬编码 Function 常量，改成参数后若漏传/传错，solo 会按错误 function 处理。
func TestPrepareBodyWritesGivenFunction(t *testing.T) {
	src := []byte(`{"model":"glm-5.3","messages":[{"role":"user","content":"hi"}]}`)
	for _, fn := range []string{Function, FunctionCode} {
		out := PrepareBody(src, fn)
		var obj map[string]any
		if err := json.Unmarshal(out, &obj); err != nil {
			t.Fatalf("PrepareBody(function=%q) 产出非法 JSON: %v", fn, err)
		}
		if got, _ := obj["function"].(string); got != fn {
			t.Errorf("function = %q，期望 %q", got, fn)
		}
		// config_name 与 model 应同步为剥离渠道前缀后的模型名。
		if got, _ := obj["config_name"].(string); got != "glm-5.3" {
			t.Errorf("config_name = %q，期望 glm-5.3", got)
		}
		if got, _ := obj["model"].(string); got != "glm-5.3" {
			t.Errorf("model = %q，期望 glm-5.3", got)
		}
	}
}
