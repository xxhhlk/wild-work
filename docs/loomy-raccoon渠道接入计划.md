# Loomy + 商汤小浣熊（raccoon-ai）渠道接入计划

> 状态：**v2 — 决策已固化，待批准执行**（2026-09-22 21:35）
> v1 → v2 变更：§1 固化 6 项决策；新增 §2 由决策引出的 3 条硬约束（尤其「自适应」的边界）；
> §4 阶段 A 加入已授权的 shim/TLS 深化层；§5 触点补全积分/费率部分；§7 记录 2 项附带发现。
> 依据技能：`model-bridge` + `reverse-skill` + `electron-ai-client-protocol-extract`
> 本文件只记录**方案与判决标准**，不含任何凭据、token、账号标识。

---

## 0. 结论摘要

1. 两个客户端均已装在本机（Windows，`testuser` 账户），都是 Electron，当前**均未运行**。
2. **Loomy 取证成本最低**：`resources\app.asar.unpacked\electron\` 是**明文未打包源码**，已按功能分目录
   （`auth/ llm/ points/ channel/ nexus/ opencode/`）——契约可直接读，无需反混淆。
3. **小浣熊核心在 `app.asar`**（打包），但 `resources\config`、`cli-bundle`、`default-llm-config.json`
   明文；userData 有完整 `logs/`（850KB）+ 多个 sqlite。
4. **可行性核心不是"能不能发请求"，而是"凭据能否自持刷新"**（见 §2.2）。这决定渠道能否并入账号池，
   还是只能做一次性试用。
5. 计划分 A–F 六阶段，**B 是硬门禁**（三条 Go/No-Go），不通过就停在 A，不进入编码。

---

## 1. 已定决议（本轮确认，后续按此执行）

| # | 决议 | 落地含义 |
|---|---|---|
| **D1** | 一期**含积分 + 费率面板**；**签到可豁免**（若麻烦则不做） | 必做 `FetchModelPricing` + `UserResource`/`UserResourceDetail` + 定价缓存；`DailyCheckin` **条件实现**，降级判据见 §2.3 |
| **D2** | 授权到 **③ 装 shim / 抓 TLS** | 阶段 A 可直接上 hook / socket tap 拿完整请求头与请求体；纪律见 §4-A5 |
| **D3** | 小浣熊要做的是**官方模型额度**（非 BYOK） | 必须复现官方鉴权与设备身份；`default-llm-config.json` 里的 `apiBase/apiKey` 只作线索，**不作为方案** |
| **D4** | Loomy 为**个人版** | 不涉及企业租户/组织策略；但仍需查清个人账号是否绑定设备指纹 |
| **D5** | 账户/路径要**自适应** | 能做到什么程度见 §2.1——**路径可自适应，DPAPI 解密不可跨账户** |
| **D6** | **Windows 为主，无 mac 环境** | 平台实现只做 Windows；涉及 DPAPI 的代码用 build tag 隔离，mac 分支留空但**文档标注未实现** |

---

## 2. 由决议引出的硬约束（动手前必须接受）

### 2.1 「自适应」的真实边界（回应 D5）

**能自适应**：
- userData 路径：依次探测 `%APPDATA%\Loomy`、`%APPDATA%\office-raccoon`、`%APPDATA%\商汤小浣熊`，
  以及多用户 profile（`C:\Users\*\AppData\Roaming\<dir>`）——找到**存在且非空**的那份。
- 版本化安装目录：像 Qoder 的 `.qoder-versions\<ver>\` 那样，客户端可能有**多份副本**，
  必须**枚举全部**而不是写死单一路径。
- 客户端主程序路径（用于定位 shim 目标）：从开始菜单 lnk / 注册表卸载项解析，不写死。

**不能自适应（Windows 硬约束，必须接受）**：
- Chromium 凭据（`Local State` → `os_crypt.encrypted_key`）是 **DPAPI `CurrentUser`** 加密的。
  **跨账户解不开**——这不是权限问题，是解密密钥不在当前用户密钥环里。
- 因此：若客户端数据属于 `testuser`，而 wild-work 跑在 `im`，**以 im 身份无法解密那份凭据**。

**正解（不是"每次运行都去读客户端"）**：
```
一次导入 → 落到 wild-work 自己的 auths/<channel>-*.json → 之后由 refresh token 自持续期
```
导入器以**数据所属账户**身份运行（用户在 `testuser` 会话里执行，或 UAC 提权），
产出标准 `{auth:{...},account:{...}}` 嵌套格式（R6.3）。之后 wild-work 与客户端解耦。

> 三条凭据获取路线的优先级：
> **① 官方 OAuth 重新登录**（最优，天然跨账户跨机器）→ **② 本地凭据导入器**（次优，一次性）
> → **③ shim / TLS 抓包**（D2 已授权，兜底；抓到的 token 同样走导入器）。

### 2.2 凭据自持刷新 = 可行性核心

wild-work 现有 `internal/**` 里**完全没有** OSCrypt / DPAPI / sqlite 代码——所有渠道走的是
「本工具自己的浏览器 OAuth 登录 + `auths/*.json`」。所以 Loomy / 小浣熊若走客户端凭据路线，
**是一条全新能力线**，必须先证明：

1. refresh token 可在**没有客户端参与**的情况下换新（若像 Qoder 那样 token 由 host 经 stdio 下发 → 不通过）；
2. refresh 后**不会**因缺少客户端上报而失效（设备指纹 / 心跳绑定 → 不通过）；
3. token 有效期与轮换能支撑无人值守（决定 `KeepaliveHours`）。

三条任一不满足 → 该渠道**只能做一次性试用**，须如实标注，不进入账号池。

### 2.3 签到降级判据（回应 D1 的"如果麻烦"）

满足任一条件即**豁免签到**（按无签到渠道处理）：
- 上游是**服务端被动发放**、无领取接口（同千问办公）；
- 需要**图形验证 / 客户端内交互 / 额外设备指纹**；
- 签到端点未在客户端请求中出现（可能已废弃或走别的域）。

豁免的落地方式（三处**必须同源**，见 R10 与 AGENTS §5）：
| 位置 | 改法 |
|---|---|
| `cmd/wild-work/main.go` | `CheckinMinutes: nil`（**`KeepaliveHours` 注意 §7.1**） |
| `internal/app/app.go:180` | `noExplicitCheckin()` 加该 Kind |
| `cmd/wild-work/web/app.js:225` | `NO_EXPLICIT_CHECKIN` 集合加该渠道 |

> 签到只影响积分获取节奏，不影响推理可用性 → **可以一期不做、二期单独补**，不阻塞主线。

---

## 3. 本机侦查结论（已采集证据）

### 3.1 定位

| 项 | Loomy | 商汤小浣熊 |
|---|---|---|
| 安装目录 | `C:\Program Files\Loomy` | `C:\Program Files\raccoon-ai` |
| 主程序 | `Loomy.exe` | `商汤小浣熊.exe` |
| Electron 证据 | `LICENSE.electron.txt`、`*.pak` | 同 |
| 快捷方式 | `Loomy.lnk` → `C:\Program Files\Loomy\Loomy.exe` | 无（`%APPDATA%\商汤小浣熊` 存在） |

### 3.2 resources（决定取证路线）

**Loomy**（`resources\`）：
```
app.asar / app.asar.unpacked          ← unpacked\electron\ 是明文源码，按功能分目录
opencode / opencode-runtime / opencode-home-template
node-runtime / npm / python-archive / uv / ffmpeg-static
lark-cli / larksuite-cli / dingtalk-workspace-cli / wecom
playwright-mcp-runtime
.env.prod                             ← 非明文：`LOOMYENC1:` 编码块
app-update.yml / elevate.exe
```
`app.asar.unpacked\electron\` 子目录（**主战场**）：
```
auth  llm  points  channel  nexus  opencode  opencode-plugin  cloud-conversation
conversation  config  cross-end-sync  dingtalk  feishu  qq  ipc  knowledge  learning
memory  remote  replay  runtimes  schedule  search  share  skill  soul  team-assets
pet  image  notification  diagnostics  crash  lib  markdown-viewer
```

**小浣熊**（`resources\`）：
```
app.asar / app.asar.unpacked / app / config / assets / connectors
box-agent-runtime / browser-gateway / browser-tools / render-node-deps / cli-bundle
default-llm-config.json     ← 明文，顶层键：apiBase / apiKey / provider / model / lightweight / imageGeneration
default-pets / hyperframes-assets / rg / 7za / elevate.exe
```

### 3.3 userData 与日志

| 项 | Loomy | 小浣熊 |
|---|---|---|
| userData | `%APPDATA%\Loomy` | `%APPDATA%\office-raccoon`（另有 `%APPDATA%\商汤小浣熊`） |
| 日志 | `nexus-debug.log`、`loomy-installer-trace.log`、`update-trace.log` | `logs\raccoon.log`(850KB)、`logs\box-agent.log`(571KB)、`box-agent-stderr.log`、`runtime-provision.log` |
| 本地数据 | `app-ui-state.json`、`Local State`(Chromium OSCrypt)、`shared_proto_db`、`Partitions` | `local-chat.sqlite3`、`local_profile.db`、`memories.db`、`files.db`、`data_sources.db`、`schedule_tasks.db`、`settings.json`、`default-session-model-binding.json`、`desktop-device-identity.json`、`mnt`、`mobile-relay` |
| 日志内域名（已抽取） | `nexus-debug.log`：**0 个 https 命中** | `raccoon.log`：仅 `https://xiaohuanxiong.com`；`box-agent.log`：github / react.dev / vitejs.dev / xiaohuanxiong.com |

> **判读**：日志里没有推理端点 → 请求由主进程（`app.asar` 内）发出、或走 `wss://` / 相对路径拼接。
> 这**不是**「上游没有 API」的证据，而是阶段 A 要解决的问题。

### 3.4 项目内可复用资产

- `.gotmp/agent-auth.js`：**从千问办公客户端抠出的 `agent-auth` 模块**（含 `resolveUnpacked()`——
  把 `app.asar` 路径改写成 `app.asar.unpacked` 并校验存在性，以及与 `qwenwork_agent_auth_client.node`
  原生模块的加载链）。→ 「抠客户端鉴权模块 + 本地调用」在本项目**已有先例**，可复用同一手法。
- `.gotmp/base_check.go`：临时探针基线，可作新渠道探针骨架参考。
- `docs/千问办公QwenWork逆向对比备忘.md`、`docs/qoderCN渠道接入备忘.md`：同类渠道的取证与实施模板。

---

## 4. 阶段计划

### 阶段 A：契约取证（只读优先；shim/TLS 已获授权）

| 子项 | 动作 | 产出 |
|---|---|---|
| A1 | **Loomy**：读 `app.asar.unpacked\electron\llm\` `auth\` `points\` `channel\` `config\` | 端点表、鉴权头、模型名与 key 形态、积分字段、**刷新机制** |
| A2 | **小浣熊**：读 `resources\config\`、`cli-bundle\`、`default-llm-config.json`；解 `app.asar` 找 LLM/网络模块；读 `desktop-device-identity.json` 结构 | 同上 + **设备身份构成**（D3 关键） |
| A3 | **运行时取证**：启动客户端 → `Get-NetTCPConnection -State Established` 挑非内网远端 + 日志增量 | **真实端点**（最硬证据） |
| A4 | 若 A1–A3 拿不到请求体：上 `--import` hook（`fetch` + `diagnostics_channel` + `node:http(s)` 三层）+ socket tap 兜底 | 完整请求头 + 请求体 |
| A5 | 若必须改客户端文件才能拿到：装 shim（**仅 A4 不足且有明确收益时**） | 见下方纪律 |

**A5 shim 纪律（强制，逐条对应历史踩坑）**：
1. 先备份为 `.orig.*`；脚本**枚举全部副本**逐个打补丁，不写死单一路径；
2. 重拉子进程必须显式 `ELECTRON_RUN_AS_NODE=1`（否则拉起第二个 GUI 实例 → 单实例锁 → stdout 全空）；
3. **stdout 只准出现转发来的字节**，shim 自身诊断一律写日志文件；
4. 任何外部文件引用（hook/脚本/配置）**写绝对路径**（shim 装在客户端目录，`path.join(HERE,...)` 会静默失效）；
5. 改完必须**完全重启客户端**（worker 常驻，不重启不生效；判定看 `.meta.log` 新时间戳）；
6. 收尾提供**一键还原**，还原后再枚举一遍确认无残留。

**取证纪律（全程）**：
- 抓包优先于源码（R21 教训：源码推的形状漏字段，曾把「请求没对齐」误判成「上游不支持」）
- 交付与备忘**只落**端点 / 模型 ID / 状态码 / 数字用量；**不落** token、Cookie、签名头原值
- 「字段被采纳（行为有变化）」与「能力可见」是两件事，分别记录
- 查不到就写 `unknown`，不猜值

产出：`docs/loomy-协议取证备忘.md`、`docs/raccoon-协议取证备忘.md`

### 阶段 B：可行性门禁（Go / No-Go，硬）
**前置三条（见 §2.2）**：凭据可自持刷新 / 无设备心跳强绑定 / 有效期支撑无人值守。
**再加三条**：
1. 存在可**独立复现**的 HTTP(S) 推理端点（不是只在客户端内部走通）；
2. 最小请求拿到真实模型响应，且**模型身份可核对**（不把展示名或自称当供应商身份保证）；
3. 记录接口许可与条款态度（`model-bridge` 规则 1：未核实就明确记录，不臆造）。

不通过 → 写清阻塞项与原因，**停止**，不进入 C。

### 阶段 C：独立探针 + 凭据导入器（脱离 wild-work）
- 位置：`.gotmp/` 或 `_probe/`（不污染主模块）
- 探针覆盖：模型列表 → 一次流式对话 → 一次非流式 → 错误形态（401/429/业务码）
- 探针自证：打印**实际发出的 body 片段**（否则会把「没发出去」误判成「上游不支持」）
- 导入器：以数据所属账户身份运行 → 输出 `auths/loomy-*.json` / `auths/raccoon-*.json`（R6.3 嵌套格式）
- 通过判据：流式增量真有非空正文（非缓冲后一次性吐）、模型名与目录一致

### 阶段 D：接入 wild-work（触点见 §5）
按 R10 四步走，额外注意：
- 新渠道 auth 前缀不能与既有 glob 冲突（`qoder*.json` 会吞 `qodercn-`/`qodercom-`，见 R10 注）
- 档位投影**默认不做**：仅当阶段 A/B 实测上游真有档位字段才登记 realm，
  且 `SupportsEffortKind` 与 `RealmForKind` **必须同一次改完**（R23）
- 定价与 `/v1/models` 的档位**共用** `reasoning.ListingForKind`（唯一出口，不得另写一份）

### 阶段 E：验收
- `go build ./... && go vet ./... && go test ./...` 全绿
- 三接口各跑一次真实对话（Chat / Responses / Anthropic）
- 一次完整工具调用闭环（调用 → 客户端执行 → 回传 → 最终答复）
- 流式增量、错误分类（429 与业务码判定顺序）、多账号隔离与会话隔离
- **积分/费率面板**：账号积分显示、明细、临期额度（`ExpireAt`）、费率表与 `/v1/models` 口径一致
- 签到（若纳入）：手动按钮 + 定时 + 结果入日志
- **本地重建 `dist/wild-work.exe`**（R6.0：CI 不产出该文件）

### 阶段 F：收尾
- 两份取证备忘（脱敏） + `AGENTS.md` 渠道表与文档索引 + `README.md` + `config.example.json`
- 清理探针、临时进程、hook；确认零凭据入库
- 交付：代码 / 配置说明 / 支持矩阵 / 未验证项清单 / 一键回退步骤

---

## 5. 触点清单（D 阶段细化）

| 文件 | 改动 | 备注 |
|---|---|---|
| `internal/provider/provider.go` | 新增 `Loomy` / `Raccoon` Kind 常量 | Kind 即模型前缀 |
| `internal/loomy/`、`internal/raccoon/` | `client.go`、`constants.go`、`sse.go`、静态模型表 | 实现 `Upstream` 全部方法 |
| `internal/auth/auth.go` | `LoadLoomyDir` / `LoadRaccoonDir` | 前缀 `<channel>-*.json`；glob 边界 |
| `internal/login_<ch>/` | 走 OAuth 时按 `login_qwenwork` 模板；否则做**凭据导入器** | 二选一，取决于 §2.1 结论 |
| `cmd/wild-work/main.go` | 加载凭证 → Pool → Upstream → Scheduler → 两个 Runtime map → 启动日志计数 → `-channels` 列表 | 5 处 |
| `internal/app/app.go` | 登录分支、`complete<Ch>Login`、reload、刷新观察者、`noExplicitCheckin`、费率行（`ListingForKind` 调用处） | 6 处 |
| `internal/server/handler.go` | 模型前缀报错文案 | 1 处 |
| `internal/reasoning/catalog.go` | **仅当实测有档位**：`RealmForKind` + `SupportsEffortKind` 同改 | 守门测试 `TestEffortKindHasOwnRealm` |
| `cmd/wild-work/web/index.html` | 添加账号按钮 | |
| `cmd/wild-work/web/app.js` | `CH_LABEL` / `CH_CLASS` / `NO_EXPLICIT_CHECKIN` / 兜底提示 | 三处表 |
| `config.example.json` | 与 `config.Default()` 同步 | R6.7 |
| `README.md` / `AGENTS.md` | 渠道表、决议项、文档索引 | |
| 积分/费率 | `FetchModelPricing` + `UserResourceDetail` + `data/pricing-cache.json` | 复用既有缓存与 1h 刷新 |

### 5.1 「登录账号」这个动作：现有实现与新渠道设计

**现有渠道的统一抽象**——每个渠道一个 `internal/login_<ch>` 包，导出同样的 4 个函数：

| 函数 | 职责 |
|---|---|
| `NewClient() *http.Client` | 登录用 HTTP 客户端（部分渠道带 cookiejar） |
| `Start(client, statePath) (authURL, error)` | 生成 PKCE / nonce / deviceID → 落状态文件 → **返回授权 URL** |
| `Poll(client, statePath) (Result, ErrPending)` | 轮询并换 token；未完成返回 `ErrPending` |
| `SaveAuth(authDir, r) (path, error)` | **原子写**（`tmp` + `rename`，0600）`auths/<channel>-<uid>.json` |

**编排**（`internal/app/app.go`）：
```
面板点「＋渠道」→ promptLogin(channel) 弹确认框
→ POST /api/login/start {channel} → StartLoginFor(kind) → 返回 authURL
→ 前端打开浏览器授权
→ 后台 pollLogin(ctx)：每 2s 轮询，超时 5 分钟（loginPollEvery / loginTimeout）
→ 成功 complete<Ch>Login(r) → SaveAuth 落盘 → reloadAccounts → afterAccountAdded（拉余额/昵称）
取消：POST /api/login/cancel → CancelLogin() → Shutdown() 关本地监听
```

**7 个渠道的差异只在"怎么把 code 换成 token"**：

| 渠道 | 模式 | 关键点 |
|---|---|---|
| workbuddy（CN） | 浏览器授权 + 服务端签发 state | 无 PKCE |
| workbuddyai（国际） | 同上（host 不同） | pending 判据 = `code=11217` |
| traework | **本地回调** `127.0.0.1:0` + PKCE | 带 15 位 `deviceID` + `x_device_*` 头 |
| qoder / qodercn / qodercom | **Device Flow**（服务端 poll） | `redirect_uri` 为自定义 scheme + machineID / machineToken |
| qwenwork | **本地回调** `127.0.0.1:0/callback` + PKCE S256 | 从 JWT 解析 uid / nickname |

**落盘格式**（`auths/<channel>-<uid>.json`，R6.3 嵌套）：
```json
{ "auth":    { "accessToken", "refreshToken", "expiresAt", "apiHost", "domain",
               "deviceId", "machineId", "machineToken", "machineType" },
  "account": { "uid", "nickname", "enterpriseId" } }
```
> ⚠️ **设备指纹随凭据一起落盘是本项目既有做法**：`auths/qwenwork-*.json` 实测含
> `deviceId / machineId / machineToken / machineType`；qoder 系另有 `EnsureFingerprint()` 给老凭证补指纹。
> **对新渠道的直接含义**：若上游把额度绑设备身份，**导入器必须把
> `desktop-device-identity.json` 的字段一起搬进 auths**，否则换进程 / 换机器即失效。

**新渠道的三种形态（优先级从高到低，判定点在阶段 A）**：

| 形态 | 触发条件 | 面板表现 | 实现 |
|---|---|---|---|
| **A. 正式登录包**（最优） | 客户端用**标准 OAuth**（PKCE / Device Flow），且 client_id、端点、redirect_uri 可从客户端代码取出 | 与现有渠道**完全一致**：「登录 Loomy」→ 浏览器 → 自动完成 | 新建 `internal/login_loomy`、`internal/login_raccoon`，照抄 4 函数契约 |
| **B. 凭据导入器**（次优） | 授权只在客户端内部完成，无法脱离客户端走 OAuth | 按钮文案改为「**从本机客户端导入**」，与"登录"区分 | `internal/login_<ch>/import.go`：以数据所属账户运行 → 解 DPAPI / 读 sqlite → 转标准 auth 文件 |
| **C. shim / TLS 抓包产物**（兜底） | A、B 都不通 | 同 B（走导入器） | 抓到的 token 同样经 B 落盘 |

> 无论 A / B / C，**最终都必须落到 `auths/<channel>-<uid>.json`**——server / pool / scheduler 只认它（R9）。
> B / C 额外要求：只在 Windows 实现（D6），用 `//go:build windows` 隔离；DPAPI 解密在 Go 侧走
> `crypt32.dll!CryptUnprotectData`（`syscall` 直调，保持 `CGO_ENABLED=0`），
> **不依赖 PowerShell**（避开 5.1 / 7 的 `AesGcm` 差异与外部 shell 依赖）。

---

## 6. 风险矩阵

| 风险 | 影响 | 对策 |
|---|---|---|
| 凭据需客户端参与刷新（host/stdio 下发） | 无法并入账号池 | 阶段 B 门禁；不通过则如实标注「一次性试用」 |
| 设备指纹强绑定（D3/D4） | 换机/换用户即失效 | 阶段 A 摸清 `desktop-device-identity.json` 构成；评估可复现性 |
| 推理走私有协议（wss / grpc / 自定义编码） | 无法 HTTP 薄转发 | 门禁不通过即停；shim 路线需单独批准（含维护成本） |
| DPAPI 跨账户（D5） | 读不到凭据 | 导入器以数据所属账户运行，一次导入后自持 |
| 客户端自升级 | shim/hook 失效、版本目录变化 | 不写死路径；枚举副本；一键还原；升级后重装 |
| Loomy `.env.prod` 加密（`LOOMYENC1:`） | 看不到配置 | 优先从 `electron/llm` 明文源码取契约，不硬啃密文 |
| 上游风控 / 条款 | 账号风险 | 只用自有账号、限速、不批量；条款未核实则明确记录 |
| 签到复杂（D1） | 拖慢一期 | 按 §2.3 判据豁免，二期单独补 |
| 反代合规性 | 法务/账号 | 阶段 B 明确记录「许可是否允许第三方客户端调用」 |

---

## 7. 附带发现（**不夹带修改**，供单独决定）

### 7.1 `KeepaliveHours: nil` 被静默补成 `[22]`（2026-09-23 已修复）
`internal/scheduler/scheduler.go:59` 的 `New()` 会把 `KeepaliveHours` 零值补成 `[]int{22}`，
而 `cmd/wild-work/main.go:185` 的千问办公注释写的是「`KeepaliveHours: nil`（无需保活）」——
**注释与实际行为不符：22:00 仍会跑 token 保活**。

> 2026-09-23 已修复：`scheduler.New` 改为区分「nil = 未配置」与「`[]int{}` = 本渠道无此类任务」，受影响的四个渠道（workbuddyai / qwenwork / raccoon / loomy）已改为显式空切片。
影响面：qwenwork（推测其他传 nil 的渠道同理）。是否属缺陷需单独核实（也可能是刻意保留的兜底）。
> 对新渠道的直接影响：**若 Loomy / 小浣熊 确实需要"完全不保活"，不能只传 nil**，须先确认这处行为。

### 7.2 「抠客户端鉴权模块」已有先例
`.gotmp/agent-auth.js` 是千问办公客户端的 `agent-auth` 模块副本（含 `resolveUnpacked` + 原生 `.node` 加载）。
若 Loomy / 小浣熊 采用同构鉴权，可**直接复用这套手法**，成本显著低于从零逆向。

---

## 8. 下一步

1. 你回「批准执行」→ 从 **阶段 A（只读取证）** 开工；A3 起需要你配合启动一次客户端。
2. 阶段 A 结束先交**单项可行性结论**（含 §2.2 三条前置的实测答案），你确认后再进 C/D。
3. 若 A 阶段发现凭据必须客户端参与刷新（§2.2 第 1 条不满足），我会**立即停下报告**，而不是继续写不能用的适配器。
