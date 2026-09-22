# Loomy / 商汤小浣熊 —— 阶段 E 验收报告

> 日期：2026-09-23 ｜ 被测二进制：`dist/wild-work.exe`（含 `2a273cb` 定时任务修复）
> 账号：traework / qoder / qodercn / qwenwork / raccoon / loomy 各 1（workbuddy、qodercom 无凭据）

---

## 1. 结论

**验收通过**：三接口、工具调用闭环、错误分类、并发与会话隔离、积分/费率口径、Web UI 六项全部达成。
过程中发现 **1 个并发缺陷（未修）** 与 **3 个需知晓的事实**，详见 §4。

---

## 2. 结果总表

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

---

## 3. 逐项明细

### E1 三接口

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

### E2 工具调用闭环

**raccoon 逐模型**（`tool_choice=required`，单线程顺序测）：

| 模型 | 结果 |
|---|---|
| raccoon-8c4485 / raccoon-19b265 / sn-sensenova-6-8-flash-lite / sn-glm-5-3 / sn-glm-5-3-flash / sn-deepseek-v4-1-flash | ✅ `finish=tool_calls` + 结构化 `tool_calls` |
| **sn-kimi-k3** | ❌ `finish=stop`，输出**文本** `content='get_weather({"city": "Beijing"})'` |
| raccoon-405a1c | ⚠️ 请求时 503（瞬时不可用），**复测 200 正常**，工具能力未验证 |

**loomy 逐模型**（`tool_choice=required`）：

| 模型 | 结果 |
|---|---|
| deepseek-v4-flash-0731 / MiniMax-M3 / Kimi-k2.6 / qwen-3.8-max / GLM-5.3-Flash / qwen3.8-flash / mimo-v2.5 | ✅ `finish=tool_calls` |
| **spark-x** | ❌ `finish=stop`，把调用写成**文本** `content='get_weather("Beijing")'` |

> 7/8 通过 → 链路无缺陷；`spark-x` 是模型自身行为（上游仍声明 `function_calling: true`）。

### E3 错误形态

| 场景 | 结果 |
|---|---|
| `raccoon/nope-xyz`、`loomy/nope-xyz` | **400 `model_not_found`**，本地拒绝（未打上游）✅ |
| 空 `messages` | 400，透传上游 `litellm.BadRequestError` |
| `nosuch/x`（未知渠道） | 400 `invalid_model`：`provider "nosuch" is not configured` |

### E4 并发与会话隔离

6 渠道并发 1 轮，5 个成功且 `resp_model == req_model`（**无串号**）；qwenwork 返回 503（见 §4.4）。

### E5 积分 / 费率

| 渠道 | credits | 明细 |
|---|---|---|
| loomy | 15000 | `积分 total=15000 remain=15000 usable=true` |
| raccoon | 6249 | 每日 249 + 奖励 6000 + 充值 0 + 月度 0 |

费率表与模型表口径：`raccoon` 8/8、`loomy` 8/8，**差集为空**；费率取上游倍率（raccoon 0.2~0.75、loomy 0.1~3）。

### E6 Web UI

`GET /` 200（13.2KB）含 `raccoon` / `loomy` / `小浣熊` / `Loomy`；`app.js`、`style.css` 同样命中。

---

## 4. 发现的问题

### 4.1 ⚠️ 并发刷新竞态（**未修，建议尽快处理**）

**现象**：并发请求同一账号且本地 token 已过期时，每个请求都各自触发一次刷新 ——
raccoon 实测 4 并发 → **1 次成功 + 3 次 `refresh_conflict`**，账号被冷却 10 分钟并返回 503。

```
01:56:09  refresh start platform=raccoon uid=RaccoonJames reason=request   ×4（同一秒）
01:56:10  refresh failed ... {"code":200822,"message":"refresh_conflict"}
01:56:10  refresh success platform=raccoon uid=RaccoonJames expires_at=1790110570
→ /api/state: cooling=true until=09-23 02:06
```

