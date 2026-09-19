# 千问办公（QwenWork）逆向对比 + 实机验证备忘

> 目的：对比两个开源参考项目的千问办公逆向原理是否一致，评估与 wild-work 的契合度，并用真实凭据实机验证。
> 克隆位置（不入版本控制）：`ref/xrl-router-plugin-qwenwork`、`ref/Buddy2api`
> 验证脚本：`temp/qwen/`（含明文凭据，勿提交）
> 编写日期：2026-09（对照 wild-work v2.1.x）

---

## 0. 两个参考项目速览

| 项 | xrl-router-plugin-qwenwork | Buddy2api |
|---|---|---|
| 作者 | 杏仁鹿（krkr@xrl.im） | LBJ_WICM |
| 语言/栈 | TypeScript + Express（xrl-router 插件，WS 注册） | Python + FastAPI + SQLite + 静态 Web 管理页 |
| 版本/最后提交 | 0.1.0 / 2026-08-06 | 2.1.9 / 2026-09-15 |
| 许可 | MIT | MIT |
| 渠道 | qwenwork（默认）+ wukong（钉钉悟空 DEAP） | workbuddy / qclaw / qwenwork / traework |
| 定位 | **纯协议桥接层**：不做密钥池/重试/路由（交给 xrl-router） | **完整网关**：账号库、API Key、额度、Codex `/v1/responses` |
| 逆向文档 | `docs/reverse/QWENWORKCN_REVERSE.md`（极详尽，含失败路径与绕过链） | `docs/design/multi-channel-v2.md` Appendix B（实现要点 + 冻结门闩） |

两者**互相独立**（无代码引用关系），却得出高度一致的结论，互证价值很高。

---

## 1. 逆向原理对比：核心算法字节级同源

### 1.1 完全一致的部分

| 环节 | 两项目实现（逐条一致） |
|---|---|
| 应用真身 | QwenWorkCN 桌面端（Electron），userData = `%APPDATA%\QwenWorkCN`，凭据文件 `auth-v2.dat` |
| 凭据加密 | Electron safeStorage `v10` 头。Windows：`Local State` 的 `os_crypt.encrypted_key`（`DPAPI\0` 前缀）→ DPAPI(CurrentUser, entropy=NULL) → 32B AES key → **AES-256-GCM**（`v10 + 12B nonce + ct + 16B tag`） |
| token 刷新 | `POST https://gateway.qwenwork.cn/api/v1/deviceToken/refresh`，body `{refresh_token, target:"c"}` → 返回 `device_token`(或 `token`) + **轮换的** `refresh_token` + `expires_at` |
| AES 会话密钥 | **16 个 ASCII hex 字符**（`uuid4().hex[:16]`），`key = iv = utf8 字节`；**不是** `os.urandom(16)` |
| info 明文 | `{uid, aid:"", name, email, security_oauth_token:<access>}` → AES-128-CBC(PKCS7) → base64 |
| Cosy-Key | `base64(RSA_PKCS1_v1_5(公钥, 16字符))`，**PKCS1 非 OAEP**（OAEP → 403） |
| authorization | `Bearer COSY.<b64(header)>.<md5>`，header = `{version:"v1", requestId, info, cosyVersion, ideVersion}` |
| 签名串 | `f"{o}\n{cosyKey}\n{ts}\n{body}\n{path}"`；`path` = URL pathname，**去 query + 去 `/algo` 前缀** |
| RSA 公钥 | **同一把 PEM**（1024-bit，modulus 头 `c0f223…`），且与 wild-work `internal/qoder/cosy.go` 的 `serverPubKeyPEM` 逐字节相同 |
| 推理端点 | `POST /algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common` |
| 请求体 | **明文 JSON**（不带 `Encode=1`） |
| 响应格式 | 外层 envelope `data:{"headers":…,"body":"<内层 OpenAI chunk JSON>","statusCodeValue":200}`，需剥一层 |
| 签到 | **无签到活动**（Buddy2api `checkin_supported=False`） |
| 静态头族 | `Cosy-Business-Product: qoder_work`、`Cosy-Business-Type: agent`、`Cosy-ClientType: 6`、`Cosy-Scene: qwork`、`Login-Version: v2`、`x-model-source: system` |

