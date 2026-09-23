package app

import (
	"testing"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
)

// TestUniqueRuntimesDedupesSharedPool 锁定「共享账号池的渠道在账号维度只出现一次」。
//
// TraeWork 与 TraeCode 共用同一个池（同一上游账号体系，避免 refresh_token 轮换冲突），
// 但二者是 runtimes map 里的两个条目。若遍历 runtimes 统计账号，同一账号会被列两次
// —— 面板上曾因此显示两个 traework，账号总数虚高，「刷新全部」同账号被刷两遍。
func TestUniqueRuntimesDedupesSharedPool(t *testing.T) {
	shared := pool.New("")
	shared.Add(&auth.Auth{UID: "1923503557969866", Kind: "traework"})
	other := pool.New("")
	other.Add(&auth.Auth{UID: "qwen-uid", Kind: "qwenwork"})

	a := &App{runtimes: map[provider.Kind]*Runtime{
		provider.TraeWork: {Kind: provider.TraeWork, Pool: shared},
		provider.TraeCode: {Kind: provider.TraeCode, Pool: shared},
		provider.QwenWork: {Kind: provider.QwenWork, Pool: other},
	}}

	rts := a.uniqueRuntimes()
	if len(rts) != 2 {
		t.Fatalf("uniqueRuntimes 应去重到 2 个运行时，得到 %d", len(rts))
	}
	// 共享池归属渠道序更靠前的 TraeWork，保证面板展示名稳定（不受 map 遍历顺序影响）
	for _, rt := range rts {
		if rt.Pool == shared && rt.Kind != provider.TraeWork {
			t.Errorf("共享池应归属 TraeWork，得到 %s", rt.Kind)
		}
	}
	if n := a.totalAccounts(); n != 2 {
		t.Errorf("totalAccounts 应去重为 2，得到 %d", n)
	}
	if n := len(a.allStatuses()); n != 2 {
		t.Errorf("allStatuses 应去重为 2，得到 %d", n)
	}
}
