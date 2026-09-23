package qoder

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"
)

// TestAuthHeaderDateMatchesSignature 锁定签名契约：AuthHeader 返回的 date
// 必须就是签名串里使用的时间戳，cosy-date 头要原样复用它。
//
// 反例（本次修复的 bug）：签名与 cosy-date 头各自 time.Now()。签名串里含整个
// body，长会话（数 MB 上下文）时 md5 + 字符串拼接要耗掉毫秒级时间，两次取值
// 可能跨秒 → 上游拿 cosy-date 重算签名对不上 → {"code":"101","message":
// "Signature invalid"}，表现为长会话里偶发、body 越大越频繁。
func TestAuthHeaderDateMatchesSignature(t *testing.T) {
	s := &CosySession{
		MachineID:    "mid",
		MachineToken: "mtok",
		MachineType:  "mtype",
		TempKey:      []byte("0123456789abcdef"),
		CosyKey:      "cosykey",
		Info:         "info",
	}
	const body = `{"hello":"world"}`
	const rawURL = "https://gateway.qoder.com.cn/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result"
	const wantPath = "/api/v2/service/pro/sse/agent_chat_generation"

	auth, date, err := s.AuthHeader(body, rawURL, "uid")
	if err != nil {
		t.Fatal(err)
	}
	if date == "" {
		t.Fatal("AuthHeader 应返回签名所用时间戳（供 cosy-date 头复用）")
	}
	parts := strings.SplitN(auth, ".", 3)
	if len(parts) != 3 || parts[0] != "Bearer COSY" {
		t.Fatalf("auth 结构非法: %q", auth)
	}
	// 用返回的 date 复算签名，必须与 Authorization 里的 sig 逐字节一致
	sum := md5.Sum([]byte(parts[1] + "\n" + s.CosyKey + "\n" + date + "\n" + body + "\n" + wantPath))
	if got := hex.EncodeToString(sum[:]); got != parts[2] {
		t.Fatalf("签名与返回的 date 不一致：复算 %s != auth %s", got, parts[2])
	}
}

// BenchmarkAuthHeaderLargeBody 量化 AuthHeader 在长会话下的耗时（body 含在签名串里，
// 需 md5 + 一次字符串拼接）。修复前，签名 date 与 cosy-date 头之间的间隔就是这个
// 量级，跨秒概率 ≈ 耗时/1s —— 这解释了为什么 Signature invalid 只在长会话里偶发。
func BenchmarkAuthHeaderLargeBody(b *testing.B) {
	s := &CosySession{CosyKey: "k", Info: "i"}
	body := strings.Repeat("x", 3<<20) // 3 MiB，接近长 agent 会话的请求体
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.AuthHeader(body, "https://gateway.qoder.com.cn/algo/api/v2/service/pro/sse/agent_chat_generation", "u"); err != nil {
			b.Fatal(err)
		}
	}
}
