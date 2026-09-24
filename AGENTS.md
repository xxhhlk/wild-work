# Wild-Work 项目提示词（AGENTS.md）

> 本文件面向 AI Agent 与开发者，记录**工具设计、架构选型、决议项**与开发约定。
> **项目渊源**：原始上游为 3 个分离的 xxx2api 仓库 → workbuddy-wild v0.1.x（wails GUI 包装器）→ v0.2.x（增加 traework 集成）→ v2.0.x（大改版弃用 wails，改用系统托盘 daemon + Web UI，增加 qoder 渠道）。
> 当前 `master` 分支为 v2.0.x 版本；v0.2.x 已迁移至 `legacy-wails` 分支。

---

## 0. 项目渊源

Wild-Work 是 WorkBuddy（国内版+国际版）/TraeWork/Qoder 多渠道账号聚合工具，演进历程：

1. **上游 API 仓库**：3 个独立仓库 (`wild-work-buddy-api`, `wild-work-traework-api`, `wild-work-qoder-api`) → 提供各渠道基础 API 封装
2. **v0.1.x (workbuddy-wild)**：Wails GUI 包装器，仅支持 WorkBuddy 单渠道
3. **v0.2.x**：增加 TraeWork 集成，仍用 Wails
4. **v2.0.x (当前 master)**：大改版弃用 Wails，改用**系统托盘 daemon + 浏览器 Web UI**；新增 Qoder 渠道
5. **v2.1.x**：新增 **WorkBuddy 国际版**（`www.workbuddy.ai`）渠道，与国内版 `workbuddy` 完全独立

> ⚠️ v0.2.x 代码已迁移至 `legacy-wails` 分支，不再维护。

## 0. 项目一句话

去掉 wails/WebView2，改为 **系统托盘 daemon + 系统浏览器 Web UI** 的多平台（Win/mac）多渠道账号聚合工具。

## 1. 已定决议项（不要推翻，除非有强理由并更新本节）

