package login_raccoon

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wild-work/internal/raccoon"
)

// fakeJWT 造一个仅含 payload 的假 JWT（jwtExp 只解 payload，不验签）。
func fakeJWT(t *testing.T, exp int64) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"exp": exp, "name": "tester"})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// TestExchangeSuccess 正常兑换：按约定字段发授权码、解析 data 信封、取到 exp。
func TestExchangeSuccess(t *testing.T) {
	const exp = int64(1800000000)
	tok := fakeJWT(t, exp)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != raccoon.EpAuthCodeWeb {
			t.Errorf("请求路径不符：%s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("期望 POST，实际 %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type 不符：%s", ct)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求体解析失败：%v", err)
		}
		if body["authorization_code"] != "code-1" {
			t.Errorf("授权码字段不符：%v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"access_token":    tok,
				"refresh_token":   "rt-1",
				"office_identity": "org-1",
				"office_org_name": "测试组织",
				"office_org_role": "owner",
			},
		})
	}))
	defer srv.Close()

	r, err := exchange(srv.URL, srv.Client(), "code-1")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if r.AccessToken != tok || r.RefreshToken != "rt-1" || r.OfficeIdentity != "org-1" {
		t.Fatalf("凭据解析不符：%+v", r)
	}
	if r.ExpiresAt != exp {
		t.Fatalf("exp 期望 %d 实际 %d", exp, r.ExpiresAt)
	}
}

// TestExchangeBusinessError 业务码非 0（如 200035 = 授权码已消费）应立即失败，
// 且不再拿同一个 code 去试另一个前缀（换端点也没用，只会白跑一次请求）。
func TestExchangeBusinessError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200035,
			"msg":  "authorization code invalid",
		})
	}))
	defer srv.Close()

	_, err := exchange(srv.URL, srv.Client(), "code-1")
	if err == nil {
		t.Fatal("期望失败")
	}
	if !strings.Contains(err.Error(), "200035") {
		t.Fatalf("错误应带上业务码：%v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("业务错误不应回落重试，实际请求 %d 次", n)
	}
}

// TestExchangeFallsBackOn404 端点不存在（404）时回落到另一个前缀。
func TestExchangeFallsBackOn404(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == raccoon.EpAuthCodeWeb {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"access_token": fakeJWT(t, 1), "refresh_token": "rt"},
		})
	}))
	defer srv.Close()

	r, err := exchange(srv.URL, srv.Client(), "code-1")
	if err != nil {
		t.Fatalf("exchange 应回落成功：%v", err)
	}
	if r.RefreshToken != "rt" {
		t.Fatalf("回落结果不符：%+v", r)
	}
	if len(paths) != 2 || paths[0] != raccoon.EpAuthCodeWeb || paths[1] != raccoon.EpAuthCodeElectron {
		t.Fatalf("回落路径不符：%v", paths)
	}
}

// TestExchangeNoToken 响应里没有 access_token 必须报错，不能当成成功。
func TestExchangeNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	defer srv.Close()

	_, err := exchange(srv.URL, srv.Client(), "code-1")
	if err == nil || !strings.Contains(err.Error(), "access_token") {
		t.Fatalf("期望缺 token 报错，实际 %v", err)
	}
}

// TestExchangeTopLevelToken 兼容个别版本把凭据直接放在顶层。
func TestExchangeTopLevelToken(t *testing.T) {
	tok := fakeJWT(t, 42)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "access_token": tok, "refresh_token": "rt"})
	}))
	defer srv.Close()

	r, err := exchange(srv.URL, srv.Client(), "code-1")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if r.AccessToken != tok {
		t.Fatalf("顶层 token 未兜底解析：%+v", r)
	}
}

// TestExchangeEmptyCode 空授权码直接拒绝，不发请求。
func TestExchangeEmptyCode(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer srv.Close()

	if _, err := exchange(srv.URL, srv.Client(), "  "); err == nil {
		t.Fatal("空授权码应报错")
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("空授权码不应发请求，实际 %d 次", n)
	}
}

// TestJwtExp 覆盖 exp 解析（含非法输入不得 panic）。
func TestJwtExp(t *testing.T) {
	if got := jwtExp(fakeJWT(t, 123456)); got != 123456 {
		t.Fatalf("jwtExp 期望 123456 实际 %d", got)
	}
	for _, bad := range []string{"", "not-a-jwt", "a.b", "a.!!!.c"} {
		if got := jwtExp(bad); got != 0 {
			t.Fatalf("jwtExp(%q) 期望 0 实际 %d", bad, got)
		}
	}
}

// TestShutdownIdempotent 无 state、无备份时 Shutdown 必须静默成功（幂等）。
func TestShutdownIdempotent(t *testing.T) {
	dir := t.TempDir()
	statePath := dir + "/login-state.json"
	Shutdown(statePath, dir)
	Shutdown(statePath, dir)
}
