package provider

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

// ---------------------------------------------------------------------------
// 流式收尾：截断时必须补 error 帧 + [DONE]
// ---------------------------------------------------------------------------
//
// 为什么这是硬要求：SSE 响应头一旦发出（200 + text/event-stream），后续任何故障
// 都无法再用 HTTP 状态码表达。此时若直接 return 而不写收尾帧，客户端收到的是
// 一条**没有 [DONE] 的截断流**——表现为「突然无响应」（实测 loomy 空闲超时即此形态）。
//
// 补一帧 error 让客户端拿到明确原因（可据此重试），再补 [DONE] 保证流正常收尾。
// 两者缺一不可：只补 [DONE] 会把故障伪装成正常结束，只补 error 则部分客户端
// 会一直等 [DONE] 而挂住。

// TruncationErrorCode 把流中断错误归一成客户端可见的错误码。
// 空闲超时单独给码：它是「上游卡死」，与普通读错误（连接被切断等）运维含义不同。
func TruncationErrorCode(err error) string {
	if IsIdleTimeout(err) {
		return "upstream_timeout"
	}
	return "upstream_stream_error"
}

// WriteTruncationFrames 在流中断处补写 error 帧 + [DONE]，并 flush。
//
// 调用方应在「已开始写流、随后读上游出错」的分支里调用，然后再返回原始错误
// （错误仍需上抛给 handler 记日志；本函数只负责让客户端能收尾）。
// w 不是 Flusher 时静默跳过 flush（httptest.ResponseRecorder 等场景）。
//
// 错误信息做截断：上游/网络错误文本可能很长（含 URL、证书细节），
// 全部塞进 SSE 帧会撑大客户端日志，200 字足够定位。
func WriteTruncationFrames(w io.Writer, err error) {
	msg := "upstream stream interrupted"
	if err != nil {
		msg = truncateRunes(err.Error(), 200)
	}
	frame, merr := json.Marshal(map[string]any{"error": map[string]any{
		"message": msg,
		"type":    "upstream_error",
		"code":    TruncationErrorCode(err),
	}})
	if merr != nil {
		// 序列化失败理论上不可能（纯字符串 map）；退化成固定帧，仍保证收尾。
		frame = []byte(`{"error":{"message":"upstream stream interrupted","type":"upstream_error","code":"upstream_stream_error"}}`)
	}
	_, _ = w.Write(append(append([]byte("data: "), frame...), []byte("\n\ndata: [DONE]\n\n")...))
	if fl, ok := w.(interface{ Flush() }); ok {
		fl.Flush()
	}
}

// truncateRunes rune 安全截断（不切碎多字节字符）。
func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// ErrIdleTimeout 上游流长时间无数据（空闲超时）。
//
// 与 http.Client.Timeout 的区别是本模块存在的全部理由：
//
//	http.Client.Timeout  整请求上限 —— 计时器在 Do() 返回后继续跑，直到 body 读完。
//	                     SSE 整个生成期都在读 body，故「慢但一直在出字」的请求
//	                     也会被从流中间掐断（实测 loomy 两次中断都恰好 120.00s）。
//	ErrIdleTimeout      空闲上限 —— 只在「连续 N 秒一个字节都没读到」时才判死。
//	                     慢而持续输出的响应不受影响，真卡住仍能及时发现。
//
// 流式渠道因此必须用无总超时的 client + 本看门狗兜底（见 AGENTS 不变量）。
var ErrIdleTimeout = errors.New("upstream stream idle timeout")

// IsIdleTimeout 报告错误是否为空闲超时（含被 IdleReader 包装后的形态）。
func IsIdleTimeout(err error) bool { return errors.Is(err, ErrIdleTimeout) }

// IdleReader 给流式响应体加「空闲超时」看门狗。
//
// 每次 Read 起一个 goroutine 去读底层，与定时器 select 竞争：
//   - 数据先到 → 正常返回；
//   - 定时器先到 → Close 底层（解除 net/http body 在途的 Read）→ 返回 ErrIdleTimeout。
//
// 超时路径会**等读取 goroutine 真正退出**再返回，否则调用方拿到的 p 会被那个
// goroutine 并发写（数据竞争）。Close 能中断挂起的 Read，所以这个等待不会永久阻塞。
//
// 非流式（aggregate）路径同样适用：由「总时长硬上限」变为「空闲上限」，
// 长而持续输出的响应不再被误杀，真卡住仍会报错。
type IdleReader struct {
	rc io.ReadCloser
	d  time.Duration

	closed bool // 已判定空闲超时并关掉底层（后续 Read 直接报错，不再重复关）
}

// NewIdleReader 包装 rc；d <= 0 时不加看门狗（直接透传，便于测试与「显式关闭」场景）。
func NewIdleReader(rc io.ReadCloser, d time.Duration) *IdleReader {
	return &IdleReader{rc: rc, d: d}
}

// Read 实现 io.Reader。
func (r *IdleReader) Read(p []byte) (int, error) {
	if r.d <= 0 {
		return r.rc.Read(p)
	}
	if r.closed {
		return 0, ErrIdleTimeout
	}

	type result struct {
		n   int
		err error
	}
	// 缓冲 1：超时分支返回后，读取 goroutine 仍可能写入，不能被阻塞住。
	ch := make(chan result, 1)
	go func() {
		n, err := r.rc.Read(p)
		ch <- result{n, err}
	}()

	t := time.NewTimer(r.d)
	defer t.Stop()
	select {
	case res := <-ch:
		return res.n, res.err
	case <-t.C:
		// 关底层解除挂起的 Read，然后等它退出——p 的所有权必须干净交还。
		r.closed = true
		_ = r.rc.Close()
		<-ch
		return 0, ErrIdleTimeout
	}
}

// Close 关闭底层响应体。幂等（超时路径已关过时只返回 nil）。
func (r *IdleReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.rc.Close()
}
