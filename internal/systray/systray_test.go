package systray

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"
	"time"
)

// 菜单项图标必须是 ICO 容器：Windows 的 LoadImage(IMAGE_ICON) 不认裸 PNG。
func TestMenuIconIsICO(t *testing.T) {
	ico := menuIcon(37, 99, 235)
	if len(ico) < 22 {
		t.Fatalf("ICO 太短：%d 字节", len(ico))
	}
	if got := binary.LittleEndian.Uint16(ico[0:2]); got != 0 {
		t.Errorf("ICONDIR.reserved 应为 0，得到 %d", got)
	}
	if got := binary.LittleEndian.Uint16(ico[2:4]); got != 1 {
		t.Errorf("ICONDIR.type 应为 1(icon)，得到 %d", got)
	}
	if got := binary.LittleEndian.Uint16(ico[4:6]); got != 1 {
		t.Errorf("ICONDIR.count 应为 1，得到 %d", got)
	}
	if ico[6] != 16 || ico[7] != 16 {
		t.Errorf("条目尺寸应为 16x16，得到 %dx%d", ico[6], ico[7])
	}
	size := binary.LittleEndian.Uint32(ico[14:18])
	off := binary.LittleEndian.Uint32(ico[18:22])
	if int(off)+int(size) != len(ico) {
		t.Fatalf("数据区不吻合：off=%d size=%d 总长=%d", off, size, len(ico))
	}
	if !bytes.Equal(ico[off:off+8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Errorf("条目数据应为 PNG（PNG 签名），实际 % x", ico[off:off+8])
	}

	// 条目里的 PNG 必须真能解码：旧的手写 PNG 编码器产出的字节解不开，Windows 因此
	// 加载失败（png.Decode 报 invalid checksum），菜单项图标一直不显示。
	img, err := png.Decode(bytes.NewReader(ico[off : off+size]))
	if err != nil {
		t.Fatalf("条目数据不是合法 PNG：%v", err)
	}
	if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 16 {
		t.Fatalf("解出的尺寸应为 16x16，得到 %dx%d", b.Dx(), b.Dy())
	}
	corner := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA)
	if corner.R != 255 || corner.G != 255 || corner.B != 255 {
		t.Errorf("四边应为白色，左上角得到 %v", corner)
	}
	center := color.RGBAModel.Convert(img.At(8, 8)).(color.RGBA)
	if center.R != 37 || center.G != 99 || center.B != 235 {
		t.Errorf("中心应为传入颜色 37,99,235，得到 %v", center)
	}
}

// 未启动托盘（--no-tray / 初始化失败）时 Quit 立即返回，不拖慢进程退出。
func TestQuitWithoutTray(t *testing.T) {
	start := time.Now()
	if !Quit(5 * time.Second) {
		t.Fatal("未启动托盘时 Quit 应返回 true")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("未启动托盘时不应等待，实际耗时 %v", d)
	}
}

// 消息循环未响应（图标回收未确认）时按超时返回 false，不阻塞退出。
func TestQuitTimesOutWithoutLoop(t *testing.T) {
	trayStarted.Store(true)
	t.Cleanup(func() { trayStarted.Store(false) })

	start := time.Now()
	if Quit(150 * time.Millisecond) {
		t.Fatal("消息循环未响应时应返回 false")
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("应等待到超时，实际 %v", d)
	}
}

// 图标回收完成后 Quit 不再等待（doneCh 是消息循环执行完 NIM_DELETE 的信号）。
func TestQuitReturnsOnDone(t *testing.T) {
	trayStarted.Store(true)
	doneOnce.Do(func() { close(doneCh) })
	t.Cleanup(func() { trayStarted.Store(false) })

	start := time.Now()
	if !Quit(5 * time.Second) {
		t.Fatal("回收完成后 Quit 应返回 true")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("回收完成后不应等待，实际耗时 %v", d)
	}
}