**结论：两者对「千问办公逆向原理」的认知完全一致，且与 wild-work 现有 Qoder COSY 实现同构。**

### 1.2 存在差异的部分（版本漂移 + 工程取舍）

| 维度 | xrl-router-plugin | Buddy2api |
|---|---|---|
| 逆向观测版本 | macOS `QwenWorkCN.app 0.1.3` → Windows 实机验证 | Windows `0.1.8-26081406` |
| `Cosy-Version` | `1.0.47` | `1.1.18`（取自 qoderclicn 常量 `l0A`，设 `COSY_VERSION_FROZEN` 门闩，未冻结拒绝出站） |
| header `cosyVersion`/`ideVersion` | `"1.0.0"` / `"1.0.0"` | `1.1.18` / `0.1.8` |
| `Cosy-MachineOS` / UA | `aarch64_darwin` / `node` | `x86_64_win32` / `qoderwork/0.1.8` |
| `Cosy-MachineId` | 固定 `"unknown"` | 取 `auth-v2.dat` 的 `loginDeviceId` |
| body 策略 | **原样透传** OpenAI chat.completions + 补 `request_id`/`session_id` | **重构造** qoder 原生结构（`chat_context.text` 为字符串、`session_type:"qoder_work"`） |
| token 生命周期 | 三源 fallback（内存 → `auth-v2.dat` → `.env QWEN_KEYS`）+ `fs.watch` 文件监听 + **双向写回**，按需刷新（5min 缓冲，单飞防并发） | 账号入库 SQLite，`pick_account_with_fallback` 按需刷新，刷新成功后写回 `auth-v2.dat`（原子 replace + 首次 `.bak` + 保留未知字段） |
| 平台覆盖 | macOS（Keychain + PBKDF2 1003/saltysalt/AES-128-CBC IV=0x20）+ Windows（DPAPI + GCM） | **仅 Windows**（DPAPI），Linux Docker 明确不支持 |
| 额度查询 | 无 | `GET /api/v1/adapter/user/account-context?include=user,plan,quota` |
| 模型列表 | 静态 4 个 | 静态 + `GET /api/v2/model/list` 动态拉取 |

### 1.3 关键发现

1. **RSA 公钥三方一致**（wild-work qoder / xrl / Buddy2api）→ 该 PEM 是「Qoder 系」共享公钥；也佐证千问办公与 QoderWork 同源（xrl 文档：千问办公内部代号即 **Qoder**，出品方 DingTalk，更新源 `static.qoder.com.cn/qwen-work-cn`）。
2. **明文 body 已三方独立验证**（xrl §6.8、Buddy2api Appendix B、本文 §2 实测）。
3. **`cosy-key` 语义已被修正**：xrl §6.1 曾推测为「RSA-1024 封装的会话对称密钥」，§6.8 自我修正为「RSA_PKCS1(asar 公钥, 16 字符 AES key)」，与另两方一致。
4. **`Cosy-Version` 校验宽松**（本文 §2.4 实测：删除或改成 0.1.43/1.0.47 均可）→ 该头非强校验项。
5. **Buddy2api 把 QwenWork 与 QoderWork CN 明确分家**：QwenWork = `gateway.qwenwork.cn` / clienttype **6** / 明文；QoderWork CN = `gateway.qoder.com.cn` / clienttype **5** / `Encode=1` / `dt-`·`drt-`。但**公钥相同、签名串格式相同**，实为同一 COSY 框架的两个实例。
6. **防重放机制**：签名 `md5` 绑定 `body + path + 时间戳`，每请求必须新生成 `timestamp` + 新 `info`/`cosy-key`。xrl §6.5 记录的 `103 Duplicate request` 死锁，根因是复用抓包的 authorization；自造签名即绕过。

