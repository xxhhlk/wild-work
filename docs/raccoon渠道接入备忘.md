# 商汤小浣熊（raccoon-ai）渠道接入备忘（阶段 A 静态取证）

> 状态：**阶段 A 静态取证完成，待运行时取证（A3）**
> 取证日期：2026-09-22 ｜ 客户端：`C:\Program Files\raccoon-ai`（Electron，**商汤小浣熊**）
> 依据技能：`electron-ai-client-protocol-extract`
> **本文档不含任何 token / apiKey / 密钥值**，只记录端点、字段名与机制。

---

## 1. 一句话结论

小浣熊是**商汤**的 AI 办公/分析客户端，模型走**官方托管网关 `xiaohuanxiong.com/api/web/llm/v2`（OpenAI 兼容）**，
凭据落在**明文 JSON `~/.box-agent/config/auth.json`**（`access_token` + `refresh_token` + 组织身份），
**不需要 DPAPI / OSCrypt 解密** —— 这是本次两个渠道里**取证成本最低、最可直接复现**的一个。

---

## 2. 安装位置与结构

| 项 | 值 |
|---|---|
| 安装目录 | `C:\Program Files\raccoon-ai` |
| 主程序 | `商汤小浣熊.exe` |
| 主包 | `resources\app.asar`（**259 MB / 9679 条目**） |
| 其他资源 | `app\`（6.3 MB，前端静态）、`config\`（branding）、`connectors\`（20 个 MCP 连接器）、`cli-bundle\`(94 MB)、`box-agent-runtime\`(250 MB)、`browser-tools\`、`browser-gateway\` |
| userData | `%APPDATA%\office-raccoon`（`local-chat.sqlite3`、`logs\`、`settings.json`、`desktop-device-identity.json`、`mnt\`、`mobile-relay\`） |
| **agent 家目录** | `%USERPROFILE%\.box-agent\`（`config\`、`sessions\`、`memory\`、`skills\`、`sandbox\`、`browsers\`） |

**branding**：`config\branding.json` → `productName: "商汤小浣熊"`；另含 `branding-zhihu.json`（**知乎定制版 504 KB**）。

---

## 3. 端点表（`resources\app.asar → /.env.electron` 生产配置）

| 用途 | 值 |
|---|---|
| 主站 / API | `https://xiaohuanxiong.com` |
| **AI / LLM baseURL** | `https://xiaohuanxiong.com/api/web/llm/v2` |
| **桌面认证前缀** | `/api/electron/auth/v1`（另有 web 面 `/api/web/auth/v1`） |
| 基础 / 业务 API | `/api/web` ｜ `/api/web/office/v3` |
| 组织 | `/api/web/org`、`/api/web/org/user` |
| 团队服务 | `https://xiaohuanxiong.com/agentapi` |
| 协作长连 | `wss://collab-server.xiaohuanxiong.com` |
| 更新 CDN | `https://sta-volc.xiaohuanxiong.com/office-agent/{win,mac,linux}`（**火山引擎**） |
| 控制台 / 文档 | `https://office-console.xiaohuanxiong.com` ｜ `https://doc.xiaohuanxiong.com` |
| 登录页 | `https://xiaohuanxiong.com/login` |
| Cookie | 前缀 `electron_`，domain `localhost` |

关键开关：`NEXT_PUBLIC_IS_ELECTRON=true`、`NEXT_PUBLIC_DESKTOP_HEADER_AUTH=true`、
`NEXT_PUBLIC_DESKTOP_CLIENT_AUTH=true`、`NEXT_PUBLIC_DESKTOP_APP_MODE=online_plus`、
`NEXT_PUBLIC_DESKTOP_TOKEN_BRIDGE=<bool>`、`CORE` 渲染开关 `RACCOON_USE_DESKTOP_RENDERER=true`。

> `/.env`（开发版）指向 `code-dev.xiaohuanxiong.com`；两者都**不含**内置 LLM key。

### 3.1 官方托管 vs 自定义 provider

`build\electron\main\hostedGateway.js`：官方托管 = host 属于 `xiaohuanxiong.com`（或内网 IP `10.158.136.99`）；
用户自定义 URL **不被改写**。
→ **本渠道按 D3 只做官方托管路径**。

