package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Listen.Addr() != "127.0.0.1:7863" {
		t.Errorf("listen=%s", c.Listen.Addr())
	}
	if c.APIKey != "WildWorkAPI" {
		t.Errorf("api_key=%q", c.APIKey)
	}
	if err := c.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.HardCreditDur.Hours() != 12 {
		t.Errorf("hard=%v", c.HardCreditDur)
	}
}

func TestListenParsing(t *testing.T) {
	cases := []struct {
		in   string
		addr string
	}{
		{":9999", ":9999"},
		{"127.0.0.1:9999", "127.0.0.1:9999"},
		{"9999", ":9999"},
		{"0.0.0.0:7863", "0.0.0.0:7863"},
	}
	for _, c := range cases {
		l, err := ParseListen(c.in)
		if err != nil {
			t.Errorf("ParseListen(%q): %v", c.in, err)
			continue
		}
		if got := l.Addr(); got != c.addr {
			t.Errorf("ParseListen(%q).Addr()=%s want %s", c.in, got, c.addr)
		}
	}
	if _, err := ParseListen(":notaport"); err == nil {
		t.Error("want error for bad port")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	os.WriteFile(fp, []byte(`{"listen":"127.0.0.1:9999","api_key":"k","region":"cn"}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Addr() != "127.0.0.1:9999" || c.APIKey != "k" {
		t.Errorf("c=%+v", c)
	}
}

func TestLoadFileObjectListen(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	os.WriteFile(fp, []byte(`{"listen":{"host":"192.168.1.2","port":8000}}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Addr() != "192.168.1.2:8000" {
		t.Errorf("addr=%s", c.Listen.Addr())
	}
}

func TestSaveRoundtrip(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "sub", "c.json")
	c := Default()
	c.Listen.Host = "127.0.0.1"
	c.Schedule.CheckinHours = []int{8, 20}
	if err := Save(c, fp); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Listen.Addr() != "127.0.0.1:7863" {
		t.Errorf("addr=%s", c2.Listen.Addr())
	}
	if len(c2.Schedule.CheckinHours) != 2 || c2.Schedule.CheckinHours[0] != 8 {
		t.Errorf("hours=%v", c2.Schedule.CheckinHours)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("WILDWORK_LISTEN", ":7777")
	t.Setenv("WILDWORK_API_KEY", "envkey")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Addr() != ":7777" || c.APIKey != "envkey" {
		t.Errorf("c=%+v", c)
	}
}

func TestClockTimes(t *testing.T) {
	got, err := ParseClockTimes([]string{"21:30", "09:05", "09:05"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 9*60+5 || got[1] != 21*60+30 {
		t.Fatalf("minutes=%v", got)
	}
	if _, err := ParseClockTimes([]string{"24:00"}); err == nil {
		t.Fatal("want invalid time error")
	}
	if got := FormatClockTimes(got); got[0] != "09:05" || got[1] != "21:30" {
		t.Fatalf("times=%v", got)
	}
}

// 空输入必须返回**非 nil** 空切片：scheduler.New 用 nil 表示「未配置」并补成默认
// 9:00/21:00，若这里退化成 nil，用户就无法通过在配置里写 "checkin_times": [] 关掉签到。
func TestClockTimesEmptyIsNonNil(t *testing.T) {
	for _, in := range [][]string{nil, {}} {
		got, err := ParseClockTimes(in)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatalf("ParseClockTimes(%v) 返回 nil，会让 scheduler.New 补成默认签到时段", in)
		}
		if len(got) != 0 {
			t.Fatalf("ParseClockTimes(%v) = %v，want 空", in, got)
		}
	}
}

func TestLoadLegacyCheckinHours(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	os.WriteFile(fp, []byte(`{"schedule":{"checkin_hours":[8,20]}}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Schedule.CheckinTimes; len(got) != 2 || got[0] != "08:00" || got[1] != "20:00" {
		t.Fatalf("times=%v", got)
	}
}

func TestLoadMinuteCheckinTimes(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	os.WriteFile(fp, []byte(`{"schedule":{"checkin_times":["09:05","21:30"]}}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Schedule.CheckinTimes; len(got) != 2 || got[0] != "09:05" || got[1] != "21:30" {
		t.Fatalf("times=%v", got)
	}
}

func TestAdminPasswordAndLoopback(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	// 旧配置（无 admin_password）必须能加载，且默认空 = 面板不鉴权
	os.WriteFile(fp, []byte(`{"listen":"127.0.0.1:9999"}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminPass != "" {
		t.Errorf("缺省 admin_password 应为空，实际 %q", c.AdminPass)
	}
	// 新字段可读
	os.WriteFile(fp, []byte(`{"listen":{"host":"0.0.0.0","port":8000},"admin_password":"pw12345678"}`), 0o600)
	if c, err = Load(fp); err != nil {
		t.Fatal(err)
	} else if c.AdminPass != "pw12345678" {
		t.Errorf("admin_password=%q", c.AdminPass)
	}
	// env 覆盖
	t.Setenv("WILDWORK_ADMIN_PASSWORD", "env-pw")
	if c, err = Load(fp); err != nil || c.AdminPass != "env-pw" {
		t.Errorf("env override 失败：%v %q", err, c.AdminPass)
	}
	// IsLoopback
	for host, want := range map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true, "0.0.0.0": false, "": false, "192.168.1.9": false} {
		if got := (Listen{Host: host}).IsLoopback(); got != want {
			t.Errorf("IsLoopback(%q)=%v want %v", host, got, want)
		}
	}
}

func TestBadDuration(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	os.WriteFile(fp, []byte(`{"cooldown":{"hard_credit":"not-a-duration"}}`), 0o600)
	if _, err := Load(fp); err == nil {
		t.Fatal("want error for bad duration")
	}
}

// 静态兜底表开关：字段缺失视为启用（旧配置/旧前端不得静默关掉该能力）。
func TestStaticEffortFallbackEnabled(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")

	os.WriteFile(fp, []byte(`{}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !c.StaticEffortFallbackEnabled() {
		t.Fatal("字段缺失应视为启用")
	}

	os.WriteFile(fp, []byte(`{"compat":{"static_effort_fallback":false}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.StaticEffortFallbackEnabled() {
		t.Fatal("显式 false 应关闭")
	}

	t.Setenv("WILDWORK_STATIC_EFFORT_FALLBACK", "on")
	os.WriteFile(fp, []byte(`{"compat":{"static_effort_fallback":false}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !c.StaticEffortFallbackEnabled() {
		t.Fatal("环境变量应覆盖配置文件")
	}
}

// TestContextWindows 逐模型上下文档位：非法项清洗、环境变量覆盖配置文件。
func TestContextWindows(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")

	os.WriteFile(fp, []byte(`{}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Compat.ContextWindows) != 0 {
		t.Fatalf("缺省应为空，得到 %v", c.Compat.ContextWindows)
	}

	// 合法项保留，非法项（缺 /、值非正）清洗掉
	os.WriteFile(fp, []byte(`{"compat":{"context_windows":{"qoder/qwen3.8-flash":1000000,"bad":200000,"qoder/glm-5.3":0}}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Compat.ContextWindows) != 1 || c.Compat.ContextWindows["qoder/qwen3.8-flash"] != 1000000 {
		t.Fatalf("应只保留合法项，得到 %v", c.Compat.ContextWindows)
	}

	// 环境变量整体覆盖
	t.Setenv("WILDWORK_CONTEXT_WINDOWS", "qoder/glm-5.3=400000, qoder/qwen3.8-flash=1000000 ,bad")
	os.WriteFile(fp, []byte(`{"compat":{"context_windows":{"qoder/qwen3.8-flash":200000}}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Compat.ContextWindows["qoder/qwen3.8-flash"] != 1000000 {
		t.Fatalf("环境变量应覆盖配置文件，得到 %v", c.Compat.ContextWindows)
	}
	if c.Compat.ContextWindows["qoder/glm-5.3"] != 400000 {
		t.Fatalf("第二个条目应解析，得到 %v", c.Compat.ContextWindows)
	}
	if len(c.Compat.ContextWindows) != 2 {
		t.Fatalf("非法片段应跳过，得到 %v", c.Compat.ContextWindows)
	}
}

// TestStreamIdleSeconds 守门：流式空闲超时（loomy/raccoon 去掉整请求总超时后的唯一兜底）。
//
// 背景：http.Client.Timeout 是整请求上限，会把长思考的 SSE 流从中间掐断
// （实测 loomy 两次中断都恰好 120.00s）。流式改用无总超时 client 后，
// 必须由本项承担「上游卡死」的检测。
func TestStreamIdleSeconds(t *testing.T) {
	t.Run("默认 90s", func(t *testing.T) {
		c := Default()
		if err := c.normalize(); err != nil {
			t.Fatal(err)
		}
		if c.Upstream.StreamIdleSeconds != 90 {
			t.Fatalf("默认 = %d, want 90", c.Upstream.StreamIdleSeconds)
		}
		if c.StreamIdleDur != 90*time.Second {
			t.Fatalf("解析值 = %v, want 90s", c.StreamIdleDur)
		}
	})
	t.Run("未配置时补默认", func(t *testing.T) {
		c := Default()
		c.Upstream.StreamIdleSeconds = 0
		if err := c.normalize(); err != nil {
			t.Fatal(err)
		}
		if c.Upstream.StreamIdleSeconds != 90 {
			t.Fatalf("零值应补 90，得到 %d", c.Upstream.StreamIdleSeconds)
		}
	})
	t.Run("钳下限 10s（过小会误杀思考期静默）", func(t *testing.T) {
		c := Default()
		c.Upstream.StreamIdleSeconds = 3
		if err := c.normalize(); err != nil {
			t.Fatal(err)
		}
		if c.Upstream.StreamIdleSeconds != 10 {
			t.Fatalf("应钳到 10，得到 %d", c.Upstream.StreamIdleSeconds)
		}
		if c.StreamIdleDur != 10*time.Second {
			t.Fatalf("解析值 = %v, want 10s", c.StreamIdleDur)
		}
	})
	t.Run("合法值原样保留", func(t *testing.T) {
		c := Default()
		c.Upstream.StreamIdleSeconds = 300
		if err := c.normalize(); err != nil {
			t.Fatal(err)
		}
		if c.StreamIdleDur != 300*time.Second {
			t.Fatalf("解析值 = %v, want 300s", c.StreamIdleDur)
		}
	})
	t.Run("环境变量覆盖", func(t *testing.T) {
		t.Setenv("WILDWORK_STREAM_IDLE_SECONDS", "45")
		c, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if c.Upstream.StreamIdleSeconds != 45 || c.StreamIdleDur != 45*time.Second {
			t.Fatalf("env 未生效: %d / %v", c.Upstream.StreamIdleSeconds, c.StreamIdleDur)
		}
	})
}
