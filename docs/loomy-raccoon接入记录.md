# Loomy + 商汤小浣熊（raccoon-ai）渠道接入记录

> 状态：**A–F 六阶段全部完成**（2026-09-22 起，最终校验 2026-09-25）
> 本文档由四份阶段文档合并而成（原：`loomy-raccoon渠道接入计划` / `阶段C探针实测` /
> `阶段D实施记录` / `阶段E验收报告`），保留全部实测证据与决策依据，仅合并重复的「未验证项」。
> 依据技能：`model-bridge` + `reverse-skill` + `electron-ai-client-protocol-extract`
> 纪律：本文件只记录**端点 / 模型 ID / 状态码 / 数字用量**，**不含任何凭据、token、账号标识**。

---

## 0. 结论摘要

1. 两个客户端均为 Electron（Windows），已装在本机。
2. **Loomy 取证成本最低**：`resources\app.asar.unpacked\electron\` 是**明文未打包源码**，
   按功能分目录（`auth/ llm/ points/ channel/ nexus/ opencode/`）——契约可直接读，无需反混淆。
3. **小浣熊核心在 `app.asar`**（打包），但 `resources\config`、`cli-bundle`、
   `default-llm-config.json` 明文；userData 有完整 `logs/`（850KB）+ 多个 sqlite。
4. **可行性核心不是"能不能发请求"，而是"凭据能否自持刷新"**（见 §2.2）。
   这决定渠道能否并入账号池，还是只能做一次性试用。
5. 两渠道**均已接入并端到端验证通过**，最终形态见 §8.5 的档位语义与 §12.1 支持矩阵。

---

## 1. 已定决议（D1–D6）

| # | 决议 | 落地含义 |
|---|---|---|
| **D1** | 一期**含积分 + 费率面板**；**签到可豁免**（若麻烦则不做） | 必做 `FetchModelPricing` + `UserResource`/`UserResourceDetail` + 定价缓存；`DailyCheckin` **条件实现**，降级判据见 §2.3 |
| **D2** | 授权到 **③ 装 shim / 抓 TLS** | 阶段 A 可直接上 hook / socket tap 拿完整请求头与请求体；纪律见 §4 |
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
- 因此：若客户端数据属于**用户 A**，而 wild-work 跑在**用户 B**，**以 B 身份无法解密 A 的那份凭据**。

**正解（不是"每次运行都去读客户端"）**：
```
一次导入 → 落到 wild-work 自己的 auths/<channel>-*.json → 之后由 refresh token 自持续期
```
导入器以**数据所属账户**身份运行（用户在其自己的会话里执行，或 UAC 提权），
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

**门禁结论（阶段 B）**：两渠道均 **Go** —— 小浣熊有 refresh（access ≈2h / refresh ≈30d，
轮换需落盘）；Loomy 无 refresh 但 session ≈14 天且可重新导入，按「到期重导入」处理。

### 2.3 签到降级判据（回应 D1 的"如果麻烦"）

满足任一条件即**豁免签到**（按无签到渠道处理）：
- 上游是**服务端被动发放**、无领取接口（同千问办公）；
- 需要**图形验证 / 客户端内交互 / 额外设备指纹**；
- 签到端点未在客户端请求中出现（可能已废弃或走别的域）。

豁免的落地方式（三处**必须同源**，见 R10 与 AGENTS §5）：
| 位置 | 改法 |
|---|---|
| `cmd/wild-work/main.go` | `CheckinMinutes: []int{}`（**显式空切片**，见 §10.1） |
| `internal/app/app.go` | `noExplicitCheckin()` 加该 Kind |
| `cmd/wild-work/web/app.js` | `NO_EXPLICIT_CHECKIN` 集合加该渠道 |

> 签到只影响积分获取节奏，不影响推理可用性 → **可以一期不做、二期单独补**，不阻塞主线。
> **最终结果**：两渠道均豁免（小浣熊无签到活动；Loomy 的 `pet-work` 每日任务待二期评估）。

---

## 3. 本机侦查结论（阶段 A 采集证据）

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

## 4. 阶段 A：契约取证

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

> **A 阶段产出**：`docs/loomy渠道接入备忘.md`、`docs/raccoon渠道接入备忘.md`（静态取证备忘）。

---

## 5. 阶段 C：独立探针 + 凭据导入器

- 位置：`.gotmp/` 或 `_probe/`（不污染主模块）
- 工具：`_probe/c1-probe.mjs`（流式 / 错误形态 / 关思考矩阵）、`_probe/import-auth.mjs`（凭据导入器）
- 全部在**客户端所在机器**（本机 Windows 账户）执行；**token 全程未打印、未落日志**。
- 消耗：两渠道合计约 20 次小请求（`points_consumed` 每次 1，或 <100 tokens）。

### 5.1 验收判据达成情况

| 判据 | 小浣熊 | Loomy |
|---|---|---|
| 流式增量真有非空正文（非缓冲后一次性吐） | ✅ `firstContentEvent=1`（第 2 个事件即有正文），13 事件逐块到达 | ✅ `firstContentEvent=21`（前 20 个事件是思考，符合"思考先于正文"），32 事件 |
| 模型名与目录一致 | ✅ 请求 `raccoon-8c4485` → 响应 `model: raccoon-8c4485` | ✅ 请求 `deepseek-v4-flash-0731` → 响应同名 |
| 探针自证实际发出的 body | ✅ 打印请求体（脱敏） | ✅ 同 |
| 错误形态覆盖 | ✅ 401 / 400 / 静默回落 | ✅ HTTP200+业务码 / 400 |
| 凭据导入器产出标准 auth 文件 | ✅ `auths/raccoon-<昵称>.json` | ✅ `auths/loomy-<userid>.json` |

### 5.2 流式实测

**小浣熊**
```
POST https://xiaohuanxiong.com/api/web/llm/v2/chat/completions
Authorization: Bearer <access_token>   Content-Type: application/json
{"model":"raccoon-8c4485","messages":[…],"max_tokens":200,"stream":true}

→ 200  content-type: text/event-stream; charset=utf-8
   firstByte=3311ms  total=3490ms
   events=13  chunks=12  [DONE]×1
   content=9 chars  reasoning=0 chars  finish=stop  firstContentEvent=1
   usage={completion_tokens:9, prompt_tokens:20, total_tokens:29,
          completion_tokens_details:{reasoning_tokens:0}}