---

## 2. 实机验证（2026-09，真实凭据）

### 2.1 验证环境

- 凭据来源：**千问办公网页版**（`qwenwork.cn`）登录态 —— cookie `token`（HS256 JWT，`iss=qwenwork.cn`，`client_type=desktop`，uid `d0fbfa46-…`）+ `ory_hydra_session`
- 桌面端 COSY 签名：Python 复刻（`temp/qwen/cosy.py`），RSA 公钥取三方一致的 PEM
- 目标：`gateway.qwenwork.cn`（桌面端网关）
- 原则：仅只读 GET + 极短推理（`max_tokens` 8~24），每次间隔 3~4s，不并发

### 2.2 核心验证结果

| # | 验证项 | 结果 |
|---|---|---|
| 1 | 网页 cookie 只读端点 `/user/balance` | **200** `{"balance":2098.6147,"freeze_credit":0}` |
| 2 | 网页 cookie `/user/info` | **200**（nickname=rockswang，Free 套餐，month_requests=100000） |
| 3 | **COSY 签名 GET `/api/v2/model/list`** | **200**，2420 字节，返回 3 个模型 |
| 4 | `?Encode=1` 对 GET 模型列表 | **200**（与不带 Encode 完全相同，说明该参数在 GET 上无效果） |
| 5 | **COSY 签名 POST 推理（明文 body）** | **200** `x-model-name: qwen3.8-flash` / `x-provider-name: maas` |
| 6 | 积分实际扣减 | 2098.5884 → 2098.587（一次 flash 极短对话 ≈ **0.0014** 积分） |
| 7 | `usage` 字段 | `{prompt_tokens:66, completion_tokens:28, total_tokens:94, reasoning_tokens:24}` |
| 8 | 篡改 md5 签名 | **403** `{"code":"101","message":"Signature invalid"}` |
| 9 | body 改了但签名不重算 | **403** Signature invalid |

**→ 逆向结论完全复现：网页版 JWT 可直接驱动桌面端 COSY 签名，明文 body 可用，无需 Encode。**

### 2.3 模型清单（实测，2026-09）

`GET /api/v2/model/list` 返回 **scene = `qwork`，仅 3 个模型**（`chat`/`developer`/`assistant`/`inline` 等 scene 全为空）：

| key | display_name | price_factor | is_reasoning | is_vl | max_input |
|---|---|---|---|---|---|
| `pro` | 高级（Pro） | 1 | false | true | 180000 |
| `flash` | 标准｜Qwen3.8-Flash | **0.1** | false | true | 180000 |
| `qwen3.8-max-preview` | Qwen3.8-Max | 1.1 | false | true | 180000 |

**⚠️ 与两个参考项目记载的模型清单已发生代际变化：**

| `x-model-key` | 实测路由结果 | 说明 |
|---|---|---|
| `flash` | `x-model-name: qwen3.8-flash` / `maas` | 新档位 |
| `pro` | **`glm-5.2`** / `maas-glm` | 显示名「高级」，实际后端仍是 GLM-5.2 |
| `qwen3.8-max-preview` | （未测） | 新档位 |
| `qwork-advanced` | **`glm-5.2`** / `maas-glm` | **旧 key 仍被接受**（Buddy2api 静态表用此值，未失效） |
| `qwork-auto` / `qwork-lite` / `qmodel_latest` | 未测 | xrl 记载的旧档位 |

`x-model-name` 是**真实后端模型名**，`x-model-key` 只是应用层档位。`pro` 与 `qwork-advanced` 都路由到 `glm-5.2`，说明档位映射在服务端。

### 2.4 请求头容差矩阵（实测，对实现价值最高）

