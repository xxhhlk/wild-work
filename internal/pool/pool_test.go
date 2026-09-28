package pool

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wild-work/internal/auth"
)

func TestPickHighestCredits(t *testing.T) {
	p := New("")
	a1 := &auth.Auth{UID: "u1"}
	a2 := &auth.Auth{UID: "u2"}
	a3 := &auth.Auth{UID: "u3"}
	p.Add(a1)
	p.Add(a2)
	p.Add(a3)
	p.SetCreditDetail("u1", 100, 0, 0)
	p.SetCreditDetail("u2", 500, 0, 0)
	p.SetCreditDetail("u3", 300, 0, 0)
	got := p.Pick("")
	if got == nil || got.UID != "u2" {
		t.Fatalf("pick=%+v want u2", got)
	}
}

func TestPickSkipsCooling(t *testing.T) {
	p := New("")
	a1 := &auth.Auth{UID: "u1"}
	a2 := &auth.Auth{UID: "u2"}
	p.Add(a1)
	p.Add(a2)
	p.SetCreditDetail("u1", 100, 0, 0)
	p.SetCreditDetail("u2", 50, 0, 0)
	p.Cooldown("u1", CoolHard, time.Hour, "test")
	got := p.Pick("")
	if got == nil || got.UID != "u2" {
		t.Fatalf("pick=%+v want u2", got)
	}
}

func TestPickExpiredCooldownReturnsToHealthy(t *testing.T) {
	p := New("")
	a1 := &auth.Auth{UID: "u1"}
	p.Add(a1)
	p.SetCreditDetail("u1", 100, 0, 0)
	p.Cooldown("u1", CoolSoft, time.Millisecond, "429")
	time.Sleep(5 * time.Millisecond)
	got := p.Pick("")
	if got == nil || got.UID != "u1" {
		t.Fatalf("pick=%+v want u1 after cooldown expiry", got)
	}
}