---

## 4. 推理请求形状

`build\desktop-renderer\_next\static\chunks\47108-*.js`（renderer 实测）：

| 项 | 值 |
|---|---|
| 模型目录 | `GET {base}/api/web/llm/v2/model_catalog`（`fetchWithAuth`，timeout 10s） |
| 对话 | `POST {base}/chat/completions`（base 已含 `/api/web/llm/v2`） |
| Anthropic 模式 | 同 base 走 `/messages`（provider 常量：`remote_server` / `openai` / `anthropic`） |
| 图像生成 | `POST https://xiaohuanxiong.com/api/web/llm/v2/images/gen` |
| 默认模型 | **`raccoon-chat-ml-5-5`**（`default-llm-config.json` 与 `~/.box-agent/config/config.yaml` 一致） |
| provider | `openai`（`/chat/completions`）；`anthropic` 时改 `/messages` |
| 上下文/输出 | `contextWindow` 默认 **180000**；`maxTokens` 官方托管默认 **80000**；`timeout` 1200s |

`.env.electron` 内含 `TEAM_SERVICE_URL=https://xiaohuanxiong.com/agentapi`（团队额度相关）。

---

## 5. 凭据机制（**核心结论**）

### 5.1 凭据文件：明文 JSON

`build\electron\main\boxAgentAuthFile.js`：

```
路径：%USERPROFILE%\.box-agent\config\auth.json      # 可被 BOX_AGENT_CONFIG_DIR 覆盖
格式：{ "access_token": "...", "refresh_token": "...",
        "office_identity": "...", "office_org_name": "...", "office_org_role": "..." }
权限：0600（Windows 上不生效，源码已注明）
写入：tmp + rename 原子替换
```

**实测存在**：`C:\Users\testuser\.box-agent\config\auth.json`（841 B，2026-09-22 20:10 更新）。
→ **无加密、无 DPAPI 绑定**，跨账户可读（同一用户目录下）。

### 5.2 凭据流转

`build\electron\main\desktopAuthState.js` + `main.js` + **`desktopLogin.js`**：

```
点登录 → IPC desktop-auth:start-login-from-wall → desktopLogin.openDesktopLogin()
  → shell.openExternal("https://xiaohuanxiong.com/code/authorize
                        ?login_source=desktop&appname=办公小浣熊客户端")
  → 用户在系统浏览器完成网页登录
  → 网页前端跳深链：office-raccoon://auth/callback?code=<授权码>&state=<可选>
  → 客户端收深链（queueDesktopLogin → parseDesktopLoginCode）
  → POST {authApi}/login_with_authorization_code  {"authorization_code": "<code>"}
  → resp.data = {access_token, refresh_token, office_identity, office_org_name, office_org_role}
  → updateDesktopAuthState(...) 写 auth.json

主进程是权威源：resolveAuthoritativeAccessToken() 忽略 renderer 传来的过期 token，读文件为准
预热/刷新入口：IPC desktop-auth:ensure-access-token-ready
             → scheduleAuth.prepareBoxAgentAuthForWarmup()
             → 失败则 clearDesktopAuthState()
另有：box-agent-auth:sync(token) 把凭据同步给 box-agent runtime 进程
```

- **存在 `refresh_token`** → 支持刷新（相对 Loomy 的关键优势）
- ⚠️ **这是「登录即授权」的网页授权码流程**（2026-09-23 更正，完整链路见 §11）：
  - 授权入口固定：`GET https://xiaohuanxiong.com/code/authorize?login_source=desktop&appname=办公小浣熊客户端`
    （服务端返回 SPA shell，跳转逻辑在前端 JS 里）
  - **回调地址硬编码**为 `office-raccoon://auth/callback`（服务端 SPA 的 `hl()` 里
    `new URL("office-raccoon://auth/callback")`），全站 JS **零处** `redirect_uri` → 第三方**改不了回调地址**
  - 兑换端点 `POST {authApi}/login_with_authorization_code`：请求头只有 `Content-Type`、
    body 只有 `{authorization_code}` → **无额外鉴权**（谁拿到 code 谁就能兑换）
  - `{authApi}` = `getAuthApiUrl()`：**优先** `NEXT_PUBLIC_DESKTOP_REMOTE_AUTH_API_PREFIX`（`/api/web/auth/v1`），
    仅当其为空才回落 `NEXT_PUBLIC_AUTH_API_PREFIX`（`/api/electron/auth/v1`）；两个前缀实测都可用
  - 授权码一次性，消费即失效（错误码 `200035`）；兑换超时 15s

