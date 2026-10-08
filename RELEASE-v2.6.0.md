# wild-work v2.6.0 — 三个新渠道（MonkeyCode / 商汤小浣熊 / 讯飞 Loomy）+ 用量积分四处修复

> 发布主题：**社区贡献的三个新渠道一次性入库**（MonkeyCode、商汤小浣熊、讯飞 Loomy），渠道数升至**十二**；
> 同时修复**四个用量/积分统计问题**（月初看不到当月数据、签到收入不入账、Trae 系 token 流水恒空、流水排序错乱）。
> 上次 tag v2.5.5 之后累积 16 个提交。

---

## 🆕 新增渠道（社区贡献，来自 xxhhlk）

### MonkeyCode（`monkeycode/*`）— PR #65

平台托管的模型聚合服务（后端接 11 家提供商），一个渠道覆盖一批模型。

- **按模型声明的 `type` 分流**：Anthropic 形状走 `{base}/messages`，Responses 形状走 `{base}/responses`，两路都是「每轮带完整上下文」的无状态 SSE（无 VM、无任务会话、无会话 id 复用）。
- **导入型凭据**：面板「＋ MonkeyCode」读取本机官方客户端（ohmyagent）的 `settings.json`，一个账号 = `oma_` api_key + `omas_` signing_secret，另有控制台会话 Cookie（拿不到不影响对话）。
- 上游无 `GET /models`（返回 405）→ **模型表为静态清单**（25 个模型，basic/pro/ultra 三档）；无额度/刷新接口 → 额度恒 0、`RefreshToken` 为空实现。
- 未知模型本地 400（不请求上游，不罚号）。

### 商汤小浣熊（`raccoon/*`）— PR #64

- **两条添加路径**：① 面板主按钮走**浏览器授权登录**（登录期间临时把 `office-raccoon://` 协议回调指向本工具以接住授权码，结束立即恢复注册表；启动时另有残留自愈）；② 弹窗次按钮「从客户端导入」读本机 `.box-agent/config/auth.json`。两条路拿到同一账号，落盘形态一致。
- 凭据 `access_token` ≈2h + `refresh_token` ≈30d，保活每 4 小时一次以减少「请求先 401 再刷新」的往返；无签到端点；上游默认档即最深思考，故不接档位面。

### 讯飞 Loomy（`loomy/*`）— PR #63

- **导入型**：面板「＋ Loomy」读本机 `C:\Users\Public\Loomy\<hash>\userData\auth-session.json`（枚举 hash 子目录，跨账户跨版本都能命中）。
- **上游无 refresh 端点**，session ≈14 天，到期需重新登录并再次导入；故 `RefreshToken` 留空、定时任务全关（保活会被按「无 refresh token」跳过，但每天仍空跑一次并记失败）。
- **鉴权失败是 HTTP 200 + 业务码 `100002`**（不是 401）——业务码判定必须排在状态码判定之前，否则失败会被当成成功。

### 三条 PR 带来的共享增量

| 文件 | 增量 |
| --- | --- |
| `internal/provider/provider.go` | `ErrBadParams` / `ErrImageInvalid` 错误类（请求级，不罚号不轮转）；`ResourceItem.InfoOnly`（只展示、不计入小计） |
| `internal/provider/stream.go` | 空闲看门狗 `IdleReader` + 截断补帧（无总超时 client 的空闲兜底） |
| `internal/provider/codemarker.go` | `CodeMarker`：业务码判定收在一处 |
| `internal/auth/auth.go` | `Auth` 增 `SigningSecret` / `ConsoleCookie` / `BaizhiCookie` 字段；`LoadMonkeyCodeDir` / `LoadRaccoonDir` / `LoadLoomyDir` |
| `internal/app/app.go` | `POST /api/account/import_local`（导入型渠道统一入口） |

> 生产验证：三渠道均在**真实账号 + 真实上游**上跑过端到端（隔离实例，凭据用后即删）；合计 71 个测试函数，全部对假上游、不依赖网络。

---

## 🐛 本版修复

### 1. 面板「近 7 天」每月 1~6 号看不到当月数据（issue #59）— PR #58 / #66

**现象**：月初打开面板，Token 折线图停在月底，当月模型全部消失；积分 7 天视图全空。用户实测同一份数据少了约 2/3（194M → 549M token）。