**结论：所有 `Cosy-*` 业务头、版本头、UA 全部不校验；只有 COSY 签名三件套是强校验。**

| 测试 | 结果 |
|---|---|
| 基线（完整静态头） | 200 |
| 删除 `Cosy-ClientType` | 200 |
| 删除 `Cosy-Business-Product` | 200 |
| 删除 `Cosy-Business-Type` | 200 |
| 删除 `Cosy-Scene` | 200 |
| 删除 `Cosy-MachineOS` | 200 |
| 删除 `Login-Version` | 200 |
| 删除 `x-model-source` | 200 |
| 删除 `User-Agent` | 200 |
| 删除 `Cosy-Version` | 200 |
| **删除 `Cosy-User`** | **403 Signature invalid** ← 签名串依赖它 |
| `Cosy-Version: 1.0.47`（xrl 观测值） | 200 |
| `Cosy-Version: 0.1.43`（Qoder CN 值） | 200 |
| `Cosy-ClientType: 5`（Qoder CN 值） | 200 |
| `Cosy-Business-Product: qoder`（非 `qoder_work`） | 200 |
| 极简头（仅 `Content-Type` + `Accept` + COSY 三件套 + `x-model-key`） | **200** |

> 注：`Cosy-User` 本身参与的是 `info`（AES 加密的 userInfo 内含 uid），签名串里的 `cosyKey` 覆盖它；删掉该头即 403，说明网关会从 header 取值参与校验。

### 2.5 body 必需字段（实测）

| 测试 | 结果 |
|---|---|
| 极简头 + 完整 body | 200 |
| **无 `request_id`** | 外层 200 但 `statusCodeValue:400`，`body:{"code":"400","message":"request_id is required"}` |
| **无 `session_id`** | 同上，`session_id is required` |
| 无 `max_tokens` | 200 |
| 无 `model`（仅靠 `x-model-key`） | 200 |
| 无 `stream` | 200 |
| **仅有 `messages`**（无 model/stream/ids） | **200** ← 说明 `request_id`/`session_id` 由服务端兜底？ |

> ⚠️ 矛盾点：单独删 `request_id` 报 400，但「仅有 messages」却 200。推测服务端有幂等/缓存：同一签名请求重复提交时命中已生成的会话。**实现上仍应始终补 `request_id`/`session_id`**（两参考项目一致做法）。
> **关键：网关对错误返回 HTTP 200 + `statusCodeValue:400` 包在 SSE 外层**，客户端必须剥壳判断，不能只看 HTTP 状态码（Buddy2api 的 `envelope_error()` 正是为此）。

### 2.6 x-model-key 与 body model 的关系（实测）

| 测试 | 结果 |
|---|---|
| `x-model-key: pro` + body `model: flash` | **200，`x-model-name: glm-5.2`** → **header 胜出** |
| 去掉 `x-model-key`，body `model: flash` | 200（模型未知，可能回落到默认 `pro`→glm-5.2） |

### 2.7 端点鉴权矩阵（重要差异）

| 端点 | COSY 签名 | 纯 `Bearer <JWT>` |
|---|---|---|
| `GET /api/v2/model/list`（网关） | **200** | **403 Signature invalid** |
| `GET /api/v1/adapter/user/account-context`（网关） | **401 INVALID_TOKEN** | **200** |
| `POST .../agent_chat_generation`（网关） | **200** | 未测（预期 403） |
| `GET https://qwenwork.cn/user/balance`（网页域） | — | cookie 200 |
| `GET https://qwenwork.cn/user/v2/account-context`（网页域） | — | cookie 200 |

**结论：桌面网关内部存在两类端点 —— COSY 签名端点（推理、模型列表）与纯 Bearer 端点（account-context 等账户 API）。混用必失败。** 这与 Buddy2api 的实现完全吻合（`_account_context` 用纯 Bearer，`chat`/`models` 用 COSY）。