### 5.3 agent 侧模型档案

`build\electron\main\modelProfileRegistry.js`：

```
路径：%USERPROFILE%\.box-agent\config\model-profiles.json
profile：{ profileId, provider, apiBase, apiKey, authFile, defaultModel,
           contextWindow, maxTokens, timeout }
官方托管时：apiKey = "box-agent-auth-json"（占位），authFile = <auth.json 路径>
profileId = "hosted:<sha256(apiBase)[:16]>"
```

实测 `model-profiles.json` 存在（628 B）。

---

## 6. 已知 MCP 连接器（`resources\connectors\`）

`baixiao` / `bugly-token` / `cisp` / `datayes-data` / `deeplink` / `dnb-global-data` / `fazhi` /
`h3c-cloudnet` / `ifind` / `jiandaoyun` / `kuaicha` / `opendata` / `picset-commerce-images` /
`picset-video-generation` / `pkulaw` / `qichacha` / `qixin` / `wind-finance` / `xmind`
→ 定位是**商业/金融/法务分析助手**，与工业渠道（WorkBuddy/Trae/Qoder）场景不同。

---

## 7. 映射到 wild-work 的实现要点（草案）

| 项 | 方案 |
|---|---|
| Kind / 前缀 | `raccoon`（`raccoon/<model>`） |
| auth 文件 | `auths/raccoon-<uid>.json`；导入器读 `~/.box-agent/config/auth.json` 后转换（R6.3 嵌套格式） |
| 登录形态 | **两条并存**（2026-09-23）：①**协议劫持登录**（面板主按钮）—— 登录期间把 `office-raccoon` 协议临时指向本工具截获授权码、自调 `login_with_authorization_code`，结束即恢复注册表（见 §11.3 / §12）；②**导入器**（弹窗次按钮）—— 读 `~/.box-agent/config/auth.json` |
| 推理 | `POST https://xiaohuanxiong.com/api/web/llm/v2/chat/completions` |
| 模型表 | 动态 `GET /api/web/llm/v2/model_catalog`；兜底 `raccoon-chat-ml-5-5` |
| 积分 | **`GET /api/web/points/v1/balance`** → `{available_points, daily_points, monthly_points, reward_points, topup_points, topup_frozen}` |
| 签到 | 未发现签到概念（有 `schedule_tasks.db`，需 A3 确认） |
| 思考档位 | **不投影且主动剥离** `reasoning_effort`：网关认该字段，但实测默认档最深、下发反而削弱（见 §13） |
| DPAPI | **不需要**（凭据明文），Windows-only 隔离要求降低 |

---

## 8. 未验证项（留待 A3 运行时取证）

1. `fetchWithAuth` 的**真实鉴权头**（`Authorization: Bearer` / Cookie `electron_*` / 双写）
2. `chat/completions` 的**请求体与响应 SSE 形态**（字段名、usage、是否带 reasoning）
3. **`refresh_token` 的刷新端点与有效期**（决定 B 门禁第 1、3 条）
4. access token 有效期与滑动策略
5. ~~积分/额度端点~~ **已解决**（2026-09-22 深夜，用户指出客户端本来就能看积分）：端点 `/api/web/points/v1/balance`，路径来自 renderer chunk `75741-*.js` 的
   `class extends ... static get baseUrl(){return `${origin}/api/web/points/v1`}`；**它不在 `/api/web/llm/v2` 前缀下**，我最初只在 llm/org/agentapi 前缀里找，故漏掉。
