package provider

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingBody 一个「Read 会永久挂住直到被 Close」的响应体，模拟上游卡死。
type blockingBody struct {
	mu     sync.Mutex
	closed bool
	ch     chan struct{} // Close 时关闭，解除挂起的 Read
}

func newBlockingBody() *blockingBody { return &blockingBody{ch: make(chan struct{})} }

func (b *blockingBody) Read(p []byte) (int, error) {
	<-b.ch
	return 0, io.ErrUnexpectedEOF // 被 Close 后返回读错误（与 net/http 行为一致）
}

func (b *blockingBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.ch)
	}
	return nil
}

func (b *blockingBody) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// TestIdleReaderTimesOut 守门：连续无数据超过阈值必须判 ErrIdleTimeout，并关掉底层 body。
//
// 这是 loomy/raccoon 流式去掉「整请求总超时」后的唯一兜底 ——
// 若它不生效，上游卡死会变成无限等待（比原来的 120s 截断更糟）。
func TestIdleReaderTimesOut(t *testing.T) {
	body := newBlockingBody()
	r := NewIdleReader(body, 50*time.Millisecond)

	start := time.Now()
	n, err := r.Read(make([]byte, 16))
	elapsed := time.Since(start)

	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("err = %v, want ErrIdleTimeout", err)
	}
	if !IsIdleTimeout(err) {
		t.Fatal("IsIdleTimeout 应识别自身哨兵")
	}
	// 阈值是 50ms：不能提前返回（说明没等），也不该远超（说明没超时机制）。
	if elapsed < 40*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("耗时 %v，不像空闲超时（阈值 50ms）", elapsed)
	}
	if !body.isClosed() {
		t.Fatal("超时后必须关闭底层 body，否则连接泄漏")
	}
}

// TestIdleReaderPassesThroughData 有数据时逐次透传，不受空闲阈值影响。
func TestIdleReaderPassesThroughData(t *testing.T) {
	r := NewIdleReader(io.NopCloser(strings.NewReader("hello world")), 50*time.Millisecond)
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q", got)
	}
}

// TestIdleReaderResetsTimerPerRead 每次成功读取都重置计时：
// 连续多次「接近但不超过阈值」的读取不该被判超时（慢而持续的流必须放行）。
//
// 这正是本次修复的核心诉求 —— 旧的 http.Client.Timeout 会把这种流掐断。
func TestIdleReaderResetsTimerPerRead(t *testing.T) {
	const idle = 120 * time.Millisecond
	pr, pw := io.Pipe()
	r := NewIdleReader(pr, idle)
	defer pw.Close()

	// 每 40ms 送一段，共 5 段（总耗时 200ms > idle，但每次间隔都远小于 idle）。
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(40 * time.Millisecond)
			_, _ = pw.Write([]byte("x"))
		}
		pw.Close()
	}()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("持续输出的流不该超时: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("读到 %d 字节，want 5", len(got))
	}
}

// TestIdleReaderDisabled 阈值 <= 0 时不加看门狗（直接透传，便于测试与显式关闭场景）。
func TestIdleReaderDisabled(t *testing.T) {
	r := NewIdleReader(io.NopCloser(strings.NewReader("abc")), 0)
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("got %q", got)
	}
}

// TestIdleReaderCloseIdempotent Close 幂等（超时路径已关过时再关不应报错/重复关）。
func TestIdleReaderCloseIdempotent(t *testing.T) {
	body := newBlockingBody()
	r := NewIdleReader(body, 20*time.Millisecond)
	if _, err := r.Read(make([]byte, 4)); !IsIdleTimeout(err) {
		t.Fatalf("err = %v, want ErrIdleTimeout", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("超时后再 Close 应返回 nil: %v", err)
	}
}

// TestWriteTruncationFrames 守门：流中断必须补 error 帧 + [DONE]。
//
// 不补帧 = 客户端收到无收尾的截断流 =「突然无响应」（2026-09-24 loomy 实测形态）。
func TestWriteTruncationFrames(t *testing.T) {
	t.Run("空闲超时用 upstream_timeout", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteTruncationFrames(rec, ErrIdleTimeout)
		body := rec.Body.String()
		if !strings.Contains(body, `"code":"upstream_timeout"`) {
			t.Fatalf("缺少 upstream_timeout 码: %s", body)
		}
		if !strings.Contains(body, "data: [DONE]") {
			t.Fatalf("缺少 [DONE] 收尾: %s", body)
		}
		if !strings.Contains(body, `"type":"upstream_error"`) {
			t.Fatalf("缺少 upstream_error 类型: %s", body)
		}
	})
	t.Run("普通读错误用 upstream_stream_error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteTruncationFrames(rec, io.ErrUnexpectedEOF)
		body := rec.Body.String()
		if !strings.Contains(body, `"code":"upstream_stream_error"`) {
			t.Fatalf("缺少 upstream_stream_error 码: %s", body)
		}
		if !strings.Contains(body, "data: [DONE]") {
			t.Fatalf("缺少 [DONE] 收尾: %s", body)
		}
	})
	t.Run("err 为 nil 时仍能收尾", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteTruncationFrames(rec, nil)
		if !strings.Contains(rec.Body.String(), "data: [DONE]") {
			t.Fatalf("缺少 [DONE]: %s", rec.Body.String())
		}
	})
	t.Run("长错误信息被截断", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteTruncationFrames(rec, errors.New(strings.Repeat("x", 5000)))
		body := rec.Body.String()
		if len(body) > 1000 {
			t.Fatalf("错误信息未截断，帧长 %d", len(body))
		}
		// 截断后仍是合法 JSON 帧（能被客户端解析出 error 对象）。
		if !strings.Contains(body, `"error"`) {
			t.Fatalf("帧结构损坏: %s", body)
		}
	})
}

// TestTruncationErrorCode 错误码归一。
func TestTruncationErrorCode(t *testing.T) {
	if got := TruncationErrorCode(ErrIdleTimeout); got != "upstream_timeout" {
		t.Fatalf("got %q", got)
	}
	if got := TruncationErrorCode(io.EOF); got != "upstream_stream_error" {
		t.Fatalf("got %q", got)
	}
	// 包装后仍应识别（IsIdleTimeout 走 errors.Is）。
	if got := TruncationErrorCode(errors.Join(errors.New("wrap"), ErrIdleTimeout)); got != "upstream_timeout" {
		t.Fatalf("包装后的空闲超时未识别: %q", got)
	}
}
