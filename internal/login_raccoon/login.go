// Package login_raccoon 实现商汤小浣熊的「协议劫持登录」。
//
// 官方登录链路（逆向自 desktopLogin.js 与服务端 SPA，见 docs/raccoon渠道接入备忘.md §11）：
//
//	① 打开 https://xiaohuanxiong.com/code/authorize?login_source=desktop&appname=办公小浣熊客户端
//	② 用户在系统浏览器完成网页登录
//	③ 页面跳硬编码深链 office-raccoon://auth/callback?code=<授权码>
//	④ 该深链交给 HKCU\Software\Classes\office-raccoon 注册的协议处理器（默认是官方客户端 exe）
//	⑤ 客户端 POST {authApi}/login_with_authorization_code {"authorization_code": code} 兑换 token
//
// 回调地址由服务端前端硬编码、全站 JS 无 redirect_uri，第三方改不了；但**兑换端点不校验调用方身份**
// （无签名头、无设备身份、无鉴权头），谁拿到 code 谁就能换到 token。于是本包的做法是：
// 登录期间把 office-raccoon 协议临时指向 wild-work 自身来截获深链，兑换完成后立刻恢复注册表。
//
// 与「从本机客户端导入」的关系：
//   - 两者最终拿到的是**同一个账号**，但会话语义不同：协议登录新建独立会话（新 sid），
//     可与客户端并存；导入复用客户端同一份凭据（同一 sid），两边会抢着消费同一个 refresh_token；
//   - 导入要求客户端已登录；
//   - 协议登录不依赖客户端登录态，但登录期间官方客户端收不到回调（协议被抢占）；
//     若客户端在此期间启动，它会重新注册协议 → 劫持失效（面板文案与文档均已提示）。
//
// 注册表读写的实现在 internal/raccoon/protocol_windows.go（仅 Windows）。
package login_raccoon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wild-work/internal/raccoon"
)

// ErrPending 表示授权尚未完成（浏览器里还没走完）。
var ErrPending = errors.New("login pending")

// errEndpointMissing 兑换端点不存在，值得回落另一个前缀重试。
var errEndpointMissing = errors.New("auth endpoint missing")

// Result 登录成功后拿到的凭据（uid/nickname 由调用方从 JWT 解出，本包不碰 JWT）。
type Result struct {
	AccessToken    string
	RefreshToken   string
	OfficeIdentity string
	OfficeOrgName  string
	OfficeOrgRole  string
	ExpiresAt      int64
}

// state 落盘的登录状态（含恢复注册表所需信息）。
type state struct {
	ExePath   string `json:"exePath"`
	BackupFP  string `json:"backupFp"`
	AuthURL   string `json:"authUrl"`
	CreatedAt int64  `json:"createdAt"`
}

// NewClient 登录流程用的 HTTP 客户端（兑换是普通 JSON 请求，无 cookie 需求）。
func NewClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// Start 发起登录：劫持协议注册 → 返回授权 URL。
//
// 顺序上先清残留回调文件（避免上次登录的 code 被当成这次的结果），再劫持注册表，
// 最后写 state；任何一步失败都保证不留下「劫持着但没人恢复」的状态。
func Start(_ *http.Client, statePath, stateDir, exePath string) (string, error) {
	// 上一次登录可能留下没人读的回调文件（超时后用户才在浏览器点完），先清掉。
	if err := raccoon.ClearCallback(stateDir); err != nil {
		return "", fmt.Errorf("清理上次登录残留失败：%w", err)
	}
	backupFP := raccoon.BackupPath(stateDir)
	if err := raccoon.HijackProtocol(exePath, backupFP); err != nil {
		return "", err
	}
	st := state{
		ExePath:   exePath,
		BackupFP:  backupFP,
		AuthURL:   raccoon.AuthorizeURL,
		CreatedAt: time.Now().Unix(),
	}
	if err := writeState(statePath, st); err != nil {
		// 状态没落盘就没人能恢复注册表 → 立刻自己回滚。
		if _, rerr := raccoon.RestoreProtocol(backupFP); rerr != nil {
			log.Printf("raccoon 协议恢复失败（需手动检查注册表）：%v", rerr)
		}
		return "", err
	}
	return raccoon.AuthorizeURL, nil
}

// Poll 单次检查登录结果：无回调返回 ErrPending；有回调则兑换 token，并在返回前恢复注册表。
//
// 无论兑换成功还是失败，注册表都会恢复、state 与回调文件都会清理 ——
// 这个「终态必然恢复」的性质是调用方（app.pollLogin）能安全退出的前提。
func Poll(client *http.Client, statePath, stateDir string) (Result, error) {
	if _, err := readState(statePath); err != nil {
		return Result{}, fmt.Errorf("read state: %w", err)
	}
	payload, ok, err := raccoon.LoadCallback(stateDir)
	if err != nil {
		Shutdown(statePath, stateDir)
		return Result{}, err
	}
	if !ok {
		return Result{}, ErrPending
	}
	if payload.Err != "" {
		Shutdown(statePath, stateDir)
		return Result{}, fmt.Errorf("登录回调不可用：%s", payload.Err)
	}
	res, xerr := exchange(raccoon.MainSite, client, payload.Code)
	// 先恢复注册表再返回：即便兑换失败也不能让协议一直指着自己。
	Shutdown(statePath, stateDir)
	if xerr != nil {
		return Result{}, xerr
	}
	return res, nil
}