6. `model_catalog` 返回的完整模型清单与能力（图片/工具/上下文）
7. 是否需要设备指纹（`desktop-device-identity.json` 与请求是否关联）
8. 上游条款态度（`model-bridge` 规则 1）

---

## 9. 取证依据（文件 → 结论）

| 文件 | 得到的结论 |
|---|---|
| `app.asar:/.env.electron` | 全部端点与桌面认证前缀（生产） |
| `app.asar:/.env` | 开发环境端点（code-dev） |
| `resources\default-llm-config.json` | 默认 baseURL / provider / 模型 `raccoon-chat-ml-5-5` |
| `app.asar:/build/electron/main/hostedGateway.js` | 官方托管域名判定规则 |
| `app.asar:/build/electron/main/boxAgentAuthFile.js` | **凭据文件路径与字段名（明文 JSON）** |
| `app.asar:/build/electron/main/desktopAuthState.js` | 主进程凭据权威源、信任来源判定 |
| `app.asar:/build/electron/main/modelProfileRegistry.js` | `model-profiles.json` 结构与 hosted 占位 apiKey |
| `app.asar:/build/electron/main.js` | `desktop-auth:ensure-access-token-ready` → 预热/刷新链路 |
| `app.asar:/build/desktop-renderer/.../47108-*.js` | `model_catalog`、`/chat/completions`、provider 常量 |
| `~\.box-agent\config\config.yaml` | 实测生效配置（官方托管预设、工作目录、图片端点） |
| `~\.box-agent\config\auth.json` | 文件存在（841 B）→ 明文凭据路径成立 |

> 附带说明：客户端自带的 `.env` / `.env.electron` 里混有若干**厂商级密钥**
> （飞书 App Secret、火山 ASR token、Apple 公证口令、署名开发者邮箱等）。
> 本次**只采集端点与开关**，这些值一律未记录、未落库。

---

## 10. A3 运行时实测结果（2026-09-22 22:05，真实账号）

> 脚本：`_probe/a3-probe.mjs`；原始响应落 `_probe/out/raccoon-model_catalog.json`。
> **token 全程未打印、未落库。**

**结论：端点 / 鉴权 / 模型目录 / 推理 全部实测通过；凭据刷新链路已从官方生产代码确认。**

| 验证项 | 结果 |
|---|---|
| `GET /api/web/llm/v2/model_catalog` | **200**，返回 8 个模型 |
| `POST /api/web/llm/v2/chat/completions` | **200**，标准 OpenAI 形状（`choices[].message.content` + `usage`） |
| **鉴权头形态（矩阵实测）** | `Authorization: Bearer <access_token>` → **200**；<br>`token:` / `X-Access-Token:` / `Cookie: electron_token=` → **401 `200001 authorization_empty_error`**（只认 `Authorization`） |
| 模型身份核对 | 请求 `raccoon-chat-ml-5-5`（旧别名）→ 响应回填 **`raccoon-8c4485`**（真实默认模型） |
| 凭据字段（实测存在） | `~/.box-agent/config/auth.json`：`access_token`(380, JWT) / `refresh_token`(380, JWT) / `office_identity`(8) |
| **access_token 有效期** | JWT `exp` 实测 ≈ **2 小时**（`nbf`+ 约 2h 窗口） |
| **refresh_token 有效期** | JWT `exp` 实测 ≈ **30 天** |
| JWT payload 字段 | `{exp, iss, jti, name, nation_code, owner_type:"users", sid}`（无 uid；`name` 为昵称） |
| 响应附加字段 | `choices[].message.provider_specific_fields`、`usage.completion_tokens_details/prompt_tokens_details` |

**实测模型表（8 个，`type=chat`，默认 `raccoon-8c4485`）**：

