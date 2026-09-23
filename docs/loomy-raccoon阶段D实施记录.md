# Loomy / 小浣熊 阶段 D 实施记录

> 状态：**阶段 D 完成**（2026-09-22 23:20）｜`go build ./... && go vet ./... && go test ./...` 全绿
> `dist/wild-work.exe` 已本地重建（R6.0）。
> 前置：阶段 A 静态取证 → 阶段 B 门禁（两渠道 Go）→ 阶段 C 探针实测。

---

## 1. 交付物

### 1.1 新增代码
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

### 1.2 改动的既有文件（10 个）
`internal/provider/provider.go`（Kind）｜`internal/auth/auth.go`（`LoadRaccoonDir` / `LoadLoomyDir` / `loadPrefixed`）｜
`internal/reasoning/catalog.go`（`RealmLoomy` + `RealmForKind` + `SupportsEffortKind` + `normalizeRealm` + `staticCap` **五处同改**）｜
`cmd/wild-work/main.go`（装配 11 处）｜`internal/app/app.go`（渠道列表 ×3 + `noExplicitCheckin` + 登录提示 + 新路由）｜
`cmd/wild-work/web/{app.js,index.html,style.css}`（渠道表 ×3 + 导入型弹窗分支 + 两个按钮 + 配色）｜
`README.md`、`AGENTS.md`（渠道表、§6 新增第 25/26 条不变量、文档索引）

---

## 2. 端到端实测（真实账号，23:12）

```
GET  /v1/models            → 200，74 个模型（raccoon=8, loomy=8 已并列其中）
POST /v1/chat/completions  loomy/spark-x           → 200 model=loomy/spark-x   finish=stop content='ok' reasoning_len=451 usage✓
POST /v1/chat/completions  raccoon/raccoon-8c4485  → 200 model=raccoon/raccoon-8c4485 finish=stop content='ok' usage✓
POST /v1/chat/completions  raccoon（stream=true）   → SSE 真增量，model 重写为 raccoon/raccoon-8c4485
POST /v1/chat/completions  raccoon/nope-xyz        → 400 model_not_found（本地拒绝，未打到上游）
```

启动日志：`loaded accounts: ..., raccoon=1, loomy=1 from ./auths` ✓
积分：`credit auto-refresh platform=loomy uid=… remain=15000 expiring=0 unusable=0` ✓

---

## 3. 实施中发现并修复的 3 个问题

### 3.1 Loomy 积分端点前缀写错（404）
静态取证时把 `points-service.js` 里的 `this.get('/api/v2/points/records')` 误当成相对 `APIPrefix(/api/v1)`，
实际客户端传的是**含 `/api/` 的完整路径**。修正为
`/api/v1/points/records`、`/api/v2/points/records`、`/api/v1/team-points/balance`（站点根 + 完整路径）。
修正后实测 `remain=15000`。

> **后续修正（2026-09-23）**：上面那个 `remain=15000` 本身就是**漏了每日积分**的结果 ——
> 上游 `data` 里还有 `dailyBalance`（每日池）与 `availableBalance`（= 常规池 + 每日池）。
> 现已改用 **v1 面**并取 `availableBalance`（实测 **19800**），拆「积分 / 每日积分」两条明细。
> 详见 `docs/loomy渠道接入备忘.md` §6 的「余额口径」。

### 3.2 非流式聚合丢内容（Loomy）
首版 `aggregate` 只按 SSE 解析，而**上游对非流式请求直接返回 JSON**（普通 `chat.completion`），
于是产出「`content: ""` + `created` 用 `time.Now()` 兜底」的假响应 —— **静默丢全部内容**。
改为**先探测 JSON、再回落 SSE**（`aggregate` / `aggregateSSE` 双路径），两渠道同改。

### 3.3 小浣熊 `refresh_conflict`（上游约束，非代码缺陷）
wild-work 启动时按 `NeedsRefresh(10min)` 主动刷新，而客户端也在刷新同一 refresh_token →
上游返回 `400 {"code":200822,"message":"refresh_conflict","details":"refresh token conflict or reused"}`，
随后账号被标 `session_dead` 禁用，请求回 503。
**结论：小浣熊的 refresh_token 是单会话的**（用后即轮换，两主体不能共用）。
处理：
- 为降低抢刷新频率，最初曾把 `raccoon` 移出积分自动刷新循环；**找到余额端点后已加回**（余额要刷）；
  token 另由 4 小时保活维护；
- 文档明确要求：**导入后退出小浣熊客户端**。

---

## 4. 使用约束（需在文档/面板上让用户看得见）

1. **导入后请退出对应的官方客户端**（尤其小浣熊：refresh_token 单会话，两边抢刷新会互相踢）；
2. **Loomy 无续期**：session 约 14 天，到期需在客户端重新登录并再次点「导入」；
3. **模型名写错会本地 400**（`model_not_found`）—— 这是刻意设计，因为两个上游都会静默回落默认模型；
4. 小浣熊的**余额与费率都能显示**：余额取 `GET /api/web/points/v1/balance`（四个池子按 `Usable` 拆分，
   `topup_frozen` 时充值池标为不可用），费率取 `billing_multiplier`。

---

## 5. 未验证项（留待阶段 E）

1. `/v1/responses` 与 `/v1/messages`（三接口兼容层）在两渠道上的表现 —— 本次只测了 `/v1/chat/completions`；
2. 工具调用闭环（两个渠道的 `tool_calls` 流式形状已实现但未端到端验证；Loomy 上游声明 `function_calling: true`）；
3. 小浣熊 refresh 的**真实轮换**验证（本次只在"被抢刷新"场景下观察到冲突，未验证 wild-work 独占时的刷新成功路径）；
4. 两渠道的 429/限流形态（未触发过）；
5. Web UI 手工验收（导入按钮、文案、配色）；
6. 多账号并发与会话隔离。

---

## 6. 附：本次未纳入的事

- `internal/traework/live_probe_test.go` 是**并行会话的 WIP**（未跟踪状态），本次未触碰、未 add；
- 阶段 C 的探针工具与 asar 工具保留在 `_probe/`、`_raccoon_probe/`（工作区，非仓库内容）。

---

## 7. 阶段 E 前置修复：定时任务被静默补默认（2026-09-23）

阶段 E 排查「渠道 token 是否需要定期刷新」时发现 `scheduler.New` 的零值处理有缺陷：
`len(cfg.KeepaliveHours) == 0` 把 **nil 与显式空切片一视同仁**，都补成默认 `[22]`
（签到同理补 `9:00/21:00`）。结果是四个「注释声明已关闭」的渠道实际仍在跑定时任务：

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

---

## ⚠️ 后续更正（2026-09-24，`baaff96`）

- `internal/loomy/client.go` 的「三件套档位投影」当时**漏了按模型 ladder 降级**（`reasoning.Caps.Clamp`
  未调用，与 qoder 三渠道不一致）→ 已补；并补了档位日志（`loomy reasoning: model=... in=... out=...`）。
- `internal/raccoon/client.go` 当时未处理档位字段 → 已加 `forceUpstreamDeepThinking`
  （**剥离** `reasoning_effort`，因实测上游默认档最深）+ 剥离日志。
- 两渠道现均按此语义工作，详见各渠道备忘的档位章节。