| # | 决议 | 说明 |
|---|------|------|
| R1 | **托盘菜单固定，不做动态内容、不做定时/事件刷新** | 用户在自己客户端操作无法捕捉，动态展示无意义 |
| R2 | ~~托盘提供「刷新积分」菜单~~ **已移除**。刷新积分改为 Web UI 面板操作 | 托盘菜单精简为：打开主界面 / 查看日志 / 退出 |
| R3 | 托盘固定菜单项：**打开主界面 / 查看日志 / 退出** | 双击托盘 = 打开主界面；不再弹"已启动"提示框 |
| R4 | **不设管理 API 鉴权** | 单机个人工具；监听 0.0.0.0 的风险由用户承担，UI/文档给一句风险提示 |
| R5 | **Web UI 用纯静态 HTML/CSS/JS**（无前端编译链） | `go:embed` 打进单文件；实用 + 大众审美即可 |
| R6 | 托盘库：**保留 energye/systray**（已跨平台 Win/mac/Linux） | 各菜单项使用不同颜色纯 Go 生成图标，无需外部图标文件 |
| R7 | **移除 wails / WebView2 全部依赖** | 省内存与运行时；平台能力封装进 `internal/platform`（build tag 拆分） |
| R8 | daemon 单进程：一个 `http.Server` 同时服务 OpenAI 端点 + 管理 API + 静态 UI | 沿用 server 现有 ServeMux 扩展 |
| R9 | 核心业务（pool/scheduler/upstream/traework/server/login/config/auth/provider）**整体复用**，格式零迁移 | config.json / auths/ / data/state.json 兼容旧版；旧 state.json 自动迁移到 state-workbuddy.json |
| R10 | 新增渠道扩展方式：实现 `provider.Upstream` 接口 + auth 加载器 + 注册 Runtime | 模型前缀 `channel/<model>` 路由；已实现 WorkBuddyCN(国内) + WorkBuddyAI(国际) + TraeWork + TraeCode(与 TraeWork 共账号，function=solo_agent) + QoderCN + QoderCOM(国际) + 千问办公(qwenwork) + 商汤小浣熊(raccoon) + Loomy(loomy) + MonkeyCode(monkeycode，平台托管模型) + OpenCodeZen(oczen 匿名) 十一渠道；旧 Qoder（`qoder/*`，QoderWork）已从界面下线但路由保留 |
| R11 | Windows 产物在 WSL 交叉编译（`GOOS=windows CGO_ENABLED=0`，已验证可行）；macOS 产物走 GitHub Actions macos-latest（cgo 必需） | WSL 无法编 darwin cgo；CI 增加 darwin job |
| R12 | **无桌面 Linux 使用 `--no-tray` 参数** | 无参启动在无 DBus 环境托盘 panic 直接 exit 并提示；`--no-tray` 跳过托盘打印信息阻塞等待 Ctrl+C |
| R13 | **三接口兼容采用两层结构：内层 handler 不动，新增 `internal/gateway` 边缘层**，经 **in-process 调用**（`io.Pipe` + ResponseWriter 形状）复用内层 | 代码量比内联重构多 20%，但改动面小一个数量级（主链路仅 2 处调用点 + 1 个访问器），回归风险低、可脱离 pool 单测。**不得用 HTTP 自环**（`0.0.0.0` 监听不可作目标、鉴权双份、启动竞态） |
| R14 | **`Stream`/`Aggregate` 的 model 由调用方显式传入**，渠道不得用实例字段记忆「上次请求的模型名」 | 旧实现 qoder 用全局 `lastModel`，多账号并发会串号；traework 恒为空串。详见 `docs/三接口兼容改造备忘.md` §3 |
| R15 | **Responses 的 `function_call` 必须是独立 output item**（带 `call_id`），Anthropic 的 tool_use 参数必须走 `input_json_delta` | 参考实现 `tokligence-gateway` 两处写法不合规范（塞进 `message.content`、start 里一次性给完整 input），Codex/Claude Code 会解析失败 |
| R16 | **思考强度统一走 `internal/reasoning`**：客户端各写法（`reasoning_effort` / `reasoning.effort` / `thinking.*` / `output_config.effort` / `enable_thinking` / `disable_reasoning` / `think`）在**内层 handler**（`prepareChatBody`）归一化成顶层 `reasoning_effort`，非法/自相矛盾回 400 `invalid_reasoning_control`；**渠道层只做方言投影**（WorkBuddy 家族 → low/high/max；Qoder → `is_reasoning` + `parameters.reasoning_effort`/`enable_thinking`；TraeWork 协议无此字段），不重复做兼容字段解析 | 移植自 Buddy2api `reasoning_controls.py`。规则只应存在一处：渠道各自解析会让「同一客户端写法在不同渠道表现不同」。默认档 `compat.reasoning_effort` 只在**客户端未表达**时注入，对 WorkBuddy 双面与 Qoder 生效（Qoder 的具体档位由渠道层按模型 ladder 就近降级）。Loomy 按 `RealmLoomy` ladder Clamp；**raccoon 例外：必须剥离该字段**（其实测默认档最深，下发档位反而削弱思考量，见 §13 备忘） |
| R17 | **Responses 的思考链是独立 `reasoning` output item，且必须排在 `output_index=0`**；`output_index` 按实际输出顺序动态分配（思考 → 文本 → 工具），不得硬编码 | 官方顺序要求思考先于回答；硬编码 message=0 会让带思考的响应出现倒序 item。思考增量只在文本开始前接受，文本开始后到达的片段丢弃 |
| R18 | **档位必须按模型能力就近降级，不得按模型家族固定压档**：能力表优先取上游目录接口的 `reasoning.supportedEfforts`/`defaultEffort`（`provider.ModelInfo.SupportedEfforts` → `internal/reasoning.Caps`），缺失才回落 `internal/reasoning/catalog.go` 的 realm 静态表；未收录模型档位原样透传 | 上游各模型可接受档位差异很大（国内版 `deepseek-v4-pro` 无 `max`、国际版 `deepseek-v4.1-flash` 只认 `high`、`glm-5.1` 只认 `medium`），旧的「deepseek 家族固定压 low/high/max」会发出非法档位。国内版/国际版**分表**，绝不混用（同一模型两面档位不同） |
| R19 | **DeepSeek 系「开思考」= `thinking:{"type":"enabled"}` + 档位，二者缺一上游按不思考应答**；网关在客户端表达开思考时自动补 `thinking.type`，并给 assistant 消息回填 string 类型的 `reasoning_content`（多轮一致性）。由 `compat.deepseek_thinking`（默认开）统一开关，客户端显式给出的 `thinking.type` 绝不覆盖 | 逆向官方客户端 `codebuddy.js`（`thinkingFormat:"deepseek"` + `requiresReasoningContentOnAssistantMessages`）的结论；此前只发 `reasoning_effort`，DeepSeek 思维链可能一直为空。与参考实现差异：**不在客户端未表达时强行开思考**，避免给不需要思考的请求增加延迟与额度开销 |
| R20 | **Qoder 思考投影按官方客户端 `bve()` 的三处同源写法**：`model_config.is_reasoning` + `parameters.reasoning_effort` + `parameters.enable_thinking`，三者必须同源（绝不出现 `is_reasoning=true` 配 `enable_thinking=false`）；档位能力来自上游模型目录的 `thinking_config`（`provider.ModelInfo` → `reasoning.Caps` 的 `RealmQoder` 面，**与 WorkBuddy 分表**，无静态兜底）；客户端要关闭但该模型无 `disabled` 节点（如 `glm-5.3`）时**降到最低档**而不是发上游不认的 `none`；`parameters` 恒下发（见 R21） | 逆向 Qoder CN 桌面版内置 SDK（`@qoder-ai/qoder-cn-agent-sdk` 的 `qoder-worker-runtime.obf.mjs`，CLI v1.1.53）拿到；实测 `parameters.reasoning_effort` 确实改变生成量（`none` 2402 < 基线 2952 < `medium` 3455 tokens）。**R21 已推翻「legacy 端点不下发思考链」这条结论**（当时是因为请求体/请求头没对齐桌面版） |
| R21 | **Qoder 请求体与请求头按桌面版「实测抓包」逐字段对齐**（不再只参考 SDK 源码）。body：补顶层 `system` 数组（从 system 消息抽文本块，与 `messages[0]` 同构）、`task_id:"common"`、`source:1`、`version:"3"`、`is_retry:false`、`session_type:"app"`、`aliyun_user_type:""`、完整 `model_config`（`key/display_name/model/format/is_vl/is_reasoning/api_key/url/source/max_input_tokens`）、`business` 富对象（`product/version/type/id(=request_set_id)/name/begin_at/stage`）、`tools` 恒为数组、`chat_context.text` 与 `extra.originalContent` 为**字符串**；`parameters` **恒下发**且含 `max_tokens`（目录 `max_output_tokens`，实测目录无此字段 → 常量 32000）与 `context_length`（`context_config` 中标 `is_default` 的档，未知则不下发）。headers：`cosy-clienttype: 10`、`cosy-data-policy: disagree`、`cosy-version: 1.1.57`（签名 payload `cosyVersion` 必须同改）、补 `cosy-business-product/-type/-scene`、`cosy-machineos: x86_64_win32`、`cosy-machinehostname`、`accept-language`，去掉桌面端没有的 `cosy-clientip` | 依据 `_spy/http-bodies/*.json`（7 个真实请求体）+ `_spy/qoder-real-request.json`（27 个真实请求头）。**对齐后 legacy `agent_chat_generation` 立刻开始下发可见思考链**：探针 `reasoning_content` 1876（medium）/26145（xhigh）字，生产链路端到端 36845 字，`usage.completion_tokens_details.reasoning_tokens` 1435–11913 —— 这是「思考强度终于可见」的关键修复。回归护栏：`TestLiveProbeProductionPath`（走 `ChatStream` 全链路）。**唯一刻意保留的差异**：`accept-encoding` 固定 `identity`（桌面端是 `br,gzip,deflate`；Go 手动设置该头后不会自动解压，brotli 需额外依赖）。**版本同步要求**：`clientVersion` 同时出现在请求头与 `business.version`，改动必须成对 |
| R22 | **无账号渠道（oczen）不建 auth 文件、不进 `reloadAccounts`、不参与禁用/冷却惩罚** | 匿名凭证是常量 `public`；`SyncToDir` 会剔除磁盘上不存在的虚拟账号，故只在装配时注入一次。单账号 + 不可重登 ⇒ 任何账号级冷却都等于整渠道下线，故 4xx 一律走新增的 `ErrPassthrough`（原文透传、不计错不冷却），只有 429 才短冷却。渠道特性见 `internal/oczen/constants.go` 包注释与 §6 不变量 29/30 |
| R23 | **用量/积分双流水分口径统计，不强关联、不折算** | `internal/ledger` 双 JSONL（usage 按渠道×模型 / credit 按账号 earn·spend·expire）；写入仅 append 缓冲句柄（30s AutoFlush），读取仅在 UI 请求 `/api/usage` 时按月分段扫描聚合，常驻内存 ≈0。`Upstream.Stream` 返回末帧 usage（R14 同款显式传参哲学）。首见账号只记一条「存量额度」baseline，不逐条展开。详见 `docs/用量积分流水记账备忘.md` |
| R24 | **临期阈值可配（默认 24h，下限 24h）** | `config.schedule.expiring_threshold_hours`，normalize 钳下限（日期粒度到期判定低于一天无意义）；scheduler 与 app.creditTotals 同源取 `cfg.ExpiringThresholdDur` |
| R25 | **TraeWork 专用池判据是 `product_id==209`** | 2026-09-23 起上游不再下发 `available_endpoint=1`（专用池也标 0），ep 判据整体失效；实测三账号 `product_id=209`（200 档每日签到）used 恒为 0，判定改为 `ep==1 \|\| pid==209`（ep 保留为历史兑底）。pid=208（150 签到）/221（每月登录）均可消耗 |