| model_name | 说明 | visible | ability | 倍率 | ctx | max_out | 标签 |
|---|---|---|---|---|---|---|---|
| `raccoon-8c4485` | Raccoon-Work（默认） | false | 3 | 1 | 1000000 | 100000 | office/code/**vision**/debug/analysis/html/**reasoning**/auto |
| `raccoon-19b265` | Raccoon-Work-260817-A | false | 3 | 1 | 1000000 | 100000 | …/auto |
| `raccoon-405a1c` | Raccoon-Work-260817-B | false | 2 | 1 | 1000000 | 100000 | fast |
| `sn-sensenova-6-8-flash-lite` | SenseNova-6.8-Flash-Lite | **true** | 1 | 0.5 | 256000 | 63999 | vision/fast |
| `sn-glm-5-3` | GLM-5-3 | **true** | 2 | 0.75 | 1000000 | 100000 | reasoning |
| `sn-kimi-k3` | Kimi-K3 | **true** | 2 | 1 | 1000000 | 100000 | reasoning |
| `sn-glm-5-3-flash` | GLM-5-3-Flash | **true** | 2 | 0.2 | 1000000 | 100000 | lite |
| `sn-deepseek-v4-1-flash` | DeepSeek-V4.1-Flash | **true** | 3 | 0.25 | 1000000 | 100000 | reasoning/auto |

> **费率数据源确定**：`billing_multiplier`（0.2–1）+ `billing_category`（normal/lite）→ 直接喂 `FetchModelPricing`。
> `visible:false` 的 3 个是内部模型（UI 不展示但**可调用**）→ 渠道应支持但标注。
> **模型目录未下发 `reasoning_efforts`** → 不声明档位能力（`SupportsEffortKind` 不含 raccoon）。
> ⚠️ 但**网关仍认** `reasoning_effort`（SDK 声明域 low/medium/high），且实测**默认档最深**：
> 客户端/面板若下发该字段会削弱思考量 → 渠道层必须**主动剥离**（见 §13，2026-09-24 实测修正）。

### 凭据刷新链路（官方生产代码确认，`build/electron/main/scheduleAuth.js`）

```
POST {authOrigin}/refresh
     authOrigin = NEXT_PUBLIC_DESKTOP_REMOTE_MAIN_SITE_URL = https://xiaohuanxiong.com
     prefix     = NEXT_PUBLIC_DESKTOP_REMOTE_AUTH_API_PREFIX = /api/electron/auth/v1
     （代码兜底默认值：/api/web/auth/v1 —— 两个前缀都应支持）
body: {"refresh_token": "<refresh_token>"}
resp: { data: { access_token, refresh_token } }      ← refresh_token 会轮换，必须落盘
```

- **刷新窗口 300 秒**（`TOKEN_REFRESH_WINDOW_SECONDS`，过期前 5 分钟触发）
- 单飞防并发（`refreshInFlight`）
- 刷新失败 401 → 「登录态已过期，请重新登录」（并 `clearBoxAgentAuth()`）
- 刷新成功后**同时写回 access + refresh**（`updateDesktopAuthState`）并同步给 box-agent
- 写入用 `tmp + rename` 原子替换；对 `EACCES/EBUSY/EEXIST/EPERM` 做 20/50/100/200ms 重试

> ⚠️ **本次刻意未实际调用 `/refresh`**：它会**轮换 refresh_token**，若调用后未写回会**破坏你客户端的登录态**。
> 端点与格式来自官方生产代码（权威来源），B 门禁第 1 条可据此判定为**成立**；
> 真实调用留到渠道实现阶段（届时由 wild-work 自己持有并落盘轮换后的 token）。

### 对渠道实现的影响

1. **不需要 DPAPI/OSCrypt**；导入器直接读 `~/.box-agent/config/auth.json`（`BOX_AGENT_CONFIG_DIR` 可覆盖）。
2. **必须实现 refresh**：access 仅 2h，靠 `POST /api/electron/auth/v1/refresh` + 30 天 refresh_token 自持；
   轮换后的 refresh_token **必须原子落盘**（对齐 wild-work R20「凡调 RefreshToken 必紧接 SaveAtomic」）。
3. **档位不投影、且主动剥离 `reasoning_effort`**（默认档最深，见 §13）；费率直接用 `billing_multiplier`。
4. 默认模型别名：`raccoon-chat-ml-5-5` → 实际 `raccoon-8c4485`（响应回填真实 id 时注意对齐 `/v1/models` 声明）。