**根因**：`internal/app/app.go` 的 `refreshIfSessionDead` 无并发保护。
已有 `refreshMu`（app.go:94）注释是「防并发刷新积分」，只用于 `RefreshPricing`，**与 token 刷新无关**。

**影响面**：不止 raccoon —— 任何 **refresh_token 会轮换**的渠道都中招（qwenwork 同为此类）。
raccoon 因 access 仅 ≈2h、过期频繁，最容易复现。

**补充**：补测时用**单线程**顺序请求，7 个模型里仍有 1 个瞬时 503 —— 说明竞态不止发生在
并发请求之间，**后台循环（credit / pricing，每 30 分钟）与请求路径之间**同样会撞车。
该 503 复测即恢复（200），无持久影响。

**建议修法**（按账号单飞 + 结果复用）：

```go
// provider/refresh.go（新增）：同一账号的刷新全局单飞，并发调用只打一次上游
func RefreshOnce(u Upstream, a *auth.Auth) error
```

调用点统一改用（app.go 3 处 + scheduler.go 2 处）。等待者复用首个结果，
避免同一个单会话 refresh_token 被并发使用而互相作废。

### 4.2 raccoon 能力标记不准（**已修**）

`internal/raccoon/client.go:283` 原为 `SupportsTools: false`，注释写「未实测」。
本次实测默认模型支持结构化工具调用 → 已改为 `true` 并注明依据与未验证范围（`sn-*` 系列）。
影响：面板此前会把 raccoon 标成「不支持工具」。

### 4.3 各有一个模型不产出结构化工具调用

| 渠道 | 模型 | 上游声明 | 实测 |
|---|---|---|---|
| loomy | `spark-x` | `function_calling: true` | `finish=stop` + 文本形式调用 |
| raccoon | `sn-kimi-k3` | 无 tools 声明（本地按实测标 true） | 同上 |

属模型自身行为，本地无法预知 → 能力标记仍按上游声明，建议在文档注明例外。
（raccoon 侧因上游不声明能力，`SupportsTools` 已按实测改为 true，见 4.2）

### 4.4 qwenwork 凭据已失效（既有问题，需重登）

```
INVALID_REFRESH_TOKEN / invalid_grant（refresh token is invalid）
→ /api/state: disabled=true, reason="refresh session dead"
```

凭据 `expiresAt` 停在 **2026-09-21 02:32**（已 2 天）。这是**「与千问办公 App 互踩」的现实证据** ——
refresh_token 被轮换后两边互相作废。**与本次改动无关**（改前 22:00 保活也会失败并禁用）。

处理：在千问办公客户端重新登录后，面板「从本机客户端导入」。
根治方向见 `docs/千问办公QwenWork逆向对比备忘.md` §风险：按需刷新 + 写回 `auth-v2.dat`。

---

### 4.5 503（`no_healthy_account`）不打日志（可观测性缺口）

`internal/server/handler.go:613-630` 的三个 503 分支只 `writeOpenAIError`，**无 `log.Printf`**。
本次 405a1c 的瞬时 503 因此在 `app.log` 里**查不到任何线索**，只能靠复测反推。
建议：三个分支各补一行日志（渠道、账号计数、`lastErr`）。

---

## 5. 未验证项

1. **`raccoon-405a1c` 的工具调用** —— 请求时遇瞬时 503（见 4.1），复测对话正常但未补测工具能力；
2. **429 / 限流形态** —— 两渠道均未触发过，`Classify` 的限流分支仍无真实数据覆盖；
3. **小浣熊 refresh 真实轮换的成功路径** —— 单会话 token 一换即失效，零风险验证需「客户端退出 → 等 access 自然过期」；
4. **Web UI 交互**（按钮点击、导入流程）—— 本次只验证了静态资源包含关系，未做浏览器交互；
5. **同渠道多账号隔离** —— 当前每渠道仅 1 个账号，只验证了跨渠道并发隔离（E4），
   未验证同渠道多账号的路由与配额分配；
6. **签到** —— 按 D1 决策跳过（两个渠道均无签到活动），未实现也未验证。