### 2.8 token 生命周期（关键问题，未完全解决）

**⚠️ 网页版凭据不能刷新桌面端 token。**

- 网页版是 **cookie / Ory Hydra 会话**：`ory_hydra_session` + `token`（HS256 JWT，24h exp）
- 从 chat app 的 JS bundle（`/app/assets/index-Di6r_6vz.js`，3.4MB）提取到的端点全部是网页域相对路径：`/auth/oauth/*`、`/auth/dingtalk/sso`、`/auth/qr-login/transactions`、`/user/v2/*` 等
- **未发现 `refresh_token` / `deviceToken` / `ory_rt_` 相关逻辑**（桌面端的 `deviceToken/refresh` 是 Electron 端专属）
- 实测：`account-context` 纯 Bearer 可稳定 200（连续多次），说明当前 JWT 在 24h 内有效

**实现建议**：`deviceToken/refresh` 只对**桌面端 `auth-v2.dat` 的 `ory_rt_` refresh token** 有效；网页版凭据应作为「手工导入的一次性 token」，过期后需重新从浏览器复制，不能自动续期。

---

## 3. 与 wild-work 的契合度

### 3.1 wild-work 现状

已有 **Qoder 渠道**（`internal/qoder`）：

| 项 | 现值 |
|---|---|
| 域名 | Base `openapi.qoder.com.cn` / Gateway `gateway.qoder.com.cn` |
| 鉴权 | `dt-` / `drt-`；COSY 签名（`cosy.go`：RSA_PKCS1 + AES-128-CBC + MD5） |
| RSA 公钥 | 与两个参考项目**逐字节相同** |
| 静态头 | `cosy-clienttype: 10`、`cosy-version: 1.1.57`、`cosy-data-policy: disagree`、`cosy-business-product/-type/-scene: app/agent/app`、`cosy-machineos: x86_64_win32`、`user-agent: Go-http-client/2.0`（2026-09-20 起按桌面版实测对齐，见 AGENTS.md R21） |
| body | `buildAgentBodyMeta` 重构造（`session_type: "app"`、`chat_context.text` 为**字符串**、顶层 `system` 数组、`parameters` 恒下发；对齐后 legacy 端点开始下发思考链） |
| 编码 | `Encode=1` + `qoderEncode`（base64 + 自定义字母表 + 三段重排） |
| SSE | `parseNestedSSE` 剥 `body` 外层 |
| 签到 | `DailyCheckin` 返回错误（无活动） |
| 登录 | OAuth Device Flow（`client_id 1c5e33e1-…`、PKCE） |

**尚无 qwenwork 渠道**（`gateway.qwenwork.cn` / clienttype 6 / 明文 body / `auth-v2.dat` safeStorage / `ory_rt_` 刷新 / `qoder_work` 产品头）。

### 3.2 契合度评分

| 层 | 契合度 | 说明 |
|---|---|---|
| 签名算法层 | **95%** | `cosy.go` 结构可直接复用，仅需常量参数化 |
| 协议/传输层 | **90%** | 嵌套 SSE、错误分类、`provider.Upstream` 均已就位 |
| 凭据/存储层 | **50%** | `auth.Auth` 是「文件即凭据」，与 `auth-v2.dat`（safeStorage 加密）模型不同 |
| 架构扩展点 | **90%** | `provider.Kind` + `Load*Dir` + `Runtime` 注册三步扩展点齐备 |
| 调度/保活 | **60%** | 有 `KeepaliveHours`，但 QwenWork 的 refresh **轮换**特性与之冲突 |

综合：**约 85%**。

### 3.3 可直接复用的资产