**根因（两处叠加）**：

1. **月份枚举跳月**：`Query` 从 `from` 起按 **+15 天** 步进枚举月份分段。7 天窗口跨月的常态下（`from` 落在上月 25 号之后），步进一步就越过当月 ⇒ 当月分段整月不被扫描。每月 1~6 号必现，跨年同理。
2. **`entries` 未排序**：分段枚举用 `map`，`range` 顺序随机；跨月时会把当月条目排到上月之前，前端按「升序数组 + 倒序分页」渲染时表现为**最新记录排到后面几页**。

**修法**：① 改为按「`from` 所在月 → `now` 所在月」**逐月步进**；② `Query` 返回前显式 `sort.SliceStable` 按时间升序。

**回归测试**：`TestQueryCrossMonthSegments` / `TestQueryMonthEnum`（含跨年）/ `TestQueryEntriesTimeSorted`。

> 附带说明：仓库里 `TestQueryAggregation` / `TestDiffCreditsDuplicateKeys` 两个测试**一直只在月初变红**（往当月文件写数据、被测代码只读上月），正是这个 bug 的探针。修复后稳定通过。

### 2. 签到/周期发放的收入从不进账本（账本只降不升）— PR #61

**现象**：9 月账本 26 个账号整月 earn **只有 baseline 一条**——每日签到（+100/天）与月周期发放全部漏记，面板收入恒为 0。

**根因**：`DiffCredits` 对「快照中不存在的新 key」直接 `continue`，把上游每次发放都静默吞掉。

**修法**：非首次出现的新 key 且 `remain>0` 时记一条 earn；首次账号仍只记一条「存量额度」baseline，同 key 重复条目先聚合求和再差分（issue #38）的口径不变。

**实测**：修复后 26 账号每日签到 26/26 全入账（10-03 +2600、10-04 +2600），逐条累计与上游 remain 分毫不差。

### 3. Trae 系（traework/traecode）token 流水全丢 — PR #58

**现象**：模型在用、客户端自己显示的 token 数正常，但**面板 token 恒 0**（`src=none`）。

**根因**：SOLO 协议事件序 `output → token_usage → done`。`done` 分支旧序**先 `writeChunk` 再取 `pendingUsage`**——而 `writeChunk` 会把 `pendingUsage` 附加进末 chunk 后置 `nil`，于是 `usage = pendingUsage` 永远拿到 nil。客户端不受影响（usage 已随末 chunk 透传），只有网关自己记账的 `Stream()` 返回值恒空——这正是它难被发现的原因。

**修法**：`done` 分支先 `usage = pendingUsage` 再 `writeChunk`（两行换序）。

### 4. Trae 流内业务错误伪装成正常收尾 + 账号不冷却 — PR #61

**现象**：上游 SOLO 在流中途回 `event:error`（3004 限流 / 1005 权益不足）时，客户端看到「正常结束、内容莫名其妙」，拿着半截回答继续跑；账号不冷却，重试必然再撞同一号。

**根因**：错误分支写成 `delta.content` + `finish_reason:stop` + **补 `[DONE]`**，且函数返回 `nil`；更糟的是 `NoteSuccess`/`stickySuccess` 在读流**之前**已执行，粘性路由把请求钉死在中招账号上。

**修法三件套**：

1. `solosse` 错误分支改发 OpenAI 规范 error 帧（限流标记 `upstream_rate_limited`）、**不补 `[DONE]`**、把错误返回调用方；
2. 新增 `provider.StreamErrorClassifier`，handler 按 kind 冷却账号（软 60s / 硬 12h / 禁用），server 层不 import 具体渠道包；
3. 粘性路由见 `status.Cooling` 自动让位，无需显式 `stickyClear`。

**兼容层必须识别 error 帧**：`gateway.parseChatSSELine` 此前只认带 `choices` 的分片，error 帧被静默丢弃，致 `/v1/messages` 发 `end_turn`+`message_stop`、`/v1/responses` 发 `response.completed`+`[DONE]`，把失败伪装成成功（该缺口对内层所有渠道通用）。**协议合规**：Anthropic `error.type` 是 9 元判别联合，限流映射 `rate_limit_error`；Responses 用 `response.failed`，私有码只放自由 `code` 字段。