```
事件形状：`data: {"id":…,"object":"chat.completion.chunk","model":"raccoon-8c4485","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`

**Loomy**
```
POST https://loomyad.xunfei.cn/api/v1/chat/completions
Authorization: Bearer <session> + token: <session> + traceparent + loomy-version
{"model":"deepseek-v4-flash-0731","messages":[…],"max_tokens":200,"stream":true,"reasoning_effort":"none"}

→ 200  content-type: text/event-stream
   firstByte=3218ms  total=3491ms
   events=32  chunks=31  [DONE]×1
   content=9 chars  reasoning=75 chars  finish=stop  firstContentEvent=21
   usage={prompt_tokens:96, completion_tokens:31, total_tokens:127,
          completion_tokens_details:{reasoning_tokens:21}, points_consumed:1}
```
事件形状：`data: {"id":"<uuid>","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"We"},"finish_reason":null}]}`
→ **`reasoning_content` 与 `content` 都在 `choices[0].delta`，需要按字段分流**（与本项目既有 Qoder 系处理一致）。

### 5.3 错误形态矩阵（**阶段 D 的 Classify 依据**）

**小浣熊（信封 = LiteLLM 风格）**

| 场景 | HTTP | 响应 | 建议映射 |
|---|---|---|---|
| 无效 token | **401** | `{"code":200003,"message":"authorization_verify_error","details":"authorization verify failed"}` | `ErrSessionDead`（触发 refresh，refresh 失败才禁用） |
| 无 token | **401** | `{"code":200001,"message":"authorization_empty_error"}` | `ErrSessionDead` |
| **未知模型名** | **200** | 正常响应，`model` 回填为**默认模型** `raccoon-8c4485` | ⚠️ **上游静默回落到默认模型** → wild-work **必须本地严格校验模型名**，否则会静默消耗默认模型额度 |
| 空 messages | **400** | `{"error":{"code":"400","message":"litellm.BadRequestError: Custom_raccoonException - do not support message is empty or message is bigger than 70MB for model raccoon/raccoon-work (request_id:…)"}}` | `ErrBadParams`（请求级，不罚号） |

**Loomy（信封 = `{error:{...}}`，鉴权错误走 HTTP 200 + 业务码）**

| 场景 | HTTP | 响应 | 建议映射 |
|---|---|---|---|
| **无效 token** | **200** | `{"code":"100002","desc":"登录已失效，请重新登录","trace_id":"…","data":{}}` | ⚠️ **必须先查业务码再查 HTTP 状态** → `ErrSessionDead` |
| **无 token** | **200** | `{"code":"100002","desc":"缺少 token"}` | 同上 |
| **未知模型名** | **200** | 正常响应，`model` 回填为**默认模型** `deepseek-v4-flash-0731` | ⚠️ 同样**静默回落** → 必须本地校验模型名 |
| 空 messages | **400** | `{"error":{"message":"messages 不能为空","type":"invalid_request_error","param":"messages","code":400,"metadata":{"provider_name":"loomy"}}}` | `ErrBadParams` |

> 与项目既有的 `provider.CodeMarker` 约定一致：**业务码判定必须排在 `status==404` / `status>=500` 之前**
> （AGENTS §6 第 25 条）。Loomy 的 `100002` 已在静态取证中确认属于 `AUTH_ERROR_CODES = {020002, 100002}`。

### 5.4 Loomy「关闭思考」实测矩阵

> ⚠️ **本节结论已作废（2026-09-24，见 §11）**：单次采样 + 用思考字符数当指标均不可靠。
> 保留原始数据作为过程记录。

同一模型 `deepseek-v4-flash-0731`、同一 prompt，仅改思考字段：

| 变体 | reasoning 字符 | reasoning_tokens | points |
|---|---|---|---|
| `reasoning_effort:"none"` | 98 | 31 | 1 |
| `+ enable_thinking:false` | 81 | 28 | 1 |
| `+ enable_thinking:false + chat_template_kwargs:{enable_thinking:false}` | **59** | **16** | 1 |
| 对照：`reasoning_effort:"low"` | 105 | 45 | 1 |

**当时的结论**：
1. 该模型是"强制思考"型 —— 没有任何字段组合能完全关掉思考（最低仍 59 字 / 16 reasoning tokens）。
2. 但三件套有**单调递减效果**（98 → 81 → 59），说明字段**确实被部分采纳**。
3. `points_consumed` 恒为 1 → **计费与档位/思考量无关**（疑按次计）。
4. 实现策略：客户端要求关闭时，**三件套一起发**（`reasoning_effort:"none"` + `enable_thinking:false` +
   `chat_template_kwargs.enable_thinking=false`）并在文档/面板注明「最低档仍可能产生少量思考」。

> 与小浣熊对比：`reasoning: 0`（默认不产生思考），且有 `completion_tokens_details.reasoning_tokens` 字段位。
> **§11 的更正**：以 `reasoning_tokens` 重测后，Loomy 三档在 `deepseek-v4-flash-0731` 上**单调**
> （low 均 173 / medium 均 297 / high 均 459 rtok）；且该系列**仍无法完全关闭思考**（最低档仍约 59 字），
> 故 `ReasoningCanDisable` 保持 false。

### 5.5 凭据导入器产物（`_probe/import-auth.mjs`）

输出到 `wild-work/auths/`（**该目录已在 `.gitignore` 忽略**，不会入库）。

| 渠道 | 文件名 | auth 字段 | account 字段 |
|---|---|---|---|
| raccoon | `raccoon-<昵称>.json` | `accessToken`(380) / `refreshToken`(380) / `expiresAt`(JWT exp) / `domain=/api/web/llm/v2` / `apiHost=https://xiaohuanxiong.com` | `uid=<昵称>` / `enterpriseId=<office_identity>` / `nickname` |
| loomy | `loomy-<userid>.json` | `accessToken`(32) / `refreshToken`(空) / `expiresAt=updatedAt+14d` / `domain=/api/v1` / `apiHost=https://loomyad.xunfei.cn` | `uid=<userid>` / `enterpriseId=""` / `nickname=""` |

- 格式与 `internal/auth.Parse` 的嵌套分支**逐字段对齐**（`accessToken/refreshToken/expiresAt/domain/apiHost/machineId/deviceId/machineToken/machineType`）。
- 写入用 `tmp + rename` 原子替换，`0600`；**默认不覆盖已存在文件**（需 `--force`）。
- Loomy 无 refresh → `expiresAt` 取 `updatedAt + 14d`（客户端登录时请求的 expire），
  避免 `NeedsRefreshLocked` 因 `ExpiresAt<=0` 恒真而触发**永远失败**的刷新；到期后走 `session_dead` 提示重登。
- ⚠️ 小浣熊的 `expiresAt` = access_token 的 JWT `exp`（≈2h）→ **实现必须带 refresh**，否则账号 2 小时后即失效。

### 5.6 产物格式兼容校验（已通过）

用 `internal/auth.Parse` 实测解析导入产物（临时程序 `.gotmp/parsecheck/main.go`，已 gitignore）：

```
OK   raccoon-<昵称>.json         uid="<昵称>"  accessLen=380 refreshLen=380 expiresAt=1790089841
                                apiHost=https://xiaohuanxiong.com  domain=/api/web/llm/v2  needRefresh(5m)=false
OK   loomy-<userid>.json            uid="<userid>" accessLen=32 refreshLen=0 expiresAt=1791277650
                                apiHost=https://loomyad.xunfei.cn  domain=/api/v1  needRefresh(5m)=false
OK   qwenwork-<既有账号>.json    （对照：解析正常，needRefresh(5m)=true）
```

→ **格式与既有渠道完全兼容**，阶段 D 直接加 `Load<Channel>Dir` 即可加载。

### 5.7 给阶段 D 的输入清单

| 项 | 结论 |
|---|---|
| 小浣熊 Classify | 401 + `200001/200003` → `ErrSessionDead`；400 + `litellm.BadRequestError` → `ErrBadParams`；模型名校验**必须本地做** |
| Loomy Classify | **HTTP 200 + `code:"100002"` → `ErrSessionDead`**（业务码优先于状态码）；400 `messages 不能为空` → `ErrBadParams`；模型名校验**必须本地做** |
| 小浣熊 refresh | `POST https://xiaohuanxiong.com/api/electron/auth/v1/refresh`，body `{"refresh_token":…}`，resp `{data:{access_token,refresh_token}}`；**轮换必须落盘**；提前 300s 触发 |
| Loomy 续期 | 无 refresh → `RefreshToken` 返回明确错误（触发 `ErrSessionDead`），面板提示重新登录 |
| 流式解析 | 两渠道同为 `choices[0].delta`，`reasoning_content` 与 `content` 分流；`usage` 在末块；Loomy 另有 `points_consumed` |
| 档位投影 | Loomy：**要投影**（上游目录有全档位），并新建 `RealmLoomy`（须 Clamp）；小浣熊：**不投影且主动剥离**（见 §11） |
| 模型校验 | 两渠道都会**静默回落默认模型** → `runtimeForModel` 之后需按渠道模型表校验，未知模型直接 400（不转发） |

---

## 6. 阶段 D：接入实施

> 状态：**已完成**（2026-09-22 23:20）｜`go build ./... && go vet ./... && go test ./...` 全绿
> `dist/wild-work.exe` 已本地重建（R6.0）。
> 前置：阶段 A 静态取证 → 阶段 B 门禁（两渠道 Go）→ 阶段 C 探针实测。

### 6.1 交付物

**新增代码**

| 文件 | 内容 |
|---|---|
| `internal/raccoon/constants.go` | 端点、静态模型表（8 个）、`KnownModel` |
| `internal/raccoon/client.go` | `provider.Upstream` 实现（含 refresh 轮换落回、模型名本地校验、`Classify`） |
| `internal/raccoon/sse.go` | `Stream`（SSE 透传 + model 重写）/ `Aggregate`（JSON 或 SSE 双路径 + tool_calls 合并） |
| `internal/raccoon/raccoon_test.go` | 11 个用例（错误矩阵、未知模型拒绝、流式透传、聚合、JWT exp） |
| `internal/loomy/constants.go` | 端点、静态模型表（8 个）、倍率解析（`（x3.0）`）、`KnownModel` |
| `internal/loomy/client.go` | 三件套档位投影、`traceparent` 生成、业务码优先的 `Classify`、无 refresh 的明确报错 |
| `internal/loomy/sse.go` | 同 raccoon |
| `internal/loomy/loomy_test.go` | 12 个用例（业务码优先、三件套投影、倍率解析、traceparent 形状…） |
| `internal/app/import_local.go` | **导入器**：路径自适应探测 + 原子写 auths/ + JWT 解析 + `POST /api/account/import_local` 的后端 |

**改动的既有文件（10 个）**
`internal/provider/provider.go`（Kind）｜`internal/auth/auth.go`（`LoadRaccoonDir` / `LoadLoomyDir` / `loadPrefixed`）｜
`internal/reasoning/catalog.go`（`RealmLoomy` + `RealmForKind` + `SupportsEffortKind` + `normalizeRealm` + `staticCap` **五处同改**）｜
`cmd/wild-work/main.go`（装配 11 处）｜`internal/app/app.go`（渠道列表 ×3 + `noExplicitCheckin` + 登录提示 + 新路由）｜
`cmd/wild-work/web/{app.js,index.html,style.css}`（渠道表 ×3 + 导入型弹窗分支 + 两个按钮 + 配色）｜
`README.md`、`AGENTS.md`（渠道表、§6 新增第 25/26 条不变量、文档索引）

### 6.2 端到端实测（真实账号，23:12）

```
GET  /v1/models            → 200，74 个模型（raccoon=8, loomy=8 已并列其中）
POST /v1/chat/completions  loomy/spark-x           → 200 model=loomy/spark-x   finish=stop content='ok' reasoning_len=451 usage✓
POST /v1/chat/completions  raccoon/raccoon-8c4485  → 200 model=raccoon/raccoon-8c4485 finish=stop content='ok' usage✓
POST /v1/chat/completions  raccoon（stream=true）   → SSE 真增量，model 重写为 raccoon/raccoon-8c4485
POST /v1/chat/completions  raccoon/nope-xyz        → 400 model_not_found（本地拒绝，未打到上游）
```

启动日志：`loaded accounts: ..., raccoon=1, loomy=1 from ./auths` ✓
积分：`credit auto-refresh platform=loomy uid=… remain=15000 expiring=0 unusable=0` ✓

### 6.3 实施中发现并修复的 3 个问题

**3.1 Loomy 积分端点前缀写错（404）**

静态取证时把 `points-service.js` 里的 `this.get('/api/v2/points/records')` 误当成相对 `APIPrefix(/api/v1)`，
实际客户端传的是**含 `/api/` 的完整路径**。修正为
`/api/v1/points/records`、`/api/v2/points/records`、`/api/v1/team-points/balance`（站点根 + 完整路径）。
修正后实测 `remain=15000`。

> **后续修正（2026-09-23）**：上面那个 `remain=15000` 本身就是**漏了每日积分**的结果 ——
> 上游 `data` 里还有 `dailyBalance`（每日池）与 `availableBalance`（= 常规池 + 每日池）。
> 现已改用 **v1 面**并取 `availableBalance`（实测 **19800**），拆「积分 / 每日积分」两条明细。
> 详见 `docs/loomy渠道接入备忘.md` §6 的「余额口径」。

**3.2 非流式聚合丢内容（Loomy）**

首版 `aggregate` 只按 SSE 解析，而**上游对非流式请求直接返回 JSON**（普通 `chat.completion`），
于是产出「`content: ""` + `created` 用 `time.Now()` 兜底」的假响应 —— **静默丢全部内容**。
改为**先探测 JSON、再回落 SSE**（`aggregate` / `aggregateSSE` 双路径），两渠道同改。

**3.3 小浣熊 `refresh_conflict`（2026-09-25 实测更正了早期结论）**

阶段 D 曾观察到：wild-work 与客户端同时刷新时，上游返回
`400 {"code":200822,"message":"refresh_conflict","details":"refresh token conflict or reused"}`，
随后账号被标 `session_dead`。当时的结论是「**refresh_token 单会话**（两主体不能共用）」。

**2026-09-25 专项探针（§9 第 7 项）实测后更正**——真实语义是「**token 单次消费，会话可多个**」：

| 实测项 | 结果 |
|---|---|
| refresh 是否轮换 | ✅ access + refresh **都换发**（`refresh.exp` 前移一个周期） |
| 轮换后**旧 refresh** 能否再用 | ❌ **不能** —— `refresh_conflict / refresh token conflict or reused` |
| 轮换后**旧 access** 能否再用 | ✅ **能**（实测仍 200）—— 刷新不溯及既往 |
| 同一会话内轮换 | ✅ `sid` **不变**，仅 `jti` 变（session 内换发） |
| 不同 sid 能否并存 | ✅ **能** —— 客户端（`f9f152deee`）与 wild-work（`911fd26cd5`）同时在线，双方各自查余额均 200 |

**结论**：

- 上游的约束是「**同一个 refresh_token 只能被消费一次**」，不是「同账号只能有一个会话」；
- 冲突的真正前提是**两主体共用了同一份 token**——先刷的那个把它消费掉，后刷的才报 conflict；
- **各自持有独立 token 的两个会话可以长期并存**，互不影响。

**这直接推翻「导入后必须退出客户端」的旧结论**（更正见 §6.4）：

| 凭据来源 | 与客户端的关系 | 能否并存 |
|---|---|---|
| **协议劫持登录**（面板主按钮） | 完整 OAuth 授权码流程 → 服务端**新建会话**（新 sid） | ✅ **天然独立，可并存** |
| **导入器**（面板次按钮） | **原样复制**客户端 `auth.json` 的 token（**同一 sid**） | ⚠️ 共用一份 → 谁先刷谁作废对方 |

> **判据是 `sid`（会话 ID）**：`sid` 不同即两个独立会话；`sid` 相同说明是同一份凭据的拷贝。
> 实测三处凭据的 sid 互不相同（客户端 / 项目 auths / Downloads auths），印证会话可多份并存。

处理（保留，仍有效）：
- 并发场景的**根治**：`internal/provider/refresh.go` 的按账号单飞
  （`RefreshOnce`），实测 8 并发由「1 成功 + 7 conflict + 冷却」变为「8 成功 + 0 conflict」，
  见 §7.4 —— 这条解决的是**同一进程内**的并发自踢，与客户端无关，仍然必要；
- 为降低抢刷新频率，最初曾把 `raccoon` 移出积分自动刷新循环；**找到余额端点后已加回**（余额要刷）；
  token 另由 4 小时保活维护。

### 6.4 使用约束（需在文档/面板上让用户看得见）

1. **小浣熊可与官方客户端并存**：用**协议劫持登录**（面板主按钮）会新建独立会话，
   两边各自持有 token、各刷各的，互不影响（2026-09-25 实测）。
   只有走**导入器**时才需要注意——它复制的是客户端同一份 token，
   两边会抢着消费同一个 refresh_token，先刷的作废后刷的；
   若已导入，让 wild-work 先刷新一次即可脱离共用（`sid` 会在该次刷新后保持独立）。
2. **Loomy 无续期**：session 约 14 天，到期需在客户端重新登录并再次点「导入」；
3. **模型名写错会本地 400**（`model_not_found`）—— 这是刻意设计，因为两个上游都会静默回落默认模型；
4. 小浣熊的**余额与费率都能显示**：余额取 `GET /api/web/points/v1/balance`（四个池子按 `Usable` 拆分，
   `topup_frozen` 时充值池标为不可用），费率取 `billing_multiplier`。

### 6.5 附：本次未纳入的事

- `internal/traework/live_probe_test.go` 是**并行会话的 WIP**（未跟踪状态），本次未触碰、未 add；
- 阶段 C 的探针工具与 asar 工具保留在 `_probe/`、`_raccoon_probe/`（工作区，非仓库内容）。

---

## 7. 阶段 E：验收

> 状态：**验收通过**（2026-09-23）｜被测二进制：`dist/wild-work.exe`（含定时任务修复）
> 账号：traework / qoder / qodercn / qwenwork / raccoon / loomy 各 1（workbuddy、qodercom 无凭据）

### 7.1 结论

**验收通过**：三接口、工具调用闭环、错误分类、并发与会话隔离、积分/费率口径、Web UI 六项全部达成。
过程中发现 **1 个并发缺陷（已修复并 A/B 验证）** 与 **3 个需知晓的事实**，详见 §7.4。

### 7.2 结果总表

| 项 | 结果 | 关键证据 |
|---|---|---|
| E1 三接口 | ✅ | `/v1/chat/completions`、`/v1/responses`、`/v1/messages` 两渠道均 200；`count_tokens` 正常 |
| E1b 三接口流式 | ✅ | 三接口 SSE 事件序列均符合各自官方协议 |
| E2 工具调用 | ✅ | raccoon 默认模型 `finish_reason=tool_calls` + 结构化 `tool_calls`；loomy 8 模型中 7 个通过 |
| E3 错误分类 | ✅ | 未知模型本地 400 `model_not_found`（未打到上游）；未知渠道 400 `invalid_model` |
| E4 并发与会话隔离 | ✅ | 6 渠道并发，响应 `model` 与请求**逐一对应，无串号** |
| E5 积分/费率口径 | ✅ | 明细四池正确；费率表与 `/v1/models` **8/8 完全匹配** |
| E6 Web UI | ✅ | `index.html` / `app.js` / `style.css` 三处均含新渠道 |
| 重建 `dist/wild-work.exe` | ✅ | `go build -ldflags "-H windowsgui"` |

### 7.3 逐项明细

**E1 三接口**

| 渠道 | `/v1/responses` | `/v1/messages` | `/v1/messages/count_tokens` |
|---|---|---|---|
| raccoon | 200 `object=response status=completed` text=`ok` | 200 `type=message stop_reason=end_turn` text=`ok` | `{"input_tokens":3}` |
| loomy | 200 同上 | 200 同上 | `{"input_tokens":3}` |

三接口均返回 `usage`；`/v1/messages` 在 raccoon 上耗时 19.8s（上游较慢），loomy 2.4s。

**流式形态**（loomy `Kimi-k2.6`；raccoon 因账号冷却跳过）：

| 接口 | content-type | 事件序列 | 首文本 |
|---|---|---|---|
| `/v1/chat/completions` | `text/event-stream` | `data:` chunk ×8 + `[DONE]` | 1.0s |
| `/v1/responses` | `text/event-stream` | `response.created` → `in_progress` → `output_item.added` → `content_part.added` → **`output_text.delta` ×5** → `output_text.done` | 5.0s |
| `/v1/messages` | `text/event-stream` | `message_start` → `content_block_start` → **`content_block_delta` ×5** → `content_block_stop` → `message_delta` → `message_stop` | 5.5s |

三接口的 SSE 事件序列均符合各自官方协议（Responses 与 Anthropic 的事件名逐一对应）。

**E2 工具调用闭环**

raccoon 逐模型（`tool_choice=required`，单线程顺序测）：

| 模型 | 结果 |
|---|---|
| raccoon-8c4485 / raccoon-19b265 / sn-sensenova-6-8-flash-lite / sn-glm-5-3 / sn-glm-5-3-flash / sn-deepseek-v4-1-flash | ✅ `finish=tool_calls` + 结构化 `tool_calls` |
| **sn-kimi-k3** | ❌ `finish=stop`，输出**文本** `content='get_weather({"city": "Beijing"})'` |
| raccoon-405a1c | ⚠️ 请求时 503（瞬时不可用），**复测 200 正常**，工具能力未验证 |

loomy 逐模型（`tool_choice=required`）：

| 模型 | 结果 |
|---|---|
| deepseek-v4-flash-0731 / MiniMax-M3 / Kimi-k2.6 / qwen-3.8-max / GLM-5.3-Flash / qwen3.8-flash / mimo-v2.5 | ✅ `finish=tool_calls` |
| **spark-x** | ❌ `finish=stop`，把调用写成**文本** `content='get_weather("Beijing")'` |

> 7/8 通过 → 链路无缺陷；`spark-x` 是模型自身行为（上游仍声明 `function_calling: true`）。

**E3 错误形态**

| 场景 | 结果 |
|---|---|
| `raccoon/nope-xyz`、`loomy/nope-xyz` | **400 `model_not_found`**，本地拒绝（未打上游）✅ |
| 空 `messages` | 400，透传上游 `litellm.BadRequestError` |
| `nosuch/x`（未知渠道） | 400 `invalid_model`：`provider "nosuch" is not configured` |

**E4 并发与会话隔离**：6 渠道并发 1 轮，5 个成功且 `resp_model == req_model`（**无串号**）；qwenwork 返回 503（见 §7.5）。

**E5 积分 / 费率**

| 渠道 | credits | 明细 |
|---|---|---|
| loomy | 15000 | `积分 total=15000 remain=15000 usable=true` |
| raccoon | 6249 | 每日 249 + 奖励 6000 + 充值 0 + 月度 0 |

费率表与模型表口径：`raccoon` 8/8、`loomy` 8/8，**差集为空**；费率取上游倍率（raccoon 0.2~0.75、loomy 0.1~3）。

**E6 Web UI**：`GET /` 200（13.2KB）含 `raccoon` / `loomy` / `小浣熊` / `Loomy`；`app.js`、`style.css` 同样命中。

### 7.4 发现的问题

**4.1 ✅ 并发刷新竞态（已修复，A/B 对照验证）**

现象：并发请求同一账号且本地 token 已过期时，每个请求都各自触发一次刷新 ——
raccoon 实测 4 并发 → **1 次成功 + 3 次 `refresh_conflict`**，账号被冷却 10 分钟并返回 503。

```
01:56:09  refresh start platform=raccoon uid=<昵称> reason=request   ×4（同一秒）
01:56:10  refresh failed ... {"code":200822,"message":"refresh_conflict"}
01:56:10  refresh success platform=raccoon uid=<昵称> expires_at=1790110570
→ /api/state: cooling=true until=09-23 02:06
```

**根因**：`internal/app/app.go` 的 `refreshIfSessionDead` 无并发保护。
已有 `refreshMu`（app.go:94）注释是「防并发刷新积分」，只用于 `RefreshPricing`，**与 token 刷新无关**。

**更底层的原因**：渠道的 `RefreshToken` 只在**写字段**时持 `auth.mu`，HTTP 调用在锁外 ——
该锁只能防数据竞争，拦不住「两次刷新都真的打上游」。串行执行也照样各发一次请求，
第二个用的还是已被轮换的旧 refresh_token。

**影响面**：不止 raccoon —— 任何 **refresh_token 会轮换**的渠道都中招（qwenwork 同为此类）。
raccoon 因 access 仅 ≈2h、过期频繁，最容易复现。

**补充**：补测时用**单线程**顺序请求，7 个模型里仍有 1 个瞬时 503 —— 说明竞态不止发生在
并发请求之间，**后台循环（credit / pricing，每 30 分钟）与请求路径之间**同样会撞车。
该 503 复测即恢复（200），无持久影响。

**修法**（已实施）：新增 `internal/provider/refresh.go`，按账号做单飞（singleflight）。

```go
func RefreshOnce(a *auth.Auth, fn func() error) error
```

- 单飞键 = auth 文件路径（回落 UID）→ 跨渠道天然隔离，不同账号互不阻塞；
- 6 个调用点统一改用（app.go 3 处 + scheduler.go 2 处 + handler.go 1 处），闭包内做
  「重检 `NeedsRefresh` → 刷新 → `SaveAtomic`」，等待者醒来重检发现已被刷过就直接返回；
- 手写 map + channel 实现，**不引入 `golang.org/x/sync` 依赖**（本仓 go.mod 刻意保持极小）。

**A/B 对照验证**（同一场景：把 raccoon 的 `expiresAt` 推到过去，8 个请求同时放行）：

| 指标 | 无单飞（对照） | 有单飞（修复后） |
|---|---|---|
| `refresh start` | 8 条 | 8 条 |
| `refresh success` | 1 条 | **8 条（`expires_at` 完全相同）** |
| `refresh_conflict` | **7 条** | **0 条** |
| 账号冷却 | **是**（`冷却 1`） | 否 |
| 请求成功 | **1/8** | 6/8 |

对照组日志原文（禁用单飞后重建二进制）：
```
03:01:55.378726 refresh success platform=raccoon uid=<昵称> expires_at=1790114515
03:01:55.382689 refresh failed ... {"code":200822,"message":"refresh_conflict","details":"refresh token conflict or reused"}
   ... 共 7 条 conflict
```
修复后日志原文：
```
03:02:41.055~057  refresh start platform=raccoon uid=<昵称> reason=request          ×8
03:02:41.290603   refresh success platform=raccoon uid=<昵称> expires_at=1790114561  ×8（同一毫秒）
```

> 修复后仍有 2/8 请求失败（503），但**原因与刷新无关**：是上游推理超时（120s），
> 日志里 `冷却 0` 且刷新全部成功。同时段对照：loomy 串行 4/4 成功（1.2s），
> raccoon 串行 2/4（2 个 120s 超时）→ 属小浣熊上游侧的间歇性慢响应。

守门测试：`internal/provider/refresh_test.go`（6 例：并发单飞 / 不同账号独立 / 失败结果共享 /
无粘性缓存 / 无法定 key 时直通 / panic 唤醒等待者），全部通过 `-race`。

**4.2 raccoon 能力标记不准（已修）**

`internal/raccoon/client.go:283` 原为 `SupportsTools: false`，注释写「未实测」。
本次实测默认模型支持结构化工具调用 → 已改为 `true` 并注明依据与未验证范围（`sn-*` 系列）。
影响：面板此前会把 raccoon 标成「不支持工具」。

**4.3 各有一个模型不产出结构化工具调用**

| 渠道 | 模型 | 上游声明 | 实测 |
|---|---|---|---|
| loomy | `spark-x` | `function_calling: true` | `finish=stop` + 文本形式调用 |
| raccoon | `sn-kimi-k3` | 无 tools 声明（本地按实测标 true） | 同上 |

属模型自身行为，本地无法预知 → 能力标记仍按上游声明，建议在文档注明例外。
（raccoon 侧因上游不声明能力，`SupportsTools` 已按实测改为 true，见 4.2）

**4.4 qwenwork 凭据已失效（既有问题，需重登）**

```
INVALID_REFRESH_TOKEN / invalid_grant（refresh token is invalid）
→ /api/state: disabled=true, reason="refresh session dead"
```

凭据 `expiresAt` 停在 **2026-09-21 02:32**（已 2 天）。这是**「与千问办公 App 互踩」的现实证据** ——
refresh_token 被轮换后两边互相作废。**与本次改动无关**（改前 22:00 保活也会失败并禁用）。

处理：在千问办公客户端重新登录后，面板「从本机客户端导入」。

> **根治进展（2026-09-23）**：`cmd/wild-work/main.go` 已把 qwenwork 的
> `CheckinMinutes` / `KeepaliveHours` 改为**显式空切片** `[]int{}`（即停掉定时保活），
> 互踩的主要来源「daemon 定时刷新 vs 客户端刷新」已消除。
> **尚未实现的是另一半**：按需刷新后**写回 `auth-v2.dat`** —— 参见
> `docs/千问办公QwenWork逆向对比备忘.md` §3.5（该项由上游作者提出，本项目未纳入当前计划）。

**4.5 ✅ 503（`no_healthy_account`）不打日志（可观测性缺口，已修复）**

`internal/server/handler.go` 的三个 503 分支只 `writeOpenAIError`，**无 `log.Printf`**。
本次 405a1c 的瞬时 503 因此在 `app.log` 里**查不到任何线索**，只能靠复测反推。

> **2026-09-23 已修复**（`404ec45`）：三处各补一行 `log.Printf`，沿用既有 `key=value` 风格 ——
> `reason=no_account` / `reason=all_unavailable` / 第三支按有无 `lastErr` 打 `err=` 或 `reason=no_remaining`。

---

## 8. 渠道最终形态（D/E 实测修正）

### 8.1 支持矩阵

见 `README.md` 的「渠道能力对照」。两渠道的关键差异：

| | 商汤小浣熊 | Loomy（讯飞） |
|---|---|---|
| 鉴权头 | `Authorization: Bearer` 单头 | `Authorization` + `token` 双写 + **`traceparent`**（缺则挂死到超时） |
| 凭据位置 | `%USERPROFILE%\.box-agent\config\auth.json`（**明文 JSON**） | `C:\Users\Public\Loomy\<sha256(用户)[:12]>\userData\auth-session.json` |
| 续期 | refresh（access ≈2h / refresh ≈30d，**token 单次消费、会话可多个**） | **无 refresh 端点**，session ≈14 天，到期重新导入 |
| 思考档位 | **不接档位且主动剥离** `reasoning_effort`（网关认该字段，但实测默认档最深，下发反而削弱约 90%） | 全档位（上游 `/models` 的 `reasoning_efforts` 权威，独占 `RealmLoomy`，投影时 Clamp） |
| 工具调用 | 实测 8 模型中 6 个 | 实测 8 模型中 7 个 |
| 积分 | `/api/web/points/v1/balance`（每日/奖励/充值/月度四池） | `/api/v1/points/records`、`/team-points/balance` |
| 签到 | 无活动（`DailyCheckin` 返回「无签到活动」） | 无（`pet-work` 每日任务待二期评估，`DailyCheckin` 返回未实现） |
| 使用约束 | **可与官方客户端并存**（协议劫持登录 = 独立会话；导入器 = 共用一份凭据） | 到期后在客户端重登，再点一次「导入」 |

### 8.2 阶段 A 的「登录形态」判定结果（更正早期判断）

| 形态 | 触发条件 | 面板表现 | 实现 |
|---|---|---|---|
| **A. 正式登录包**（最优） | 客户端用**标准 OAuth**（PKCE / Device Flow），且 client_id、端点、redirect_uri 可从客户端代码取出 | 与现有渠道**完全一致**：「登录 Loomy」→ 浏览器 → 自动完成 | 新建 `internal/login_<ch>`，照抄 4 函数契约 |
| **B. 凭据导入器**（次优） | 授权只在客户端内部完成，无法脱离客户端走 OAuth | 按钮文案改为「**从本机客户端导入**」，与"登录"区分 | `internal/login_<ch>/import.go`：以数据所属账户运行 → 解 DPAPI / 读 sqlite → 转标准 auth 文件 |
| **C. shim / TLS 抓包产物**（兜底） | A、B 都不通 | 同 B（走导入器） | 抓到的 token 同样经 B 落盘 |

**判定结果（2026-09-23 实测，更正早期判断）**：

- **小浣熊 = 形态 B（导入器）**。它**有**网页授权码流程（`/code/authorize` → 深链
  `office-raccoon://auth/callback?code=…` → `POST {authApi}/login_with_authorization_code`），
  但回调地址由服务端前端**硬编码**、全站 JS 无 `redirect_uri`，第三方拿不到 code。
  早期把它列为「形态 A 候选」是因为只查了主进程 `main.js`，而登录实现在独立模块
  `build/electron/main/desktopLogin.js`。完整链路见 `docs/raccoon渠道接入备忘.md` §11。
- **Loomy = 形态 B（导入器）**。走讯飞账号体系（HMAC-SHA1 签名 + 短信/账密），
  无第三方可复现的授权流程。
- 形态 A 已于 2026-09-23 落地（面板主按钮）：**劫持 `office-raccoon` 协议注册**截获授权码后自兑 token，
  实现见 `internal/login_raccoon`，细节见 `docs/raccoon渠道接入备忘.md` §11.3 / §12。
  导入器作为次按钮保留。

> 无论 A / B / C，**最终都必须落到 `auths/<channel>-<uid>.json`**——server / pool / scheduler 只认它（R9）。
> B / C 额外要求：只在 Windows 实现（D6），用 `//go:build windows` 隔离；DPAPI 解密在 Go 侧走
> `crypt32.dll!CryptUnprotectData`（`syscall` 直调，保持 `CGO_ENABLED=0`），
> **不依赖 PowerShell**（避开 5.1 / 7 的 `AesGcm` 差异与外部 shell 依赖）。

### 8.3 档位投影的最终语义（2026-09-24 实测修正）

- **Loomy**：接档位。上游 `/models` 下发 `reasoning_efforts`（8 模型一致：none/low/medium/high/xhigh，
  default=low），独占 `RealmLoomy`，投影时调 `reasoning.Caps.Clamp` 按该模型 ladder 降级。
- **raccoon**：**不接档位**（`SupportsEffortKind` 不含它），且**主动剥离** `reasoning_effort` ——
  实测上游默认档才是最深思考，下发任何档位反而削弱约 90%（详见 `docs/raccoon渠道接入备忘.md` §13）。
- 定价与 `/v1/models` 的档位**共用** `reasoning.ListingForKind`（唯一出口，不得另写一份）。

---

## 9. 未验证项（合并 C/D/E 三处清单）

> **本节是收尾遗留清单，非当前待办。** 下列第 1–5、7 条已核销；第 6、8、9 条仍待验证，
> 第 10 条属刻意的功能豁免（非缺口）。各条已合并原 C §7 / D §5 / E §5 的去重内容。

**已核销：**

1. ~~`/v1/responses` 与 `/v1/messages` 在两渠道上的表现~~ —— ✅ **已核销**（§7.3 E1）：两渠道三接口均 200，流式事件序列符合各自官方协议；
2. ~~工具调用闭环~~ —— ✅ **已核销**（§7.3 E2）：raccoon 默认模型 + loomy 8 模型中 7 个通过（例外为模型自身行为）；
3. ~~`raccoon-405a1c` 的工具调用~~ —— ✅ **已核销**：§7.4 的刷新单飞修复后该账号恢复正常；同批其余 6 个模型全部通过，链路无缺陷；
4. ~~小浣熊 `sn-*` 与 `visible:false` 内部模型的可用性~~ —— ✅ **已核销**（§7.3 E2）：逐模型实测，仅 `sn-kimi-k3` 不产出结构化 tool_calls（模型行为）；
5. ~~Loomy 的 `reasoning_effort` 对非 deepseek 模型是否同样可关~~ —— ✅ **已核销**（§11 及 `docs/loomy渠道接入备忘.md` §11）：以 reasoning_tokens 重测，档位单调有效。
7. ~~小浣熊 refresh 真实轮换的成功路径~~ —— ✅ **已核销**（2026-09-25 专项探针
   `internal/raccoon/live_probe_test.go`）：轮换确认换发双 token；旧 refresh 立即作废（`refresh_conflict`）、
   旧 access 仍可用；同一会话 `sid` 不变。**副产物**：推翻了「单会话」旧结论（详见 §6.3）。

**仍待验证：**

6. **429 / 限流形态** —— 两渠道均未触发过，`Classify` 的限流分支仍无真实上游数据覆盖
   （单测 `raccoon_test.go:28-29` / `loomy_test.go:33` 已覆盖分类逻辑，缺的是真实响应样本）。
   **性质**：无法主动触发（阶段 C 实测 1900+ 次、230 req/min、64 并发均无 429），
   强打等于对上游压测，与「限速不批量」纪律冲突 → **属客观不可验证，非欠账**；
8. **Web UI 交互**（按钮点击、导入流程）—— 验收只验证了静态资源包含关系，未做浏览器交互。
   **性质**：需人工在浏览器操作，非探针可覆盖；
9. **同渠道多账号隔离** —— 当前每渠道仅 1 个账号，只验证了跨渠道并发隔离（E4），
   未验证同渠道多账号的路由与配额分配。**性质**：账号数量不足，非代码缺口。

**刻意豁免（非缺口）：**

10. **签到** —— 按 D1 决策**刻意豁免**（两渠道均无签到活动，`raccoon.DailyCheckin` 返回「无签到活动」、
    `loomy.DailyCheckin` 返回「pet-work 每日任务待二期评估」）。这是功能范围决议，**不是缺陷或欠账**。

---

## 10. 附带发现（**不夹带修改**，供单独决定）

### 10.1 `KeepaliveHours: nil` 被静默补成 `[22]`（2026-09-23 已修复）

`internal/scheduler/scheduler.go` 的 `New()` 会把 `KeepaliveHours` 零值补成 `[]int{22}`，
而 `cmd/wild-work/main.go` 的千问办公注释写的是「`KeepaliveHours: nil`（无需保活）」——
**注释与实际行为不符：22:00 仍会跑 token 保活**。阶段 E 排查「渠道 token 是否需要定期刷新」时发现
`len(cfg.KeepaliveHours) == 0` 把 **nil 与显式空切片一视同仁**，都补成默认值。结果是四个
「注释声明已关闭」的渠道实际仍在跑定时任务：

| 渠道 | 注释声明 | 实际（修复前） |
|---|---|---|
| `workbuddyai` | 「Keepalive 关闭（token 365 天）」 | 每天 22:00 保活 |
| `qwenwork` | 「nil + nil（定时保活会与千问办公 App 互踩）」 | 9:00/21:00 签到 + 22:00 保活 —— **正是注释要避免的互踩** |
| `raccoon` | 无签到活动 | 每天 9:00/21:00 调 `DailyCheckin` 并返回错误 |
| `loomy` | 无 refresh 端点、无签到 | 每天 3 次空跑（`refreshToken` 为空会提前 skip，**不会误禁用账号**） |

**修复**（`internal/scheduler/scheduler.go`）：

- `nil` = 未配置 → 落默认；`[]int{}` = 本渠道无此类任务 → 保持为空；
- `Run` 对「无任何定时任务」显式阻塞等待 —— 否则 `nextFireMinutes` 对空列表返回零值，
  timer 会立即触发而空转烧 CPU。

**配置**（`cmd/wild-work/main.go`）：上述四渠道改用显式空切片；`raccoon` 每 4 小时的
保活**保留**（access 仅 ≈2h，必要）。

新增测试 `internal/scheduler/scheduler_defaults_test.go`（4 例：零值默认、显式空关闭、
混合场景、无任务时 `Run` 阻塞且可被 ctx 取消）。

> **结论（已定）**：确认为**缺陷**（注释声明的行为与实际不符），非「刻意保留的兜底」——
> 受影响四渠道的定时任务已按注释本意全部关闭，并有守门测试。
> 对新渠道的结论：**要"完全不保活"须传显式空切片 `[]int{}`，只传 nil 会被落回默认**。

### 10.2 「抠客户端鉴权模块」已有先例

`.gotmp/agent-auth.js` 是千问办公客户端的 `agent-auth` 模块副本（含 `resolveUnpacked` + 原生 `.node` 加载）。
若后续渠道采用同构鉴权，可**直接复用这套手法**，成本显著低于从零逆向。

---

## 11. ⚠️ 后续更正：档位结论（2026-09-24，`baaff96`）

本文档 §5.4（原阶段 C 的「关思考矩阵」）**档位相关结论已作废**，
其余（端点 / 错误形态 / 流式形状）仍然有效。

| 原文结论 | 更正后 |
|---|---|
| 「关闭思考三件套递减最优，但最低仍产生 59 字思考」 | 单次采样 + 用**思考字符数**当指标，均不可靠。以 `usage.completion_tokens_details.reasoning_tokens` 重测：Loomy 三档在 `deepseek-v4-flash-0731` 上**单调**（low 均 173 / medium 均 297 / high 均 459 rtok）；`max_tokens` 不足会截断思考，进一步掩盖差异 |
| 「小浣熊：**不投影**」 | 更准确的表述：**不接档位且主动剥离** `reasoning_effort` —— 网关认该字段，但实测**默认档最深**，下发任何档位反而削弱约 90%（详见 `docs/raccoon渠道接入备忘.md` §13） |
| 「Loomy：要投影（上游目录有全档位）」 | 正确，但**还须按模型 ladder `Clamp`**（原实现漏了，`baaff96` 已补），否则超限档位（`max`/`ultra`）被原样下发 |

另修（同批 `baaff96`）：

- `internal/loomy/client.go` 的「三件套档位投影」当时**漏了按模型 ladder 降级**（`reasoning.Caps.Clamp`
  未调用，与 qoder 三渠道不一致）→ 已补；并补了档位日志（`loomy reasoning: model=... in=... out=...`）。
- `internal/raccoon/client.go` 当时未处理档位字段 → 已加 `forceUpstreamDeepThinking`
  （**剥离** `reasoning_effort`，因实测上游默认档最深）+ 剥离日志。
- 两渠道现均按此语义工作，详见各渠道备忘的档位章节。

---

## 12. 风险矩阵与回退

### 12.1 风险矩阵

| 风险 | 影响 | 对策 |
|---|---|---|
| 凭据需客户端参与刷新（host/stdio 下发） | 无法并入账号池 | 阶段 B 门禁；不通过则如实标注「一次性试用」 |
| 设备指纹强绑定（D3/D4） | 换机/换用户即失效 | 阶段 A 摸清 `desktop-device-identity.json` 构成；评估可复现性（实测两渠道均不校验） |
| 推理走私有协议（wss / grpc / 自定义编码） | 无法 HTTP 薄转发 | 门禁不通过即停；shim 路线需单独批准（含维护成本）→ 实测均为 HTTP(S) |
| DPAPI 跨账户（D5） | 读不到凭据 | 导入器以数据所属账户运行，一次导入后自持 |
| 客户端自升级 | shim/hook 失效、版本目录变化 | 不写死路径；枚举副本；一键还原；升级后重装 |
| Loomy `.env.prod` 加密（`LOOMYENC1:`） | 看不到配置 | 优先从 `electron/llm` 明文源码取契约，不硬啃密文 |
| 上游风控 / 条款 | 账号风险 | 只用自有账号、限速、不批量；条款未核实则明确记录 |
| 签到复杂（D1） | 拖慢一期 | 按 §2.3 判据豁免，二期单独补 → 已豁免 |
| 反代合规性 | 法务/账号 | 阶段 B 明确记录「许可是否允许第三方客户端调用」 |

### 12.2 触点清单（阶段 D 实际改动）

| 文件 | 改动 | 备注 |
|---|---|---|
| `internal/provider/provider.go` | 新增 `Loomy` / `Raccoon` Kind 常量 | Kind 即模型前缀 |
| `internal/loomy/`、`internal/raccoon/` | `client.go`、`constants.go`、`sse.go`、静态模型表 | 实现 `Upstream` 全部方法 |
| `internal/auth/auth.go` | `LoadLoomyDir` / `LoadRaccoonDir` | 前缀 `<channel>-*.json`；glob 边界 |
| `internal/login_raccoon/` | 协议劫持登录（形态 A 落地）+ 导入器次按钮 | 见 §8.2 |
| `cmd/wild-work/main.go` | 加载凭证 → Pool → Upstream → Scheduler → 两个 Runtime map → 启动日志计数 → `-channels` 列表 | 11 处 |
| `internal/app/app.go` | 登录分支、`complete<Ch>Login`、reload、刷新观察者、`noExplicitCheckin`、费率行 | 6 处 |
| `internal/server/handler.go` | 模型前缀报错文案（**2026-09-23 补齐**：此前是手写串、漏了 workbuddyai/qodercom/raccoon/loomy，已改为按 `Config.Runtimes` 动态生成） | 1 处 |
| `internal/reasoning/catalog.go` | `RealmLoomy` + `RealmForKind` + `SupportsEffortKind` 同改 | 守门测试 `TestEffortKindHasOwnRealm` |
| `cmd/wild-work/web/{index.html,app.js,style.css}` | 添加账号按钮、`CH_LABEL` / `CH_CLASS` / `NO_EXPLICIT_CHECKIN` / 兜底提示 | 三处表 |
| `config.example.json` | 与 `config.Default()` 字段对齐（已核对，`err_threshold` 已修正为 3） | R6.7 |
| `README.md` / `AGENTS.md` | 渠道表、决议项、文档索引 | |
| 积分/费率 | `FetchModelPricing` + `UserResourceDetail` + `data/pricing-cache.json` | 复用既有缓存与 1h 刷新 |

### 12.3 移除这两个渠道（阶段 F 回退）

⚠️ **不要 `git revert` 那个渠道提交** —— 其后的 `2a273cb`（定时任务零值）与 `37093ac`（刷新单飞）
是**通用修复**、改动了同一批文件，回滚会连带撤销它们。手动移除的完整清单
（照 `git show <渠道提交> --stat` 逐项对照）：

1. 删包：`internal/raccoon/`、`internal/loomy/`
2. `internal/provider/provider.go`：去掉 `Raccoon` / `Loomy` 两个 Kind 常量
3. `internal/auth/auth.go`：去掉 `LoadRaccoonDir` / `LoadLoomyDir`（及 `loadPrefixed` 对应调用）
4. `internal/reasoning/catalog.go`：去掉 `RealmLoomy` 常量，以及 `RealmForKind` / `SupportsEffortKind`
   里的 loomy 分支 —— **两处必须同删**，只删一处会让档位表落进 `RealmCN`，污染 WorkBuddy 国内版
5. `cmd/wild-work/main.go`：去掉两个 Runtime 的装配与 `-channels` 列表项
6. `internal/app/app.go`：去掉导入渠道分支与费率行
7. `internal/app/import_local.go`：可整体删除
8. `cmd/wild-work/web/{index.html,app.js,style.css}`：去掉按钮与 `CH_LABEL` / `CH_CLASS` /
   `NO_EXPLICIT_CHECKIN` 三处表项
9. 运行时数据：`rm -f auths/{raccoon,loomy}-*.json data/state-{raccoon,loomy}.json`；
   `data/pricing-cache.json` 可整体删除（下次启动自动重建）
10. 重建：`go build -ldflags "-H windowsgui" -o dist/wild-work.exe ./cmd/wild-work`

### 12.4 相关文档

- `docs/loomy渠道接入备忘.md` —— Loomy 协议取证（端点/签名算法/登录 API/积分端点 + A3 实测 + 档位实测 + 流式修复）
- `docs/raccoon渠道接入备忘.md` —— 小浣熊协议取证（端点/凭据文件/refresh 链路 + A3 实测 + 登录全貌 + 档位剥离 + 流式修复）