1. **RSA 公钥**：`internal/qoder/cosy.go` 的 `serverPubKeyPEM` 原样可用（已三方互证）。
2. **签名骨架**：`NewCosySession` / `AuthHeader` / `ApplyHeaders` 只需抽出「静态头集合 + 版本常量」为参数。
3. **SSE 剥壳**：`internal/qoder/sse.go` 的 `parseNestedSSE` 与两参考项目等价，envelope 结构相同 → 直接复用。
4. **编码开关**：`internal/qoder/encoding.go` 的 `qoderEncode` 在 QwenWork 上**不需要**（实测明文 200）。
5. **渠道装配模板**：`internal/workbuddyai`（无签到、`KeepaliveHours=nil`）最贴近 QwenWork。

### 3.4 需要新增的工作

| # | 工作项 | 参考实现 | 备注 |
|---|---|---|---|
| 1 | `auth-v2.dat` 解密（Windows DPAPI + AES-256-GCM） | xrl `auth.ts` / Buddy2api `store.py` | wild-work 为 `CGO_ENABLED=0`，用 `golang.org/x/sys/windows.CryptUnprotectData` 手写绑定（无需 cgo） |
| 2 | macOS Keychain 解密（PBKDF2 1003 + saltysalt + AES-128-CBC IV=0x20） | xrl `auth.ts` | 或先 Windows-only 并显式报错 |
| 3 | 写回 `auth-v2.dat`（对称加密 + 原子 replace + 首次 `.bak` + 保留未知字段） | Buddy2api `store.py::write_refreshed_auth` | 见风险 1 |
| 4 | 常量/静态头参数化 | 两项目 constants | 实测**这些头都可以不传**，可只保留 `x-model-key` + COSY 三件套，降低版本漂移风险 |
| 5 | body 策略 | xrl 透传 / Buddy2api 重构造 | **两者实测均 200**，建议照抄 xrl 透传（最省事） |
| 6 | **必须补 `request_id` / `session_id`** | 两项目一致 | 实测缺失 → `statusCodeValue:400 request_id is required` |
| 7 | **必须剥 SSE 外层判错** | Buddy2api `envelope_error()` | 错误是 HTTP 200 + `statusCodeValue:400` |
| 8 | 模型列表 | Buddy2api `models.py`（COSY GET，body 用空串 `""` 而非 `"{}"`） | 实测 `GET /api/v2/model/list` 200；注意档位已更新为 `pro`/`flash`/`qwen3.8-max-preview` |
| 9 | 额度 | 纯 Bearer `account-context`（**不能用 COSY**） | 实测：COSY 401 / Bearer 200 |
| 10 | 登录 | 可做「手工粘贴 JWT」+「导入 `auth-v2.dat`」双路径 | 两参考项目均不做 OAuth |
| 11 | 注册装配 | `cmd/wild-work/main.go` | `provider.QwenWork` + `LoadQwenWorkDir` + `Runtime` + scheduler |

预估新增代码：**约 400–600 行 Go**（含平台分支），加测试。

### 3.5 关键风险与决策点

1. **⚠️ refresh token 轮换互踩（最高优先级）**
   `deviceToken/refresh` 会**轮换** refresh_token。wild-work daemon 常驻 + `KeepaliveHours` 定时保活 → 与用户同时开着的千问办公 App 互相作废。
   - xrl 解法：按需刷新（5min 缓冲，平均 1h 才刷 1 次）+ 写回 `auth-v2.dat` + `fs.watch` 监听 App 侧刷新。
   - Buddy2api 解法：按需刷新 + 刷新成功后写回 `auth-v2.dat`（带备份）。
   - **建议**：QwenWork 渠道设 `KeepaliveHours = nil`（对齐 workbuddyai），改为「每次 chat 前检查 `ExpiresAt`，临近过期才刷新」，并实现写回 `auth-v2.dat`。