**生产实测**：09:13 真实 3004 触发完整链路——粘在 A 号 → 撞 3004 → `stream error cooled kind=soft_rate cooldown=1m0s` → **1 秒后**换到 B 号 → B 也撞 → 冷却 → C……当日 11 次冷却全部正确触发换号，60s 后自动恢复。

### 5. 四个「无定时任务」渠道不再每天空跑 — PR #56

**现象**：账号卡片每天固定出现一次红色「签到失败」标签（消息「qoder 暂无签到活动」/「no refresh token」）；千问办公更严重——注释写着「定时保活会与 App 互踩」，但每天 22:00 的保活批次照常跑。

**根因**：`scheduler.New` 用 `len(...) == 0` 判默认值，把 `nil`（未配置 → 落默认）与 `[]int{}`（本渠道没有这类任务）混为一谈，调用方「显式不要」的意图被兜底成默认时间。

**修法**：两处判据改 `== nil`；qoder / workbuddyai / qwenwork / oczen 四处调用点改传显式空切片；`Run()` 补「两个列表都为空时阻塞等 ctx/配置变更」（否则 `nextFireMinutes` 返回零值会变成每天 1440 次空转）。

---

## 🎨 面板

- 账号卡片新增**冷却状态**显示（「冷却至 MM-DD HH:MM」琥珀标签 + hover 原因）——此前后端早已透出 `cooling/until/reason` 但前端从未渲染，表现为「积分明明很多却报 503/限流」而卡片毫无提示。
- 新增**渠道积分汇总条**（每渠道可用/临期/合计，与账号卡片同源）。
- 积分流水与模型榜的渠道列改为彩色徽标；积分折线图系列名带渠道前缀（跨渠道同名昵称可区分）。

---

## ⚠️ 已知边界

- 三个新渠道均为**单作者验证**，无第三方用户验证；亦**不覆盖多账号并发**下的路由与冷却。MonkeyCode 的 `pro/*` / `ultra/*` 整档未复测（账号无权限，实测 403）。
- 依赖上游返回结构的字段（模型目录、费率、档位表）上游一改就得跟着调；MonkeyCode 的模型表是**静态清单**（上游 `GET /models` 返回 405）。
- 小浣熊的浏览器授权登录需临时改写 HKCU 的 `office-raccoon` 协议注册（登录结束即恢复，启动有自愈）；非 Windows 平台该路径为桩实现，只能用「从客户端导入」。
- TraeWork 3004 的限流维度**按账号级处置，但证据不足**：观察仅 1 例且来自 checkin 路径，只足以排除 IP/全局级。因软冷却仅 60s，判据不成立时最坏影响是闲置一分钟，故维持现处置。
- 本版不动 `state-*.json` 格式，无需迁移。

## 📎 升级提示

- 新增三渠道后面板渠道按钮为**两行五列**：
  行1 = WorkBuddyCN / QoderCN / TraeWork / MonkeyCode / Loomy；
  行2 = WorkBuddyAI / QoderCOM / 千问办公 / 小浣熊 / 智谱清言。
  导入型渠道点按钮即为「导入」动作（读本机已登录客户端凭据，不需要浏览器登录）。
- 若你的 `auths/` 下已有同名前缀文件不受影响；新增渠道各自使用独立 `state-*.json`。
- 账本修复生效后，**新产生**的签到收入会正常入账；早先漏记的历史收入不会追溯补录（上游已不提供可回算的快照）。

---

## 致谢

本版三个新渠道及多数量化修复来自社区贡献：

- **xxhhlk** — MonkeyCode / 商汤小浣熊 / 讯飞 Loomy 三个渠道（#65 / #64 / #63）、月初数据丢失的月份枚举修复（#58 的问题一）、`entries` 排序修复（#66）、scheduler nil-vs-empty 修复（#56）
- **jianzhangg** — 月份枚举与 Trae 系 token 流水修复（#58）
- **ttales430** — 签到收入入账与 Trae 流内错误处理修复（#61）

相关协议逆向成果已由作者开源独立维护：[`xxhhlk/raccoon2api`](https://github.com/xxhhlk/raccoon2api)、[`xxhhlk/loomy2api`](https://github.com/xxhhlk/loomy2api)、[`codkeep/MonkeyCodeReverseEngineer`](https://github.com/codkeep/MonkeyCodeReverseEngineer)。