> **阶段 C 补充（已实测）**：流式为**真增量**（`firstContentEvent=1`，13 事件）、
> 错误形态为 **LiteLLM 信封**（401 `200001/200003`、400 `litellm.BadRequestError`）、
> 且**未知模型名会静默回落到默认模型并返回 200** → wild-work 必须本地校验模型名
> → 见 `docs/loomy-raccoon阶段C探针实测.md`。

---

## 11. 登录流程全貌与「能否复现」（2026-09-23 更正）

> ⚠️ 更正 §5.2 早期表述：小浣熊**不是**「授权只在客户端内部完成」，而是**有网页授权码流程**。
> 早期判为「无 OAuth」的原因：只查了主进程 `main.js`（那里确实没有 `auth/v1`），
> 而登录实现是**独立模块** `build/electron/main/desktopLogin.js`（6.4 KB，未被内联）。

### 11.1 完整链路（源码级证据）

```
① 渲染层（登录墙）→ IPC desktop-auth:start-login-from-wall
   main.js 校验：isTrustedDesktopAuthSenderUrl + isDesktopOnlinePlusMode
                + NEXT_PUBLIC_DESKTOP_CLIENT_AUTH==='true' + NEXT_PUBLIC_DESKTOP_TOKEN_BRIDGE==='true'
② desktopLogin.openDesktopLogin() → shell.openExternal(getDesktopAuthUrl())
     DESKTOP_AUTH_PATH   = "/code/authorize"
     DESKTOP_AUTH_PARAMS = { login_source: "desktop", appname: "办公小浣熊客户端" }
   ⇒ https://xiaohuanxiong.com/code/authorize?login_source=desktop&appname=办公小浣熊客户端
③ 用户在系统浏览器完成网页登录
④ 网页前端跳深链（服务端 SPA `index-*.js` 的 hl()）：
     const n = new URL("office-raccoon://auth/callback");
     n.searchParams.set("code", code);  state && n.searchParams.set("state", state);
     window.location.href = n.toString();
⑤ 客户端收深链 → queueDesktopLogin → parseDesktopLoginCode
     （校验 protocol==='office-raccoon:' && hostname==='auth' && pathname==='/callback'）
⑥ POST {authApi}/login_with_authorization_code
     headers: { Content-Type: application/json }
     body:    { "authorization_code": "<code>" }
     timeout: 15s；错误码 200035 = 授权码不存在/过期/已消费
⑦ resp.data = { access_token, refresh_token, office_identity, office_org_name, office_org_role }
   → updateDesktopAuthState(...) → 写 auth.json
```

### 11.2 关键判定

| 项 | 结论 | 依据 |
|---|---|---|
| 授权入口 | 固定 `GET /code/authorize?login_source=desktop&appname=…` | `desktopLogin.js` |
| 回调地址 | **硬编码** `office-raccoon://auth/callback` | 服务端 SPA `hl()` |
| 能否自定义回调 | **不能**（全站 JS 零处 `redirect_uri`） | 服务端 `index-*.js` grep |
| 兑换端点 | `POST {authApi}/login_with_authorization_code`，**无额外鉴权头** | `desktopLogin.js` |
| `{authApi}` | `getAuthApiUrl()`：优先 `/api/web/auth/v1`（`NEXT_PUBLIC_DESKTOP_REMOTE_AUTH_API_PREFIX`），空则回落 `/api/electron/auth/v1`；两者实测都通 | `scheduleAuth.js` + `.env.electron` |
| 授权码 | 一次性，消费即失效（`200035`） | `desktopLogin.js` |
| 深链协议 | `HKCU\Software\Classes\office-raccoon`（另有 `raccoon-work`）→ 指向客户端 exe | 注册表实测 |

### 11.3 第三方复现的两条路

| 方案 | 做法 | 代价 / 风险 | 状态 |
|---|---|---|---|
| **导入** | 读客户端 `auth.json` | 需先装并登录官方客户端；导入后建议退出客户端（refresh_token 单会话） | ✅ 已实施 |
| **协议劫持登录** | 登录期间把 `HKCU\Software\Classes\office-raccoon` 临时指向 wild-work → 收深链拿 code → 自己调 `login_with_authorization_code` → 成功后**恢复注册表** | 需改用户级注册表并保证崩溃后可恢复；登录期间官方客户端收不到回调（互斥）；厂商在收紧（源码注释「仅向登录墙暴露专用 IPC」），可能被视为滥用 | ✅ **已实施**（2026-09-23）：`internal/login_raccoon` + `internal/raccoon/protocol_windows.go`；面板主按钮走此路径，次按钮保留导入 |

