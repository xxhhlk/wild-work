package app

import (
	"path/filepath"
	"testing"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/config"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
)

// TestAccountViewExposesModelCooling /api/state 的账号视图必须带上模型级冷却。
//
// 回归：AccountView 是与 pool.Status 平行的独立视图结构（多 group / 格式化时间等），
// 新增 pool.Status 字段时极易漏掉这里的映射 —— 漏了不会编译失败、单测也照绿，
// 只表现为前端「限流 <模型>」标签永远不显示。现场实测（第二实例注入 model_until）抓到过一次。
func TestAccountViewExposesModelCooling(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.StateFile = filepath.Join(dir, "state.json")
	cfg.AuthDir = dir

	wbPool := pool.New(filepath.Join(dir, "state-workbuddyai.json"))
	wbPool.Add(&auth.Auth{Kind: "workbuddyai", UID: "wb-1", Nickname: "甲", AccessToken: "t1", RefreshToken: "r1"})
	wbPool.CooldownModel("wb-1", "deepseek-v4.1-flash", pool.CoolSoft, time.Hour, "429 rate limit")

	a, err := New(Options{
		ConfigPath: filepath.Join(dir, "config.json"),
		Config:     cfg,
		Runtimes: map[provider.Kind]*Runtime{
			provider.WorkBuddyAI: {Kind: provider.WorkBuddyAI, Pool: wbPool, Upstream: &fakeUpstream{remain: 10}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(a.Close)

	st := a.GetState()
	if len(st.Accounts) != 1 {
		t.Fatalf("账号数应为 1，实际 %d：%+v", len(st.Accounts), st.Accounts)
	}
	v := st.Accounts[0]
	if v.Cooling {
		t.Error("模型级冷却不应把账号标记为整体 Cooling（会让人误以为整个账号不可用）")
	}
	if len(v.ModelCooling) != 1 {
		t.Fatalf("ModelCooling=%+v，want 1 条", v.ModelCooling)
	}
	if got := v.ModelCooling[0].Model; got != "deepseek-v4.1-flash" {
		t.Errorf("model=%q want deepseek-v4.1-flash", got)
	}
	if v.ModelCooling[0].Until == "" {
		t.Error("Until 应格式化为非空字符串（与账号级 Until 一致）")
	}
}

// TestModelCoolViewsEmptyIsNil 无模型级冷却时必须返回 nil（而非空切片），
// 否则 json omitempty 不生效，每个账号都会多出一个 model_cooling: [] 字段。
func TestModelCoolViewsEmptyIsNil(t *testing.T) {
	if got := modelCoolViews(nil); got != nil {
		t.Errorf("modelCoolViews(nil)=%v want nil", got)
	}
	if got := modelCoolViews([]pool.ModelCoolStatus{}); got != nil {
		t.Errorf("modelCoolViews(空切片)=%v want nil", got)
	}
}