2. **写回安全性**：必须保留 `auth-v2.dat` 中未知字段（`loginDeviceId`、`loginMethod`、`refreshStrategy` 等），先备份再原子替换；解密失败**禁止**写回。
3. **零 token 日志不变量**：新渠道需纳入 wild-work「日志/面板/消息框零 token」约束。
4. **平台限制**：DPAPI 绑定当前 Windows 用户，`auth-v2.dat` + `Local State` 拷贝到其他机器/账户无法解密 → 与跨平台定位冲突，需在 UI/文档明确提示。
5. **`x-model-key` 用 `qwork-*`/新档位，勿用真实后端名**：`x-model-key: glm-5.2` 是错用法（Buddy2api 明令禁止）；实测 header 优先级高于 body `model`。
6. **`Encode` 开关别搞混**：Qoder 走 `qoderEncode`（自定义字母表三段重排），QwenWork 走明文。
7. **签到**：两项目均确认无签到活动 → `DailyCheckin` 返回错误，并把 QwenWork 加入 `noExplicitCheckin` 白名单。
8. **版本头会漂移**：实测所有 `Cosy-*` 业务头/版本头都不校验，因此**建议尽量少传**，避免未来版本升级导致硬编码常量失效。
9. **模型档位代际更新**：`pro`（高级）实测后端仍是 `glm-5.2`，`flash` 是 `qwen3.8-flash`（0.1 倍率，最省积分，适合连通性测试）。静态模型表需支持运行时刷新。

---

## 4. 结论

1. **逆向原理完全一致**：两个开源项目对千问办公的逆向结论逐条吻合，RSA 公钥甚至与 wild-work 现有 Qoder 渠道**逐字节相同**，属同一 COSY 框架的两个实例（QwenWork clienttype 6 / QoderWork CN clienttype 5）。
2. **实机验证全部通过**：网页版 cookie JWT 可直接驱动桌面端 COSY 签名；明文 body 推理返回 200；签名篡改/body 篡改均 403；积分扣减真实发生（≈0.0014/次）。
3. **最重要的新发现**：网关对 `Cosy-*` 业务头**几乎零校验**，只有签名三件套（`Authorization`/`Cosy-Key`/`Cosy-User`）强校验；这大幅简化实现，也降低了版本漂移风险。
4. **两个必须处理的协议细节**：`request_id`/`session_id` 必填；错误以 HTTP 200 + `statusCodeValue:400` 的 SSE 外层返回，必须剥壳判断。
5. **与 wild-work 契合度约 85%**：签名/协议/架构三层可直接复用，主要新增工作是 `auth-v2.dat` 解密与写回、常量参数化、token 轮换策略。
6. **最大风险是 refresh token 轮换互踩**，必须用「按需刷新 + 写回 auth-v2.dat」而非现有定时保活模式。
7. **网页版凭据不可自动续期**（无 `deviceToken/refresh` 等价接口），只能作为手工导入的一次性 token。

---

## 5. 参考资料

- `ref/xrl-router-plugin-qwenwork/docs/reverse/QWENWORKCN_REVERSE.md` — 千问办公逆向全文
- `ref/xrl-router-plugin-qwenwork/docs/specs/qwenwork-{signing,token,forward}.md` — 签名、token、转发规格
- `ref/xrl-router-plugin-qwenwork/docs/DECISIONS.md` D-3 — 为什么改为自行管理 token（轮换互踩的原始记录）
- `ref/Buddy2api/docs/design/multi-channel-v2.md` Appendix A/B/C — 通道隔离设计 + QwenWork COSY 要点 + QoderWork CN 未决项
- `ref/Buddy2api/providers/qwenwork/{cosy,chat,token,store,models}.py` — Python 参考实现
- wild-work `internal/qoder/{cosy,client,sse,body,encoding}.go` — 现有 Qoder COSY 实现
- 验证脚本（含明文凭据，勿提交）：`temp/qwen/{cosy.py, stage2_cosy.py, stage9_headers.py, stage10_minimal.py, stage11.py, stage12.py}`
- 验证结果落盘：`temp/qwen/stage{5,6,7,9,10,11,12}_result.txt`