> 「劫持」之所以技术上可行，是因为兑换端点**不校验调用方身份**（无 device identity、无签名头）——
> 谁拿到 code 谁就能换到 token。这是该流程唯一的缺口。

---

## 12. 协议劫持登录实现要点（2026-09-23 落地）

| 环节 | 位置 | 关键约束 |
|---|---|---|
| 协议注册表快照/改写/恢复 | `internal/raccoon/protocol_windows.go` | ①先备份落盘再改写（备份写不进去绝不碰注册表）②恢复前校验「当前值仍指向我们」（`shouldRestore`）——官方客户端启动时会重写协议注册，此时放手、只清备份，否则会把注册表改回过期路径 ③恢复幂等 |
| 深链解析 + 回调落盘 | `internal/raccoon/protocol.go` | 按官方 `parseDesktopLoginCode` 校验 `scheme/host/path`；**不落盘原始深链**（少留一份授权码副本） |
| 登录编排 | `internal/login_raccoon/login.go` | Start 先清残留回调再劫持；Poll **无论如何先恢复注册表再返回**；兑换先试 `/api/web/auth/v1`，**仅 404/405 才回落** electron 前缀（业务错误如 200035 重试无意义） |
| 回调子进程 | `cmd/wild-work/main.go` | **最前面**拦 `--raccoon-callback`，只落盘后立即退出（绝不启动服务/托盘，否则撞端口） |
| 恢复兜底 | `internal/app/app.go` | `pollLogin` 的 **defer 统一恢复**（成功/失败/超时/取消/panic 全覆盖）；`CancelLogin` 补恢复；**启动自愈** `healRaccoonProtocol()`（data/ 有残留备份即恢复） |
| 面板 | `cmd/wild-work/web/app.js` | 主按钮走协议登录，次按钮「从客户端导入」（`pendingAction` 分发） |

**实测（VM，2026-09-23 19:38–19:43）**

- 假码闭环：劫持 → 深链被接住 → 兑换请求发出 → 上游回 `200035 authorization_code_not_found_error`（预期）
  → 日志 `raccoon 协议注册表已恢复原状`，注册表还原、`login-state.json` / `raccoon-callback.json` /
  `raccoon-protocol-backup.json` 三个中间文件全清。
- 真实登录：`登录成功 uid=<账号昵称>`（access/refresh 各 380 字符），账号进池、积分正常拉取、
  `/v1/models` 多出 8 个 `raccoon/*`、真实推理返回正常。

**顺带修掉的缺陷**：`app.reloadAccounts()` 原先只覆盖 7 个渠道，**漏了 raccoon 与 loomy** ——
导致「凭据写对了但账号不进池」，面板账号列表与 `/v1/models` 都看不到（要等下次重启才出现）。
已补两个分支，并给 `importRaccoon` / `importLoomy` 补上 `reloadAccounts()`。

**两条路的关系**：拿到的是**同一个上游账号**（refresh_token 单会话，会互相踢）；
协议登录不依赖客户端登录态，但登录期间客户端收不到回调，且客户端一启动就会重写协议注册（劫持失效）。

## 13. 思考控制：「深度思考」的正确做法是**不发档位**（2026-09-24 实测）

### 13.1 客户端按钮 ≠ 网关字段