// Shutdown 恢复协议注册表并清理登录中间文件（幂等：重复调用、无备份、备份已删都不报错）。
func Shutdown(statePath, stateDir string) {
	backupFP := raccoon.BackupPath(stateDir)
	if st, err := readState(statePath); err == nil && strings.TrimSpace(st.BackupFP) != "" {
		backupFP = st.BackupFP
	}
	restored, err := raccoon.RestoreProtocol(backupFP)
	switch {
	case err != nil:
		log.Printf("raccoon 协议注册表恢复失败：%v（若协议仍指向本程序，可重启本程序触发自愈）", err)
	case restored:
		log.Printf("raccoon 协议注册表已恢复原状")
	}
	_ = os.Remove(statePath)
	_ = raccoon.ClearCallback(stateDir)
}

// exchange 用授权码兑换 token：先试 web 面前缀，端点不存在时回落 electron 面。
// baseURL 单独传参，便于测试指向本地 httptest 服务器。
func exchange(baseURL string, client *http.Client, code string) (Result, error) {
	if strings.TrimSpace(code) == "" {
		return Result{}, errors.New("授权码为空")
	}
	body, err := json.Marshal(map[string]string{"authorization_code": code})
	if err != nil {
		return Result{}, err
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = raccoon.MainSite
	}
	eps := []string{raccoon.EpAuthCodeWeb, raccoon.EpAuthCodeElectron}
	var lastErr error
	for i, ep := range eps {
		res, err := exchangeAt(client, base+ep, body)
		if err == nil {
			return res, nil
		}
		lastErr = err
		// 只有「端点不存在」才值得换另一个前缀；业务错误（如授权码已消费）换也没用。
		if i < len(eps)-1 && errors.Is(err, errEndpointMissing) {
			continue
		}
		return Result{}, err
	}
	return Result{}, lastErr
}

// exchangeAt 向单个端点发起兑换。
func exchangeAt(client *http.Client, url string, body []byte) (Result, error) {
	if client == nil {
		client = NewClient()
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("兑换请求失败：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return Result{}, fmt.Errorf("%w（http %d）", errEndpointMissing, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("兑换失败 http %d：%s", resp.StatusCode, truncate(string(raw), 300))
	}

	// 响应信封：{code, msg, data:{access_token, refresh_token, office_identity, ...}}。
	// 业务码 200035 = 授权码不存在/已过期/已消费。
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Result{}, fmt.Errorf("兑换响应解析失败：%w（body: %s）", err, truncate(string(raw), 200))
	}
	if env.Code != 0 {
		return Result{}, fmt.Errorf("兑换被拒：code=%d msg=%s", env.Code, strings.TrimSpace(env.Msg))
	}
	var payload struct {
		AccessToken    string `json:"access_token"`
		RefreshToken   string `json:"refresh_token"`
		OfficeIdentity string `json:"office_identity"`
		OfficeOrgName  string `json:"office_org_name"`
		OfficeOrgRole  string `json:"office_org_role"`
	}
	if len(env.Data) > 0 {
		_ = json.Unmarshal(env.Data, &payload)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		// 兜底：个别版本把凭据直接放在顶层。
		_ = json.Unmarshal(raw, &payload)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return Result{}, errors.New("兑换响应里没有 access_token")
	}
	return Result{
		AccessToken:    strings.TrimSpace(payload.AccessToken),
		RefreshToken:   strings.TrimSpace(payload.RefreshToken),
		OfficeIdentity: strings.TrimSpace(payload.OfficeIdentity),
		OfficeOrgName:  strings.TrimSpace(payload.OfficeOrgName),
		OfficeOrgRole:  strings.TrimSpace(payload.OfficeOrgRole),
		ExpiresAt:      jwtExp(payload.AccessToken),
	}, nil
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

func readState(path string) (state, error) {
	var st state
	raw, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(raw, &st)
	return st, err
}

func writeState(path string, st state) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// jwtExp 取 JWT 的 exp（秒）；解析失败返回 0（交由刷新逻辑兜底）。
func jwtExp(tok string) int64 {
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) < 2 {
		return 0
	}
	seg := parts[1]
	if m := len(seg) % 4; m != 0 {
		seg += strings.Repeat("=", 4-m)
	}
	dec, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return 0
	}
	var m map[string]any
	if json.Unmarshal(dec, &m) != nil {
		return 0
	}
	if f, ok := m["exp"].(float64); ok {
		return int64(f)
	}
	return 0
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
