package config

import (
	"os"
	"path/filepath"
	"testing"
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

// TestQoderContextWindow 上下文档位目标值：缺省 0（跟随上游默认）、负数归一为 0、
// 环境变量覆盖配置文件。
func TestQoderContextWindow(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")

	os.WriteFile(fp, []byte(`{}`), 0o600)
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Compat.QoderContextWindow != 0 {
		t.Fatalf("缺省应为 0，得到 %d", c.Compat.QoderContextWindow)
	}

	os.WriteFile(fp, []byte(`{"compat":{"qoder_context_window":1000000}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Compat.QoderContextWindow != 1000000 {
		t.Fatalf("应读到 1000000，得到 %d", c.Compat.QoderContextWindow)
	}

	os.WriteFile(fp, []byte(`{"compat":{"qoder_context_window":-1}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Compat.QoderContextWindow != 0 {
		t.Fatalf("负数应归一为 0，得到 %d", c.Compat.QoderContextWindow)
	}

	t.Setenv("WILDWORK_QODER_CONTEXT_WINDOW", "400000")
	os.WriteFile(fp, []byte(`{"compat":{"qoder_context_window":1000000}}`), 0o600)
	c, err = Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if c.Compat.QoderContextWindow != 400000 {
		t.Fatalf("环境变量应覆盖配置文件，得到 %d", c.Compat.QoderContextWindow)
	}
}