func TestPickNilWhenAllCooling(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Cooldown("u1", CoolHard, time.Hour, "x")
	if got := p.Pick(""); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestPickExcluding(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Add(&auth.Auth{UID: "u2"})
	p.SetCreditDetail("u1", 100, 0, 0)
	p.SetCreditDetail("u2", 50, 0, 0)
	tried := map[string]bool{"u1": true}
	got := p.PickExcluding(tried, "")
	if got == nil || got.UID != "u2" {
		t.Fatalf("pick=%+v want u2", got)
	}
	tried["u2"] = true
	if got := p.PickExcluding(tried, ""); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestCooldownPersists(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state.json")
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	p.Cooldown("u1", CoolHard, time.Hour, "余额不足")
	p2 := New(fp)
	p2.Add(&auth.Auth{UID: "u1"})
	if p2.Pick("") != nil {
		t.Fatal("cooldown lost after reload")
	}
	st, ok := p2.Status("u1")
	if !ok || st.Reason != "余额不足" {
		t.Errorf("status=%+v ok=%v", st, ok)
	}
}

func TestDisablePersists(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state.json")
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	p.Disable("u1", "12153 session dead")
	p2 := New(fp)
	p2.Add(&auth.Auth{UID: "u1"})
	if p2.Pick("") != nil {
		t.Fatal("disabled account picked after reload")
	}
	st, _ := p2.Status("u1")
	if !st.Disabled || st.Reason != "12153 session dead" {
		t.Errorf("status=%+v", st)
	}
}

func TestReenableIfCredits(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Cooldown("u1", CoolHard, time.Hour, "余额不足")
	p.ReenableIfCredits("u1", 500, 0, 120)
	got := p.Pick("")
	if got == nil || got.UID != "u1" {
		t.Fatalf("should reenable, pick=%+v", got)
	}
	// 不可消耗额度应被记录供面板展示，但不影响可消耗余额。
	st, _ := p.Status("u1")
	if st.Credits != 500 || st.UnusableCredits != 120 {
		t.Errorf("credits=%d unusable=%d, want 500/120", st.Credits, st.UnusableCredits)
	}
}

func TestReenableZeroCreditsKeepsCooling(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Cooldown("u1", CoolHard, time.Hour, "余额不足")
	p.ReenableIfCredits("u1", 0, 0, 0)
	if p.Pick("") != nil {
		t.Fatal("zero credits should stay cooling")
	}
}

func TestReenableDoesNotTouchDisabled(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Disable("u1", "session dead")
	p.ReenableIfCredits("u1", 500, 0, 0)
	if p.Pick("") != nil {
		t.Fatal("disabled must not auto-reenable")
	}
}

func TestNoteErrorThreshold(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	for i := 0; i < 2; i++ {
		p.NoteError("u1", 3, 10*time.Minute)
		if p.Pick("") == nil {
			t.Fatalf("cooling too early at %d", i+1)
		}
	}
	p.NoteError("u1", 3, 10*time.Minute)
	if p.Pick("") != nil {
		t.Fatal("threshold 3 should cool the account")
	}
}

func TestNoteSuccessResetsCounter(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.NoteError("u1", 3, time.Hour)
	p.NoteError("u1", 3, time.Hour)
	p.NoteSuccess("u1")
	p.NoteError("u1", 3, time.Hour)
	p.NoteError("u1", 3, time.Hour)
	if p.Pick("") == nil {
		t.Fatal("success should reset error counter")
	}
}

func TestList(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", Nickname: "nick1"})
	p.Add(&auth.Auth{UID: "u2"})
	p.SetCreditDetail("u1", 42, 0, 0)
	p.Cooldown("u2", CoolSoft, time.Minute, "429")
	list := p.List()
	if len(list) != 2 {
		t.Fatalf("list=%d", len(list))
	}
	var s1, s2 Status
	for _, s := range list {
		if s.UID == "u1" {
			s1 = s
		}
		if s.UID == "u2" {
			s2 = s
		}
	}
	if s1.Credits != 42 || s1.Nickname != "nick1" || s1.Disabled || s1.Cooling {
		t.Errorf("s1=%+v", s1)
	}
	if !s2.Cooling || s2.Reason != "429" {
		t.Errorf("s2=%+v", s2)
	}
}

func TestRemoveMissingFromDir(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Add(&auth.Auth{UID: "u2"})
	p.SyncToDir([]*auth.Auth{{UID: "u2"}})
	if p.Pick("") == nil || p.Pick("").UID != "u2" {
		t.Fatal("u1 should be removed")
	}
	if _, ok := p.Status("u1"); ok {
		t.Fatal("u1 should not exist")
	}
}

func TestRecordCheckinAndRemove(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.RecordCheckin("u1", true, "ok")
	st, _ := p.Status("u1")
	if !st.LastCheckinOK || st.LastCheckinAt.IsZero() {
		t.Errorf("status=%+v", st)
	}
	p.Remove("u1")
	if _, ok := p.Status("u1"); ok {
		t.Error("account should be removed")
	}
}

// TestLoadLegacyStateMarksCreditsStale v2.2.0 及之前的 state 文件无 version/unusable 字段，
// 读入后必须置 creditsStale，避免面板把旧口径余额当真值（需删除 data 目录才能恢复的兼容缺陷）。
// 自动刷新首刷成功（SetCreditDetail）后即清除标记。
func TestLoadLegacyStateMarksCreditsStale(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state-traework.json")
	legacy := `{"accounts":{"u1":{"credits":5320,"disabled":false}}}`
	if err := os.WriteFile(fp, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})

	st, ok := p.Status("u1")
	if !ok {
		t.Fatal("账号应存在")
	}
	if !st.CreditsStale {
		t.Errorf("旧格式 state 读入后应标记 credits_stale: %+v", st)
	}
	if st.Credits != 5320 {
		t.Errorf("credits=%d want 5320（旧值保留，仅标记不可信）", st.Credits)
	}

	// 首刷成功 → 标记清除，数字变为真实拆分
	p.SetCreditDetail("u1", 2710, 0, 2600)
	st, _ = p.Status("u1")
	if st.CreditsStale {
		t.Error("SetCreditDetail 后不应再标记 stale")
	}
	if st.Credits != 2710 || st.UnusableCredits != 2600 {
		t.Errorf("credits=%d unusable=%d want 2710/2600", st.Credits, st.UnusableCredits)
	}
}

// TestLoadCurrentStateNotStale 新版格式（version>=stateVersion=3）读入后不标记 stale。
// v2 文件（无 expiring）按旧版处理置 stale——可用/不可用/临期三者口径需同批刷新。
func TestLoadCurrentStateNotStale(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state-traework.json")
	cur := `{"version":3,"accounts":{"u1":{"credits":2710,"expiring":130,"unusable":2600}}}`
	if err := os.WriteFile(fp, []byte(cur), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	st, _ := p.Status("u1")
	if st.CreditsStale {
		t.Errorf("新版格式不应标记 stale: %+v", st)
	}
	if st.Credits != 2710 || st.ExpiringCredits != 130 || st.UnusableCredits != 2600 {
		t.Errorf("credits=%d expiring=%d unusable=%d want 2710/130/2600", st.Credits, st.ExpiringCredits, st.UnusableCredits)
	}
}

// TestPickPrefersExpiring 临期优先：临期>0 的账号即使总余额更低也先被选中，
// 同组内仍按总可用余额排序；临期烧完（重置为 0）后退回总余额排序。
func TestPickPrefersExpiring(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"}) // 总余额最高，但无临期
	p.Add(&auth.Auth{UID: "u2"}) // 有临期
	p.Add(&auth.Auth{UID: "u3"}) // 临期更多
	p.SetCreditDetail("u1", 5000, 0, 0)
	p.SetCreditDetail("u2", 100, 50, 0)
	p.SetCreditDetail("u3", 200, 200, 0)
	if got := p.Pick(""); got == nil || got.UID != "u3" {
		t.Fatalf("pick=%+v want u3 (临期最多)", got)
	}
	// u3 临期烧完 → u2 接管
	p.SetCreditDetail("u3", 200, 0, 0)
	if got := p.Pick(""); got == nil || got.UID != "u2" {
		t.Fatalf("pick=%+v want u2 (次多临期)", got)
	}
	// 全部临期清零 → 退回总余额排序
	p.SetCreditDetail("u2", 100, 0, 0)
	if got := p.Pick(""); got == nil || got.UID != "u1" {
		t.Fatalf("pick=%+v want u1 (纯总余额排序)", got)
	}
}

// TestPickExpiringZeroBalance 确保临期统计的约束 expiring ≤ credits：
// credits=0 的账号即使误传 expiring>0 也不会被选中（防御性用例，正常调用方不会这样传）。
func TestPickExpiringZeroBalance(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Add(&auth.Auth{UID: "u2"})
	p.SetCreditDetail("u1", 0, 0, 0)
	p.SetCreditDetail("u2", 10, 0, 0)
	if got := p.Pick(""); got == nil || got.UID != "u2" {
		t.Fatalf("pick=%+v want u2", got)
	}
}

// ---------------------------------------------------------------------------
// 模型级冷却（CooldownModel）
// ---------------------------------------------------------------------------

// TestCooldownModelIsolatesModel 模型级冷却只让该账号在该模型上不可选，其他模型仍可路由。
// 上游按模型独立限流（WorkBuddy 系 6004 原文即声明「可换其他模型继续用」），
// 此前落到账号级冷却 —— 一个模型撞到每日上限会把账号上仍可用的模型一起连坐。
func TestCooldownModelIsolatesModel(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.Add(&auth.Auth{UID: "u2"})
	p.SetCreditDetail("u1", 500, 0, 0) // 余额最高，无冷却时必被选中
	p.SetCreditDetail("u2", 100, 0, 0)

	if got := p.Pick("flash"); got == nil || got.UID != "u1" {
		t.Fatalf("pick=%+v want u1（无冷却时余额最高）", got)
	}

	p.CooldownModel("u1", "flash", CoolSoft, time.Hour, "429 rate limit")

	if got := p.Pick("flash"); got == nil || got.UID != "u2" {
		t.Fatalf("pick=%+v want u2（u1 在 flash 上被冷却）", got)
	}
	if got := p.Pick("luna"); got == nil || got.UID != "u1" {
		t.Fatalf("pick=%+v want u1（luna 不受 flash 冷却影响）", got)
	}
	// 账号本身不算冷却，模型级冷却单独暴露（合并会让面板误报「账号被限流」）
	st, _ := p.Status("u1")
	if st.Cooling {
		t.Error("模型级冷却不应把账号标记为整体 Cooling")
	}
	if len(st.ModelCooling) != 1 || st.ModelCooling[0].Model != "flash" {
		t.Errorf("ModelCooling=%+v want [flash]", st.ModelCooling)
	}
}

// TestCooldownModelEmptyIsAccountLevel 空模型名退化为账号级冷却（调用方未指定模型时的安全默认）。
func TestCooldownModelEmptyIsAccountLevel(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.CooldownModel("u1", "", CoolSoft, time.Hour, "x")
	if got := p.Pick("flash"); got != nil {
		t.Fatalf("空 model 应退化为账号级冷却，pick=%+v", got)
	}
	st, _ := p.Status("u1")
	if !st.Cooling || len(st.ModelCooling) != 0 {
		t.Errorf("status=%+v want Cooling=true 且无 ModelCooling", st)
	}
}

// TestCooldownModelExpires 模型级冷却到期后该模型恢复可选。
func TestCooldownModelExpires(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.CooldownModel("u1", "flash", CoolSoft, time.Millisecond, "429")
	time.Sleep(5 * time.Millisecond)
	if got := p.Pick("flash"); got == nil || got.UID != "u1" {
		t.Fatalf("pick=%+v want u1 after model cooldown expiry", got)
	}
}

// TestCooldownModelPersists 模型级冷却随 state.json 往返，且只影响该模型。
func TestCooldownModelPersists(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state.json")
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	p.CooldownModel("u1", "flash", CoolSoft, time.Hour, "429 rate limit")

	p2 := New(fp)
	p2.Add(&auth.Auth{UID: "u1"})
	if got := p2.Pick("flash"); got != nil {
		t.Fatal("模型级冷却在 reload 后丢失")
	}
	if got := p2.Pick("luna"); got == nil || got.UID != "u1" {
		t.Fatalf("luna 应仍可选，pick=%+v", got)
	}
	st, _ := p2.Status("u1")
	if len(st.ModelCooling) != 1 || st.ModelCooling[0].Reason != "429 rate limit" {
		t.Errorf("ModelCooling=%+v want 1 条且带 reason", st.ModelCooling)
	}
}

// TestCooldownModelPrunesExpired 写入新模型冷却时清理已过期条目，
// 避免 map 随「请求过的模型数」无限增长。
func TestCooldownModelPrunesExpired(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.CooldownModel("u1", "m1", CoolSoft, time.Millisecond, "x")
	time.Sleep(5 * time.Millisecond)
	p.CooldownModel("u1", "m2", CoolSoft, time.Hour, "x")
	st, _ := p.Status("u1")
	if len(st.ModelCooling) != 1 || st.ModelCooling[0].Model != "m2" {
		t.Errorf("过期条目应被清理，ModelCooling=%+v", st.ModelCooling)
	}
}

// TestReenableClearsModelCooldown 签到解冻同时清账号级与模型级冷却。
// 残留模型级冷却会让账号在特定模型上长期不可选，而签到成功已证明账号整体可用。
func TestReenableClearsModelCooldown(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.CooldownModel("u1", "flash", CoolSoft, time.Hour, "429")
	p.ReenableIfCredits("u1", 500, 0, 0)
	if got := p.Pick("flash"); got == nil || got.UID != "u1" {
		t.Fatalf("签到解冻应同时清模型级冷却，pick=%+v", got)
	}
}

// TestClearPenaltyClearsModelCooldown 单账号渠道启动自愈同样要清模型级冷却。
func TestClearPenaltyClearsModelCooldown(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1"})
	p.CooldownModel("u1", "flash", CoolSoft, time.Hour, "429")
	p.ClearPenalty("u1")
	if got := p.Pick("flash"); got == nil || got.UID != "u1" {
		t.Fatalf("ClearPenalty 应同时清模型级冷却，pick=%+v", got)
	}
}

// TestLoadV3StateNotStale v4 只新增 model_until（模型级冷却），余额口径未变，
// 故 v3 文件读入后不应标记 creditsStale（否则升级后面板白闪一次「待刷新」）。
func TestLoadV3StateNotStale(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "state-traework.json")
	v3 := `{"version":3,"accounts":{"u1":{"credits":2710,"expiring":130,"unusable":2600}}}`
	if err := os.WriteFile(fp, []byte(v3), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(fp)
	p.Add(&auth.Auth{UID: "u1"})
	st, _ := p.Status("u1")
	if st.CreditsStale {
		t.Errorf("v3 文件在 v4 下不应标记 stale: %+v", st)
	}
	if st.Credits != 2710 || st.ExpiringCredits != 130 {
		t.Errorf("credits=%d expiring=%d want 2710/130", st.Credits, st.ExpiringCredits)
	}
}
