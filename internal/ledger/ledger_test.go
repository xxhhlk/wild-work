package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wild-work/internal/provider"
)

// TestDiffCreditsEarningsAndSpend 差分记账：发放→入项、消耗→spend、消失→过期。
func TestDiffCreditsEarningsAndSpend(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	l, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	cur1 := []provider.ResourceItem{
		{Name: "签到奖励", Key: "checkin_1", Remain: 100, ExpireAt: "2099-01-01", Usable: true},
	}
	if n := l.DiffCredits("traework", "u1", 100, cur1); n != 1 {
		t.Fatalf("首次快照应记 1 条 earn，got %d", n)
	}

	// 消耗 30：spend
	cur2 := []provider.ResourceItem{
		{Name: "签到奖励", Key: "checkin_1", Remain: 70, ExpireAt: "2099-01-01", Usable: true},
	}
	if n := l.DiffCredits("traework", "u1", 70, cur2); n != 1 {
		t.Fatalf("消耗应记 1 条 spend，got %d", n)
	}

	// 条目消失：expire
	if n := l.DiffCredits("traework", "u1", 0, nil); n != 1 {
		t.Fatalf("条目消失应记 1 条 expire，got %d", n)
	}

	l.Flush()
	// 验证文件行数与 kind 序列
	raw, err := os.ReadFile(filepath.Join(dir, monthFile("credit", time.Now())))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, line := range splitLines(raw) {
		var e CreditEntry
		if unmarshal(line, &e) == nil {
			kinds = append(kinds, e.Kind)
		}
	}
	want := []string{"earn", "spend", "expire"}
	if len(kinds) != 3 {
		t.Fatalf("kinds=%v", kinds)
	}
	for i, w := range want {
		if kinds[i] != w {
			t.Fatalf("kinds[%d]=%s want %s", i, kinds[i], w)
		}
	}
}

// TestDiffCreditsExpiredEntry 到期日已过的下降归因 expire 而非 spend。
func TestDiffCreditsExpiredEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	l, _ := New(dir)
	defer l.Close()

	yesterday := time.Now().In(expireLoc).AddDate(0, 0, -1).Format("2006-01-02")
	cur1 := []provider.ResourceItem{
		{Name: "每日奖励", Key: "daily", Remain: 100, ExpireAt: yesterday, Usable: true},
	}
	l.DiffCredits("traework", "u2", 100, cur1)
	cur2 := []provider.ResourceItem{
		{Name: "每日奖励", Key: "daily", Remain: 40, ExpireAt: yesterday, Usable: true},
	}
	l.DiffCredits("traework", "u2", 40, cur2)

	l.Flush()
	raw, _ := os.ReadFile(filepath.Join(dir, monthFile("credit", time.Now())))
	var last CreditEntry
	for _, line := range splitLines(raw) {
		_ = unmarshal(line, &last)
	}
	if last.Kind != "expire" {
		t.Fatalf("到期日已过的下降应归因 expire，got %s", last.Kind)
	}
}

// TestQueryAggregation 聚合：token 按模型、积分按账号，范围过滤生效。
func TestQueryAggregation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	l, _ := New(dir)
	defer l.Close()

	l.AppendUsage(UsageEntry{Ch: "workbuddy", UID: "u1", Model: "glm-5.2", PT: 100, CT: 50, Src: "upstream"})
	l.AppendUsage(UsageEntry{Ch: "workbuddy", UID: "u1", Model: "glm-5.2", PT: 10, CT: 5})
	l.AppendUsage(UsageEntry{Ch: "traework", UID: "u2", Model: "doubao-seed", PT: 200, CT: 100})
	l.AppendCredit(CreditEntry{Ch: "workbuddy", UID: "u1", Kind: "earn", Amount: 1550, Balance: 1550})
	l.AppendCredit(CreditEntry{Ch: "traework", UID: "u2", Kind: "spend", Amount: -30, Balance: 970})

	st := l.Query(7, nil)
	if st.Token.Total != 465 {
		t.Fatalf("token total=%d want 465", st.Token.Total)
	}
	if st.Token.Requests != 3 {
		t.Fatalf("requests=%d want 3", st.Token.Requests)
	}
	if len(st.Token.ByModel) != 2 {
		t.Fatalf("byModel len=%d", len(st.Token.ByModel))
	}
	// 模型榜降序：glm-5.2 (165) < doubao (300)，doubao 应排前
	if st.Token.ByModel[0].Model != "doubao-seed" {
		t.Fatalf("byModel[0]=%s want doubao-seed", st.Token.ByModel[0].Model)
	}
	if st.Credit.Earn != 1550 || st.Credit.Spend != 30 {
		t.Fatalf("earn=%d spend=%d", st.Credit.Earn, st.Credit.Spend)
	}
	// 原始条目：时间升序全量返回
	if len(st.Credit.Entries) != 2 {
		t.Fatalf("entries=%d want 2", len(st.Credit.Entries))
	}
	if st.Credit.Entries[0].Kind != "earn" || st.Credit.Entries[1].Kind != "spend" {
		t.Fatalf("entries kinds=%s,%s want earn,spend", st.Credit.Entries[0].Kind, st.Credit.Entries[1].Kind)
	}
}