## 2. 架构选型（依据）

| 主题 | 选型 | 理由 |
|------|------|------|
| GUI 壳 | **无**（删除 wails） | WebView2 内存开销大 + Windows 绑定；托盘 + 浏览器足够 |
| 托盘 | energye/systray v1.0.3（现有） | 已跨平台；菜单固定方案规避其不可删菜单项限制 |
| 管理后端 | 现有 http.Server 扩展 /api/* | 单端口、复用鉴权中间件（无鉴权）、零新服务 |
| Web UI | 纯静态 embed + fetch | 无 Node 构建链，单 exe 双击即用 |
| 登录 | 复用 internal/login + login_trae | 纯 HTTP + 本地回调端口，跨平台 |
| 平台能力 | internal/platform + build tag（windows/darwin/other） | 浏览器无痕/开机自启/消息框/日志/工作区，接口同名 |
| 构建 | WSL 交叉编译 win；CI macos-latest 编 darwin | 见 R11 |

## 3. 托盘菜单设计（当前形态）

```
wild-work
──────────
打开主界面          → 系统浏览器打开 http://<listen>/
查看日志            → 系统默认编辑器打开 data/app.log
──────────
退出                → 退出 daemon（确认框）
```

- 单击/双击/右击：右击弹菜单；**单击与双击 = 打开主界面**
- 各菜单项使用不同颜色纯 Go 生成图标（蓝色=打开、灰色=日志、红色=退出）
- 刷新积分功能已移至 Web UI 面板操作

## 4. Web UI 页面规划（纯静态，一个 index.html + app.js + style.css）

| 页面/区块 | 内容 |
|-----------|------|
| 顶部栏 | 品牌名/版本号、API 地址（点击弹窗配置）、API-Key（点击弹窗修改）、帮助/关于 |
| 账号管理 | 双列卡片网格，账号名/UID/积分/签到状态，图标按钮操作（签到/刷新/停用/删除） |
| 自动签到 | 签到时间（HH:MM 多组）+ 开机自启开关（左右布局） |
| 渠道费率 | 七渠道模型定价表（按渠道分组，合并单元格），刷新按钮 |

**外部静态资源只有一处：echarts（用量折线图），走 ZStatic `s4.zstatic.net` + SRI。**
不得改用 BootCDN / Bootcss / Staticfile / Polyfill.io —— 这几家已被黑产收购并实控，
2024-07 起发生供应链投毒（uBlock Origin 等已直接屏蔽），实测 `cdn.staticfile.org`
现已完全不可达；`cdn.staticfile.net` 属同一家，一并避开。
- `index.html` 的 `<script>` 必须同时带 `integrity`（SRI）与 `crossorigin="anonymous"`：
  CDN 或链路被劫持时浏览器会拒绝执行篡改后的脚本，退化成 `app.js` 的降级提示
  （`typeof echarts === "undefined"`），不会执行恶意代码。
- **SRI 值必须取自 npm 官方 tarball 逐字节校验**（`registry.npmjs.org/<pkg>/-/<pkg>-<ver>.tgz`），
  不得照抄第三方页面或凭 CDN 当前返回内容直接采信 —— 否则等于把「是否被篡改」的判据
  也交给同一个可能被投毒的源。
- 跨域 SRI 要求响应带 `access-control-allow-origin`（zstatic 实测 `*`）；
  换 CDN 前先确认该头，否则脚本会被 CORS 拒绝、整块图表静默失效。
- **升级 echarts 版本时必须重新计算 SRI**，否则脚本被浏览器拒绝执行。
  算法：`openssl dgst -sha384 -binary f.js | openssl base64 -A`（前缀 `sha384-`）。

管理 API（REST，均挂 `/api/*`）：

```
GET  /api/state                    # 全量状态（账号/积分/签到/配置）
POST /api/login/start              # {channel} → {auth_url}
POST /api/login/cancel
POST /api/account/checkin          # {uid}
POST /api/account/checkin_all
POST /api/account/refresh          # {uid}
POST /api/account/refresh_all
POST /api/account/remove           # {uid}
POST /api/account/disable          # {uid,disabled} 停用/启用
POST /api/account/resource_detail  # {uid} → 积分明细
POST /api/config/checkin_times     # {times:["09:00","21:30"]}
POST /api/config/listen            # {host,port}
POST /api/config/api_key           # {key}
POST /api/config/autostart         # {on:bool}
GET  /api/fees                     # 渠道费率（本地缓存 + 按需刷新）
POST /api/fees/refresh             # 异步刷新费率
GET  /api/logs                     # 最近 300 行日志
POST /api/quit                     # 退出程序
```

## 5. 渠道（已实现 WorkBuddyCN + WorkBuddyAI 国际版 + TraeWork + TraeCode + QoderCN + QoderCOM 国际版 + 千问办公 + 商汤小浣熊 + Loomy + OpenCodeZen 匿名；旧 Qoder 已下线）

1. 新建 `internal/<channel>/` 包，实现 `provider.Upstream` 接口
2. `internal/auth` 增加对应 `Load<Channel>Dir()`（文件名前缀 `<channel>-*.json`；
   **glob 边界**：`qoder*.json` 会吞掉 `qodercn-`/`qodercom-` 前缀，LoadQoderDir 必须显式排除）
3. 装配处注册 `server.Runtime{Kind, Pool, Upstream, StaticModels}` + `app.Runtime{..., Scheduler}`
4. 前端渠道选择器加一项（`web/app.js` 的 `CH_LABEL` + `CH_CLASS`，前缀/帮助清单自动跟随，见 §6 第 31 条）；
   `internal/login_<channel>` 实现登录编排（如需）
   > **无账号渠道（oczen）跳过第 2、4 步**：不建 auth 文件与加载器，虚拟账号由 `main` 装配时注入 pool，
   > 且 `app.reloadAccounts` 不得纳入（否则 `SyncToDir` 会把它剔除）。

> provider.Kind 即模型名前缀；server 按 `channel/<model>` 前缀路由，无需改接口。
> **QoderCN（`qodercn/*`）**：qoder2api 参数形态（cosyVersion 1.0.10、18 头含 cosy-scene 族、
> session_type=qoder、identity userType 实测回填）；签到双路径（campaigns 主 + daily-check-in 兑底，
> 实测 legacy 已 DISABLED）；每日 10:15 定时签到；动态模型表（无静态兑底，上次成功缓存）。详见 `docs/qoderCN渠道接入备忘.md`。
> **QoderCOM（`qodercom/*`）**：国际版（qoder.com/openapi.qoder.sh/api1+api2.qoder.sh 三域分离）；
> 凭据与 CN 区完全隔离（双向 401）；签到仅 campaigns（无 daily-check-in，实测 404）；每日 10:15。详见 `docs/qoderCOM渠道抓包分析与接入计划.md`。
> **旧 Qoder（`qoder/*`，QoderWork）已从界面下线**：代码与路由保留，存量账号仍可用；不新增功能，后续可移除。
> 旧 Qoder 无签到活动：`DailyCheckin` 返回错误，调度器只做 token keepalive；故 `noExplicitCheckin`
> 排除 `qoder`（面板不显示手动签到按钮），web/app.js 的 `NO_EXPLICIT_CHECKIN` 与之同源。
> 签到定时任务须用**显式空切片** `CheckinMinutes: []int{}` 关闭（2026-09-23 修复：原为 `nil`，
> 被 `scheduler.New` 补成默认 9:00/21:00，每天两次必然失败的签到调用与失败日志）。
> QoderCN / QoderCOM 已实现签到（campaigns 主路径），保留手动按钮。
> WorkBuddyAI 国际版：`DailyCheckin` 实现为「免费模型对话保活 + 签到探测」（对用户透明，无前端界面）；
> token 有效期 365 天，故 KeepaliveHours 设为**显式空切片** `[]int{}`（传 nil 会被 `scheduler.New` 补成默认 22:00）。协议要点见 `internal/workbuddyai/constants.go` 包注释。
> **商汤小浣熊（`raccoon/*`）**：官方托管网关 `https://xiaohuanxiong.com/api/web/llm/v2`（OpenAI 兼容，
> 鉴权只认 `Authorization: Bearer <access_token>`）；凭据来自本机客户端
> `%USERPROFILE%\.box-agent\config\auth.json`（明文 JSON，access ≈2h / refresh ≈30d，**refresh 会轮换且单会话**）；
> 积分：`GET /api/web/points/v1/balance`（**与推理网关不同前缀** —— 不在 `/api/web/llm/v2` 下，容易找漏）
> → `available_points` + 四个池子（每日/奖励/充值/月度），`topup_frozen` 标记充值池冻结，
> 由 `UserResourceDetail` 按池拆分并用 `ResourceItem.Usable` 表达冻结；费率取上游 `billing_multiplier`。
> **思考**：不接档位且**主动剥离** `reasoning_effort` —— 实测上游默认档才是最深思考，
> 下发任何档位（含 high）反而让思考量锐减约 90%（`internal/raccoon/client.go` 的
> `forceUpstreamDeepThinking`）。详见 `docs/raccoon渠道接入备忘.md` §13。
> **流式**：走独立的 `StreamHTTP`（**无总超时**）+ `IdleReader` 空闲兜底 —— 本渠道思考最深、
> 生成期最长，最易触发整请求总超时导致的流中断（见 §6 第 32 条）。
> 详见 `docs/raccoon渠道接入备忘.md`。
> **Loomy（`loomy/*`）**：讯飞自有网关 `https://loomyad.xunfei.cn/api/v1`（OpenAI 兼容，SSE）；
> 请求头必须带 `Authorization` + `token`（双写）+ **`traceparent`**（缺失会挂死到超时）+ `loomy-version`；
> 凭据来自 `C:\Users\Public\Loomy\<sha256(用户)[:12]>\userData\auth-session.json`（session ≈14 天，**无 refresh 端点**）；
> 档位来自上游 `/models` 的 `reasoning_efforts`（权威值，独占 `RealmLoomy` 面），
> 客户端三档（low/medium/high，客户端 `loomy:thinking-level`）→ `reasoning_effort` + 三件套，
> 投影时按该模型 ladder `reasoning.Caps.Clamp` 就近降级。
> **流式**：同 raccoon，走独立的 `StreamHTTP`（**无总超时**）+ `IdleReader` 空闲兜底。
> 详见 `docs/loomy渠道接入备忘.md`。
> **OpenCodeZen（`oczen/*`，匿名免费）**：凭证固定字面量 `public`，无账号/无签到/无积分；
> 免费档有三道闸门（规范 `ses_<12hex><14Base62>` 会话头 + `stream:true` 且 tools 含 `bash`/`read` +
> OpenCode CLI 伪装头），缺一即 403 FreeTierError；面板固定一项「[OpenCodeZen] 匿名」、积分显示「不适用」，
> 不可增删停用。渠道特性（匿名凭证 `public`、三道免费档闸门、伪装头清单）见
> `internal/oczen/constants.go` 包注释与 §6 不变量 29/30。

## 6. 关键不变量（改动前必读）

0. **每次代码变更后必须本地重新构建 `dist/wild-work.exe`**（见 §8）。
   `dist/` 在 `.gitignore` 中，CI 只产出带平台后缀的 `wild-work-<os>-<arch>`，
   **不会**生成 `dist/wild-work.exe`——该文件只能手动构建。
   不重建会导致：本地运行的二进制与源码不一致（例如改了版本号但仍显示旧版本）。
1. `PrepareBody` 三改写勿动：强制 `stream=true`、`tool_choice` 归一化、`developer→system`
2. 日志/面板/消息框**零 token**：不得输出 access/refresh token（调试用假 token）
3. auth 文件嵌套格式 `{auth:{...},account:{...}}`，`internal/auth.Parse` 与 login.SaveAuth 必须一致
4. `config.listen` 兼容新对象格式 + 旧字符串格式 `":7863"`
5. `data/state-*.json` 只增不减字段，向后兼容；旧 state.json 自动迁移
6. 托盘回调必须 goroutine 化
7. `config.example.json` 与 `config.Default()` 同步
8. 上游 HTTP ≥400 错误直接透传原始响应，不包装
9. 定价缓存持久化到 `data/pricing-cache.json`，启动加载，超 1h 自动刷新
10. 无桌面 Linux 必须 `--no-tray`，不带参数 panic 直接 exit 提示
11. **粘性路由**：`pickWithSticky` 优先复用上次账号，直至连续成功请求达 50 次或遭遇错误冷却。成功时 `stickySuccess` 递增计数，错误时 `stickyClear` 清除粘性记录。不使用 credits 阈值（pool 中余额是 stale 数据）。
12. **`internal/server` 主链路不得被绕过**：`POST /v1/chat/completions` 与 `GET /v1/models` 由内层直接服务，`internal/gateway` 只接管 `/v1/responses`、`/v1/messages`、`/v1/messages/count_tokens`。
13. **兼容层调用内层只能经 `Gateway.call()`**（`io.Pipe`），调用方读完必须 `res.Close()`，否则内层 goroutine 可能阻塞在 Write 上泄漏。
14. **`pipeRW.Flush()` 为空操作是刻意的**：`io.Pipe` 无缓冲，Write 即送达；不要改成缓冲 + 定时 flush。
15. **错误分类 429 必须优先于 hardMarkers**：限流 body 高频带 `quota exceeded`，先判 hardRule 会把限流误归余额耗尽 → 12h 硬冷却。三渠道 `Classify` 均已修复此顺序。
    - **唯一例外：429 + 业务码 14018**（积分耗尽 —— 结构化码，不是文案）→ 硬冷却弃号。业务码判定必须走 `provider.CodeMarker`（容忍 `{"code": 14018}` 的 JSON 空白与引号形态），字面量 `strings.Contains` 会漏判。
16. **脱敏层仅做文本替换不做语义变更**：`internal/sanitize` 只改模板句、不改用户内容语义；预检不命中时零分配原样通过。将来配置 `features.sanitize_fingerprints` 可一键关闭（逃生门）。
17. **积分「可用/不可用」拆分统计**：`provider.ResourceItem.Usable` 标记条目是否属于本工具可消耗的额度池，`provider.Summarize()` 汇总小计。
    - TraeWork 判据（2026-09-23 更新，R25）是 **`available_endpoint==1 \|\| product_id==209` 为不可用**：
      上游已不再下发 ep=1（专用池也标 0），ep 判据仅作历史兑底；实测三账号 `product_id=209`
      （200 档每日签到）used 恒为 0。**不得用 `group_type` 判定**——同名「每日签到」既有
      通用份也有专用份。早期仅用 ep 判定的实砰证据见 `docs/upstream-reverse-engineering.md` §2.3。
    - `UserResource` / `UserResourceDetail` 返回的 remain **只能是可消耗余额**，
      否则 pool 会按虚高余额选号。含专用池的总量（`usage_summary.total_amount`）不能作路由依据。
    - 不可消耗额度仅用于面板展示（`pool.Status.UnusableCredits`），不参与 `Pick()` 排序；
      展示的唯一目的是让用户看到的总积分能和官网对上。
18. **到期时间字段因渠道而异，缺失则不显示**：WorkBuddy 系是 `CycleEndTime`（**上游从不下发 `PackageEndTime`**，旧判据恒 miss），
    TraeWork 是 `expire_time`（Unix 秒），Qoder 无此字段。均按 **UTC+8 墙钟**解析（`softRateResetLoc`），
    用 `time.Local` 会在非 UTC+8 机器上算错一天。上游未下发时 `ResourceItem.ExpireAt` 必须为空串，
    前端据此隐藏整列——**不得用零值时间冒充「永不过期」**。
19. **401 必须自愈，不能只信本地 `expiresAt`**：上游刷新会作废旧 access token（refresh token 同步轮换）。若新 token 未落盘、
    或同一账号在别处被刷新，本地文件里的 token `expiresAt` 仍在未来，但上游已拒绝 → `NeedsRefresh` 恒为假、永不刷新、
    积分恒 0、明细恒空。因此积分/明细/费率路径遇 `ErrSessionDead` 必须「refresh + 落盘 + 重试一次」
    （`app.refreshIfSessionDead`，scheduler 的 checkin 路径同理）。
20. **凡是调 `Upstream.RefreshToken` 的地方必须紧跟 `SaveAtomic`**：refresh token 会轮换，不落盘 = 下次启动用旧 refresh token，
    重回上一条的死锁（`RefreshPricing` 曾漏，已补）。
21. **Qoder 的客户端版本号只有一处定义（`internal/qoder/clientVersion`）**：它同时出现在请求头 `cosy-version`、
    签名 payload 的 `cosyVersion`、以及 body 的 `business.version`。三处必须同值 —— 只改头会让上游看到
    「头说 1.1.57、body 说别的」的自相矛盾身份。桌面版升级后照 `_spy/qoder-real-request.json` 重新取值。
22. **Qoder 的 `parameters` 恒下发**（至少带 `max_tokens`），且**请求体/请求头形状以实测抓包为准，不以 SDK 源码为准**：
    R20 时期按源码推的形状少了顶层 `system`、`task_id`、完整 `model_config` 等字段，导致 legacy 端点一直不下发
    `reasoning_content`，被误判成「上游不支持」。改动 Qoder 请求形状前后，必须跑 `TestLiveProbeProductionPath`
    （走 `ChatStream` 全链路，断言 `reasoning_content` 非空）。
23. **档位「对外声明」只有一处入口（`reasoning.ListingForKind`）**：`/v1/models` 与面板费率表
    （`/api/fees`）共用它取档位，其中已含「该渠道是否有档位能力」的判断（TraeWork / 千问办公必须为空）。
    新增渠道或更换档位来源时只改这一处——两处各写一份判断必然漂移，面板显示的档位就会与实际下发的档位不一致。
    - **`SupportsEffortKind` 与 `RealmForKind` 必须同一次改完**：前者放行而后者未登记该渠道时，
      能力会写进 `RealmCN`（`RealmForKind` 的 default 分支），污染 WorkBuddy 国内版的档位表；
      且 `HasRemote("cn")` 变真后 `ensureEffortCaps` 对真实国内版渠道直接 return，远端权威源被饿死。
      守门测试：`TestEffortKindHasOwnRealm`。
    - **Qoder / QoderCN / QoderCOM 三个渠道各占一个 realm**（`RealmQoder` / `RealmQoderCN` / `RealmQoderCOM`）：
      `Catalog.SetRemote` 是「整桶替换」（`remote[realm] = bucket`），共用面时后拉到的渠道会整份覆盖
      先拉到的，而三者模型目录互不相通（同名模型 ladder 未必相同）→ 按错 ladder 降级发非法档位。
      守门测试：`TestQoderRealmIsolation`（各包另有投影侧隔离用例）。
    - **QoderCN / QoderCOM 的档位下发已线上实测**（2026-09-22，QoderCN 真实账号）：上游接受
      `parameters.reasoning_effort` 与 `parameters.enable_thinking`，档位梯度真实存在
      （low 2206 字 < medium 2502 字 < xhigh 180s 超时截断）。**保守守卫保留**：模型未声明
      `thinking_config` 时只翻 `model_config.is_reasoning`、不下发档位字段，与「面板/`/v1/models`
      不声明该模型档位」严格对齐。完整实测矩阵见 `docs/qoderCN渠道接入备忘.md` §8。
    - ⚠️ **`parameters.enable_thinking` 必须恒下发**（与 `model_config.is_reasoning` 同源），
      不能只在档位非空时写：实测只发 `is_reasoning=false` 而缺 `enable_thinking` 时上游
      **关不掉思考**，反而思考爆炸（3127 个 reasoning 块 / 1.06MB，180s 未收尾、正文 0 块）；
      补上 `enable_thinking=false` 后同一请求 53.6s 收尾、reasoning 块 0、正文 5196 字。
      守门测试：`TestBuildAgentBodyReasoningFields`（含「未表达时 enable_thinking 必须为 false」）。
    - **`internal/qoder`（QoderWork 渠道）已同样实测并修复**（2026-09-22；与 QoderCN 同一上游目录、
      同 14 个模型、同 `qfmodel` = qwen3.8-flash ladder `[low medium xhigh]`）：只发 `is_reasoning=false`
      而缺 `enable_thinking` 时 **1793 个 reasoning 块 / 608KB、180s 超时截断、content 0 块**；
      同模型同 prompt 补上 `enable_thinking=false`（官方关闭形态）后 48.2s 收尾、reasoning 块 0、
      正文 4358 字。三处投影现均恒写 `enable_thinking`。
      复现/验证：`WILDWORK_PROBE_CASES=1,12 go test -tags live ./internal/qoder/ -run TestLiveProbeEffort -v`
      （用例 1 = 缺陷形态，用例 12 = 官方关闭形态；修复后用例 1 应正常收尾、reasoning 块 0）。

24. **千问办公推理 body 必须带 `business.product`**（`internal/qwenwork/constants.go::BusinessProduct`）：
    上游按它选「模型目录」，缺省时推理端点恒回 HTTP 200 + envelope
    `{"code":"503","message":"Model catalog unavailable"}`，且**与请求头集合、与 `Encode=1`/body 编码、
    与 body 其余字段（model_config / system / tools / parameters / chat_context / session_type）全部无关**。
    实测矩阵与取证方法见 `docs/千问办公QwenWork逆向对比备忘.md` §10。
    改本渠道请求形状前后必须跑 `TestLiveProbeReasoning`（`-tags live`，走 `ChatStream` 全链路）。
    **本渠道刻意不投影任何思考字段**：千问办公官方客户端本身没有思考控制设置（无档位/开关 UI），
    抓包确认其请求体也不带 `reasoning_effort` / `enable_thinking` —— 这是符合官方行为、**不是缺口**，
    不要为它补档位投影（用户 2026-09-22 确认）。

25. **导入型渠道（`raccoon` / `loomy`）不做登录编排**：凭据由面板「从本机客户端导入」产生
    （`internal/app/import_local.go`，写 `auths/<渠道>-<uid>.json`，路径**自适应探测**多候选目录）。
    由此产生三条硬约束：
    - **未知模型必须本地拒绝**：两个上游对未知模型名都会**静默回落到默认模型并返回 200**（阶段 C 实测），
      渠道层不校验就会让用户以为在用 A 模型、实际消耗 B 模型的额度。守门测试见各渠道包的
      `TestChatStreamRejectsUnknownModel`。
    - **聚合必须同时支持 JSON 与 SSE**：上游对非流式请求可能直接返回 JSON（Loomy 实测），
      只按 SSE 解析会得到「content 空 + created 用 time.Now() 兜底」的假响应。
    - **小浣熊的 refresh_token 是单会话的**：与官方客户端同时使用会互相抢刷新并报
      `refresh_conflict`（实测 400）；文档要求导入后退出客户端。
26. **`loomy` 的档位面独占 `RealmLoomy`**（`RealmForKind` / `SupportsEffortKind` 已同时登记）：
    档位来自上游 `/models` 的 `reasoning_efforts`（无静态兜底）；投影时补 `enable_thinking` 与
    `chat_template_kwargs.enable_thinking` 三件套。实测该系列模型**无法完全关闭思考**
    （最低档仍产生约 59 字），故 `ReasoningCanDisable` 保持 false。
27. **上游错误分类里「请求级错误」不得罚号**（`internal/upstream/client.go` + `internal/server/handler.go`）：
    内容拦截 / 上下文超限（11115）/ 图片格式无效（11135）/ 出站 body 畸形（11101）都是**请求内容**的问题 ——
    同一 body 换任何账号结果都一样。这几类必须在 `Classify` 里判成 `ErrContentBlocked` / `ErrPromptTooLong` /
    `ErrImageInvalid` / `ErrBadParams`，由 `handler.chatCompletions` 走「不冷却、不计数、原文透传」分支；
    一旦落到 `ErrClient`，`default:` 分支的 `NoteError` 就会把健康账号喂到冷却。
    - **业务码一律走 `provider.CodeMarker`，不用字面量 marker**：上游信封形态不统一
      （`"code":11135` / `{"code": 11135}` / `"code":"11135"`），字面量只覆盖紧凑形态，漏判即退化成 `ErrClient`。
      `codeMarker` 同时排除 `"code":111350` 这类前缀误命中。
    - **这些判定必须排在 `status == 404` / `status >= 500` 之前**：404 上的 11115 若落到 `ErrNotFound` 会软冷却账号
      （上下文超限与账号健康无关）。
    - **图片解析失败的信封 code 也是 11101**（`Parse message failed: invalid image_url content ...`）：
      图片判定必须先于 11101 判定，否则「图片有问题」被误归「body 畸形」。
    - 出站 `image_url` 形状在 `payload.normalizeImageURL` 归一（上游只认 OpenAI 对象形态，
      字符串形态会 400 code=11101）；只转形状，空串/缺失/类型不对一律不动，让上游报真实错误。
    守门测试：`TestUpstreamRequestLevelErrorsDoNotPunishAccount`（含「未知 4xx 仍罚号」对照，防止断言空转）、
    `TestCodeMarkerTolerance`、`TestNormalizeImageURL`。
    - **业务码判定全渠道共用 `provider.CodeMarker`**（`internal/provider/codemarker.go`，upstream 包内别名
      `codeMarker`）：upstream / qoder / qodercn / qodercom / workbuddyai 的 Classify 一律走它，禁止裸
      `Contains(lower,"11115")`——request_id 等任意含这五个数字的文本会误命中，把该罚号的错误透传出去。
      超限判定在各渠道 Classify 里同样必须排在 404 兜底之前（qoder 系 4 渠道守门：
      `TestClassifyPromptTooLong11115`）。qwenwork / traework 不认 11115 码（其上游无该信封，
      只认文案形态），维持现状。
28. **token 刷新必须按账号单飞**（`internal/provider/refresh.go`）：渠道的 `RefreshToken` 只在写字段时持
    `auth.mu`，HTTP 调用在锁外 —— 该锁只防数据竞争，**拦不住「两次刷新都真的打上游」**。单会话
    refresh_token 被并发使用必然一方报 `refresh_conflict`（raccoon `200822` / qwenwork `invalid_grant`），
    失败方还会被 `Pool.Cooldown` 推进冷却，对外表现为「一并发就 503」。所有刷新调用点
    （app.go 3 处 / scheduler.go 2 处 / handler.go 1 处）一律走 `provider.RefreshOnce(a, fn)`，
    **禁止直接调 `Upstream.RefreshToken`**（`internal/login_trae` 的登录内联刷新除外）。
    - fn 内固定做「重检 `NeedsRefresh` → 刷新 → `SaveAtomic`」：等待者醒来重检发现已被刷过就直接返回，
      同时封住「上一轮 flight 刚删除」的窗口。唯一例外是 401 自愈（session 已作废但 expiresAt 未到），
      那类 fn 无条件刷新。
    - 单飞键取 **auth 文件路径**（回落 UID）：跨渠道天然隔离，且「同账号在两个目录各有一份文件」
      本就代表两个独立会话，应各自单飞。
    - 实现在 map + channel 上，**不引入 `golang.org/x/sync`**。
    守门测试：`internal/provider/refresh_test.go`（含 panic 唤醒等待者、无粘性缓存两例）。
29. **匿名渠道的虚拟账号不得进入任何「能把它弄没」的路径**：`reloadAccounts` 不纳入（`SyncToDir` 会剔除），
    启动时 `SetDisabled(uid,false)` 兜底自愈；`RemoveAccount`/`DisableAccount` 对 `provider.Oczen` 硬拒（后端拒 + 前端无入口）。
    其 `Auth.ExpiresAt` 必须为远期值（不得为 0），否则 `NeedsRefresh` 恒真 → 反复 `RefreshToken` + 冷却。
30. **无账号渠道的错误分类只能依赖 429**：单账号且不可重登 ⇒ 任何 4xx 都不应惩罚账号（否则整渠道下线），
    故 `oczen.Classify` 对其它 4xx 返回 `ErrPassthrough`（server 侧与 `ErrContentBlocked` 同分支：原文透传、不计错不冷却）。
    **严禁**把 401/403 归为 `ErrSessionDead`（会 `pool.Disable` 永久禁用且无法人工恢复）。
31. **面板上「客户端要填的渠道前缀」只有一处出口（`web/app.js` 的 `chPrefix()` / `chPrefixChip()`）**：
    客户端模型名必须带渠道前缀（`provider.Kind` 即前缀，如小浣熊填 `raccoon/<模型名>`，无前缀且无映射时网关报
    `model "xxx" needs an explicit channel prefix`），面板在**账号卡片**（badge 旁）、**费率表分组表头**、
    **帮助弹层前缀清单**三处展示，全部走同一函数；费率表的模型 tooltip 首行给完整调用 ID（可直接照抄）。
    帮助清单与路由报错文案（`server.prefixHint`）**同源**：都取 `cfg.Runtimes` 的 key（前端即 `state.compat.channels`），
    故渠道路由清单增删会自动跟随。
    - 渠道**显示名**同样只有 `CH_LABEL` 一处数据源，但取用有两档语义，别混：
      `chLabel(k)`（固定入口：账号卡片 / 费率表 / 代理行，未收录时兜底 `"WorkBuddy"`，保证有名字可看）
      与 `chNameOf(k)`（**数据驱动**入口：用量面板 / 流水 / 图表，未收录时回退原 id —— 这些值来自历史流水，
      可能是新增或已删渠道，回退 id 才有诊断价值）。原有的 `CH_NAMES`（用量面板）与
      `PROXY_HINT`（代理行）是另外两份副本，新增渠道只改一处必然漏 ——
      `raccoon`/`loomy`/`monkeycode`/`traecode` 曾在用量面板显示成英文 id（2026-09-24 已删除两份副本）。
    - **新增渠道只需在 `CH_LABEL` / `CH_CLASS` 各加一行**；前缀、帮助清单、tooltip 均自动生成，不要再手写清单
      （帮助文案曾手写 4 个渠道且含已下线的 `qoder`）。
32. **流式请求不得用 `http.Client.Timeout`；流内故障必须补 error 帧 + `[DONE]`**（2026-09-24 修复 loomy「突然无响应」）：
    - **`http.Client.Timeout` 是整请求上限**（计时器在 `Do()` 返回后继续跑，直到 body 读完），
      而 SSE 整个生成期都在读 body ⇒ 「慢但一直在出字」的请求会被**从流中间掐断**。
      实测 loomy 两次中断都恰好 `120.00s`（= `config.upstream.timeout_seconds`），
      客户端表现为「突然无响应」，且 `usage` 末帧从未到达（ledger 里该请求记成 `src=none / pt=0`）。
      故流式必须走**无总超时**的独立 client（`StreamHTTP`，照 `traework` 既有范式），
      由 Transport 的 `ResponseHeaderTimeout`（连头都等不到时兜底）+
      `provider.IdleReader`（`ErrIdleTimeout`，默认 90s，可配 `upstream.stream_idle_seconds`，
      下限 10s）两级承担。非流式调用（目录/积分/刷新）**保留**总超时 —— 短请求的总超时是恰当的。
    - ⚠️ **`StreamHTTP` 无总超时后必须有 `ResponseHeaderTimeout`**：两者都去掉就是无限挂住。
      顺带补齐了 loomy/raccoon 此前缺失的 Transport（原先未设 Transport，实际共用
      `http.DefaultTransport`：h2 开启、无 `ResponseHeaderTimeout`）。
    - **流中断（空闲超时 / 连接被切断）必须补一帧 error + `data: [DONE]`**：响应头早已按 200 发出，
      流内帧是唯一能表达故障的通道。只 `return err` = 客户端收到无收尾的截断流（只能一直等）；
      只补 `[DONE]` 则把故障伪装成正常结束。统一走 `provider.WriteTruncationFrames`，
      错误码由 `TruncationErrorCode` 归一（空闲超时 `upstream_timeout` / 其它 `upstream_stream_error`）。
      覆盖三处：`internal/loomy/sse.go`、`internal/raccoon/sse.go`、`internal/upstream/sse.go`
      （后者为 WorkBuddy 系 workbuddy/workbuddyai/traework/traecode 共用，同款缺陷一并修）。
      守门测试：`TestStreamTruncationEmitsFrames`（三包各一）+ `TestStreamReadErrorEmitsTruncationFrames`。
    - **代理渠道清单有三处，必须同步**：`config.go` 的 `Proxies` 注释、`main.go` 的 `proxyTargets()`、
      `web/app.js` 的 `PROXY_CHANNELS`。`App.SetProxies` 是**整份替换** `cfg.Proxies`，
      面板漏登记的渠道在保存时会被**静默清空**（手改 config.json 的代理会丢）——
      `raccoon`/`loomy`/`monkeycode`/`traecode` 曾同时漏在热更新名单与面板清单里（2026-09-24 已补）。
      `main.go` 的启动与热更新现已共用同一个 `proxyTargets()`，不再有两份名单。

## 7. 平台能力差异表（internal/platform）

| 能力 | Windows | macOS | Linux |
|------|---------|-------|-------|
| 打开浏览器 | rundll32 url.dll | `open <url>` | xdg-open |
| 系统消息框 | MessageBoxW | osascript display dialog | stderr |
| 开机自启 | 注册表 Run | LaunchAgent plist | 未实现 |
| 打开日志文件 | notepad | open -a TextEdit | xdg-open |
| 确认框 | MessageBoxW YESNO | osascript buttons | 默认否 |
| 无头模式 | --no-tray | --no-tray | --no-tray（推荐） |

## 8. 构建

> ⚠️ **本地构建是日常约束**：任何代码变更后都要重新构建 `dist/wild-work.exe`
> （见 §6 第 0 条）。CI 不生成该文件，且 `dist/` 不入版本控制。
> 标准流程：`go build ./... && go vet ./... && go test ./...` 全绿后再构建。

```bash
# Windows（本机直接构建，或 WSL 交叉编译）
# exe 文件图标来自 cmd/wild-work/rsrc_windows_amd64.syso（已入库，go build 自动链接）。
# 换 build/windows/icon.ico 后必须重新生成并提交，否则本地 exe 还是旧图标：
#   go run github.com/akavel/rsrc@latest -ico build/windows/icon.ico -o cmd/wild-work/rsrc_windows_amd64.syso
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H windowsgui" -o dist/wild-work.exe ./cmd/wild-work

# macOS（需 macOS 真机或 CI，cgo 必需）
GOOS=darwin GOARCH=arm64 go build -o dist/wild-work-darwin ./cmd/wild-work

# Linux 无头
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/wild-work-linux ./cmd/wild-work
```

构建后核对产物是不是刚编的（防止拿到旧文件）：

```bash
# 首选：看嵌入的 VCS 信息（go 1.18+ 默认写入），比字节计数可靠
go version -m dist/wild-work.exe | grep -E "vcs.revision|vcs.modified"
# vcs.revision 应等于 git rev-parse HEAD；带 vcs.modified=true 说明构建时有未提交改动

# 次选：版本号字节计数。旧版本号可能巧合出现在编译器生成的闭包符号名里
# （如 crypto/tls 的 `.1.2.2.1`），所以「旧版本号出现 0 次」不是必须条件，
# 只有「新版本号完全没出现」才说明构建没生效
python -c "b=open('dist/wild-work.exe','rb').read(); print('new:',b.count(b'2.3.1'))"
```

### 发版（tag 触发）

push `v*` tag → GitHub Actions 构建五平台产物并创建正式 release。
**release note 用仓库根目录的 `RELEASE-<tag>.md`（手写摘要，面向用户）**，
而不是 `--generate-notes`（那只给 commit 链接列表）；文件缺失时回退自动生成，不阻塞发版。

```bash
# 发版前确认：版本常量已 bump（internal/app/app.go const Version）、
# RELEASE-vX.Y.Z.md 已写好且与 tag 名一致、dist/wild-work.exe 已本地重建验证
git tag vX.Y.Z && git push origin vX.Y.Z
```

## 9. 文档索引

- [README.md](README.md) — 用户文档
- [DEVELOPMENT.md](DEVELOPMENT.md) — 开发者文档（面向 AI Agent）
- [HANDOFF.md](HANDOFF.md) — 交接文档（历史记录）
- [docs/三接口兼容改造备忘.md](docs/三接口兼容改造备忘.md) — 三接口（Chat/Responses/Anthropic）兼容层架构决策、实施记录、验证清单、已知限制
- [docs/loomy-raccoon渠道接入计划.md](docs/loomy-raccoon渠道接入计划.md) — 两渠道接入计划（A–F 阶段、决策记录、风险矩阵）
- [docs/loomy渠道接入备忘.md](docs/loomy渠道接入备忘.md) — Loomy 协议取证（端点/签名算法/登录 API/积分端点 + A3 实测）
- [docs/raccoon渠道接入备忘.md](docs/raccoon渠道接入备忘.md) — 小浣熊协议取证（端点/凭据文件/refresh 链路 + A3 实测）
- [docs/loomy-raccoon阶段C探针实测.md](docs/loomy-raccoon阶段C探针实测.md) — 流式/错误形态/关思考矩阵实测记录
- [docs/loomy-raccoon阶段D实施记录.md](docs/loomy-raccoon阶段D实施记录.md) — 两渠道落地记录（代码触点、端到端实测、修掉的 3 个问题）
- [docs/loomy-raccoon阶段E验收报告.md](docs/loomy-raccoon阶段E验收报告.md) — 六项验收结果 + 并发刷新竞态的 A/B 对照验证
- [docs/用量积分流水记账备忘.md](docs/用量积分流水记账备忘.md) — 双流水统计（token/积分）架构、差分算法、实测验证、已知限制（R23）

> **docs/ 采用白名单制**：`.gitignore` 中 `docs/*` 默认忽略全部文档，仅 `!docs/<文件名>` 显式反选的才入库。
> 逆向分析类文档一律**只保留本地、不入库**。新增需要入库的文档时，追加一行 `!docs/<文件名>`。