| 层 | 字段 | 说明 |
|---|---|---|
| 客户端「深度思考 / 快速」开关 | `enable_deep_thinking`（布尔，默认 `false`） | 产出在 `p()` 里：`{enable_deep_thinking: deepThink, enable_web_search, enable_deep_research: true}` |
| 该字段发给谁 | **agent 层**：`POST /sessions/{id}/chat-conversations`、`plans/{id}/execute-async` | 走 `input_type` / `payload.input` 形状 |
| 本渠道对接的 LLM 网关 | `POST /api/web/llm/v2/chat/completions` | 对该字段直接 **500**：`AsyncCompletions.create() got an unexpected keyword argument 'enable_deep_thinking'` |
| LLM 网关认的字段 | 标准 `reasoning_effort` | 客户端 LLM 层是 `@ai-sdk/openai-compatible`，`settings.reasoningEffort → reasoning_effort`，SDK 声明域 `low \| medium \| high` |

`enable_deep_research` 同理 —— 发到 LLM 网关也是 500（同一报错模板）。

### 13.2 关键实测：**上游默认档才是最深**

同 prompt（`1000! 尾零数`，硬推理）+ `reasoning_tokens`（权威指标）交叉采样：

| 请求 | rtok 各次采样 | 含义 |
|---|---|---|
| **无 `reasoning_effort`** | 2300 / 2147 / 2133 / 4067 / 1167 / 2241 | **默认档 = 最深** |
| `reasoning_effort=high` | 142 / 260 / 331 / 135 / 98 / 145 | 反而**锐减 ~90%** |
| `reasoning_effort=medium` | 1677 | 居中 |
| `reasoning_effort=low` | 0 | 基本不思考 |

fast 标签模型（`sn-glm-5-3-flash`，短 prompt）同样成立：默认 `rtok=26` > high `8` > medium `7` > none `0`。

> **结论**：`reasoning_effort` 在 litellm 侧被映射成一个**偏保守的思考预算**，下发任何值都比"不告诉它"更保守。
> 因此本渠道**主动剥离**该字段（`forceUpstreamDeepThinking`），保证实际走深度思考。
> 代价：客户端无法通过档位让该渠道"快速"——那需要换用 fast 标签的模型。

### 13.3 对 wild-work 的影响

- `SupportsEffortKind` **不含 raccoon**：`/v1/models` 不声明档位，客户端不会因此发出档位选择器。
- 但 WorkBuddy 侧的**面板默认档**（`currentReasoningEffort()`）与客户端的档位字段仍可能落到请求里
  （`prepareChatBody` 会写 `reasoning_effort`）→ 渠道层必须自行剥离，不能指望上游忽略。
- 单测 `TestForceUpstreamDeepThinking` 守住该语义（含"未表达时逐字节不改写"）。

---

## 14. 流式总超时与截断收尾（2026-09-24）

与 loomy 同批修复（完整根因分析见 `docs/loomy渠道接入备忘.md` §12），本渠道同款缺陷：

- **`http.Client.Timeout` 是整请求上限**：计时器在 `Do()` 返回后继续跑直到 body 读完，
  而 SSE 整个生成期都在读 body ⇒ 长思考请求被从流中间掐断。
  本渠道「剥离档位、走上游默认最深思考」（§13），生成期天然比其它渠道更长，**风险更高**。
- `internal/raccoon/sse.go` 在 `sc.Err() != nil` 时直接 return、不补 `[DONE]`
  ⇒ 客户端收到无收尾的截断流 =「突然无响应」。

### 14.1 修复

- 新增 `StreamHTTP *http.Client`（**无总超时**），`ChatStream` 改用它；
  非流式 `HTTP` 保留总超时（目录/积分/refresh 都是短请求）。
- 补 `newTransport()`（禁 h2 + Dial/keepalive + TLS 握手 + `ResponseHeaderTimeout: 60s`）。
  此前未设 Transport，实际共用 `http.DefaultTransport`；`StreamHTTP` 无总超时后
  **必须**有 `ResponseHeaderTimeout` 兜底。
- `IdleTimeout`（默认 90s，可配 `upstream.stream_idle_seconds`，下限 10s）+
  `provider.IdleReader` 空闲看门狗。
- 截断补 `provider.WriteTruncationFrames`（error 帧 + `[DONE]`）。

### 14.2 守门测试

`TestStreamClientHasNoTotalTimeout` / `TestStreamTruncationEmitsFrames` / `TestIdleTimeoutDefaults`
（均在 `internal/raccoon/raccoon_test.go`）。
