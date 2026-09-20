// handler_effort_warm_test.go 请求路径的档位能力表预热（ensureEffortCaps）。
package server

import (
	"sync/atomic"
	"testing"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
	"wild-work/internal/reasoning"
)

// countingUpstream 记录 FetchModels 调用次数，其余行为沿用 echoUpstream。
type countingUpstream struct {
	echoUpstream
	calls int32
	infos []provider.ModelInfo
}

func (u *countingUpstream) FetchModels(*auth.Auth) ([]provider.ModelInfo, error) {
	atomic.AddInt32(&u.calls, 1)
	return u.infos, nil
}

func (u *countingUpstream) count() int32 { return atomic.LoadInt32(&u.calls) }

// newWarmHandler 构造单渠道 Handler 并清空包级缓存（模型缓存与能力表都是
// 进程级共享状态，测试之间必须隔离）。
func newWarmHandler(t *testing.T, up provider.Upstream) *Handler {
	t.Helper()
	old := reasoning.Caps
	reasoning.Caps = reasoning.NewCatalog()
	t.Cleanup(func() { reasoning.Caps = old })

	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t", ExpiresAt: time.Now().Add(24 * time.Hour).Unix()})
	h := NewHandler(Config{Runtimes: map[provider.Kind]*Runtime{
		provider.WorkBuddy: {Kind: provider.WorkBuddy, Pool: p, Upstream: up},
	}})
	h.InvalidateModels()
	t.Cleanup(h.InvalidateModels)
	return h
}

// 预热只在尚无远端能力时触发一次；无档位能力的渠道完全不触发。
func TestEnsureEffortCapsWarmsOnce(t *testing.T) {
	up := &countingUpstream{infos: []provider.ModelInfo{
		{ID: "deepseek-v4-pro", SupportedEfforts: []string{"low", "high"}, DefaultEffort: "high"},
	}}
	h := newWarmHandler(t, up)
	rt := h.cfg.Runtimes[provider.WorkBuddy]

	// TraeWork 协议没有档位字段，预热无意义也不应拉目录
	h.ensureEffortCaps(&Runtime{Kind: provider.TraeWork})
	if n := up.count(); n != 0 {
		t.Fatalf("无档位能力的渠道不应触发目录拉取，实际 %d 次", n)
	}

	h.ensureEffortCaps(rt)
	if n := up.count(); n != 1 {
		t.Fatalf("首次请求应预热一次，实际 %d 次", n)
	}
	if !reasoning.Caps.HasRemote(reasoning.RealmCN) {
		t.Fatal("预热后应写入远端能力")
	}
	if got := reasoning.Caps.Clamp(reasoning.RealmCN, "deepseek-v4-pro", "xhigh"); got != "high" {
		t.Fatalf("预热后的远端能力应参与降级，实际 %s", got)
	}

	// 已有远端能力 → 不再重复拉取（每个进程只多付一次目录请求）
	h.ensureEffortCaps(rt)
	h.ensureEffortCaps(rt)
	if n := up.count(); n != 1 {
		t.Fatalf("已有远端能力时不应重复拉取，实际 %d 次", n)
	}
}

// 拉取失败不写入能力，且失败冷却内不重复拉取。
func TestEnsureEffortCapsFetchFailure(t *testing.T) {
	up := &countingUpstream{}
	h := newWarmHandler(t, up)
	rt := h.cfg.Runtimes[provider.WorkBuddy]

	h.ensureEffortCaps(rt)
	if reasoning.Caps.HasRemote(reasoning.RealmCN) {
		t.Fatal("拉取失败不应写入远端能力")
	}
	h.ensureEffortCaps(rt)
	if n := up.count(); n != 1 {
		t.Fatalf("失败冷却内不应重复拉取，实际 %d 次", n)
	}
}
