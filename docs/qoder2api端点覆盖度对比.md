# qoder2api 端点覆盖度对比（对照本项目 qoder 渠道）

> 目的：确认 qoder2api 是否包含本项目渠道所需的**全部**端点（模型列表/模型能力/用户余额/登录授权等）。
> 结论：**端点层面几乎完全覆盖，且 qoder2api 更全**（多出 PAT 交换、jobToken、国际区、CSRF 探测）；
> 但有几个本项目**已实现而 qoder2api 未实现**的能力（详见 §3）。
>
> ⚠️ **「本项目」列已过时（2026-09-25 核对）**：本文写作时（2026-09-21）本项目的若干缺口
> ——**签到、`context_config`/`is_default` 解析、模型分类三级回退**——**现均已落地**；
> 逐条差异见 §1/§3/§5 的「状态更新」标注。三个参考仓库的横向对比仍然有效。

---

## 1. 本项目 qoder 渠道当前使用的端点

| 端点 | 域名 | 用途 | 本项目是否使用 |
|------|------|------|----------------|
| `POST /api/v1/deviceToken/poll` | `openapi.qoder.com.cn` | OAuth 设备流换 dt-/drt- | ✅ login_qoder |
| `POST /api/v1/deviceToken/refresh` | 同上 | dt- 轮换 | ✅ RefreshToken |
| `GET /api/v1/userinfo` | 同上 | 账号信息 | ❌ 定义了 `EpUserInfo` 但**未使用** |
| `GET /api/v2/user/plan` | 同上 | 套餐名 | ❌ 定义了 `EpPlan` 但**未使用** |
| `GET /api/v2/quota/usage` | 同上 | 余额 | ✅ UserResource / UserResourceDetail |
| `GET /algo/api/v2/model/list?Encode=1` | `gateway.qoder.com.cn` | 模型列表+能力 | ✅ FetchModels |
| `POST /algo/api/v2/service/pro/sse/agent_chat_generation?...` | 同上 | 推理 SSE | ✅ ChatStream |
| `GET /sash/api/v1/me/daily-check-in/status` | `openapi.qoder.com.cn` | 签到状态 | ✅ qodercn（兜底路径） |
| `POST /sash/api/v1/me/daily-check-in/claim` | 同上 | 签到领取 | ✅ qodercn（兜底路径） |
| `GET /sash/api/v1/me/campaigns` | 同上 | 活动列表（**当前主路径**） | ✅ qodercn / qodercom（主路径） |
| `POST /sash/api/v1/me/campaigns/{id}/claim` | 同上 | 活动领取（**当前主路径**） | ✅ qodercn / qodercom |

> **状态更新（2026-09-25）**：末四行原标「❌ 定义了常量但未用」「❌ 未定义」——
> 现四者**均已实现**：`internal/qodercn/checkin.go` 为 **campaigns 优先 + daily-check-in 兜底**的双路径
> （实测 daily-check-in 的 legacy 系统已全局 DISABLED，故顺序与 qoder2api 相反）；
> `internal/qodercom/checkin.go` 仅 campaigns（其上游无 daily-check-in）。

---

## 2. qoder2api 端点全集（CN + Global）

| 端点 | qoder2api 字段 | 用途 | 本项目对应 |
|------|---------------|------|-----------|
| `/device/selectAccounts`（页面） | `DeviceLoginBase` | OAuth 授权页 | ✅ login_qoder（URL 拼接） |
| `/api/v1/deviceToken/poll` | `PollEndpoint` | 换 dt-/drt- | ✅ |
| `/api/v1/userinfo` | `UserinfoBase` | 账号信息 | ⚠️ 定义未用 |
| `/api/v2/user/plan` | `PlanEndpoint` | 套餐 | ⚠️ 定义未用 |
| `/api/v2/quota/usage` | `QuotaEndpoint` | 余额 | ✅ |
| `/algo/api/v2/service/pro/sse/agent_chat_generation` | `ChatStreamURL` | 推理 SSE | ✅ |
| `/algo/api/v2/model/list?Encode=1` | `ModelListURL` | 模型列表 | ✅ |
| `/algo/api/v3/user/jobToken?Encode=1` | `JobTokenURL` | **PAT → session token 交换** | ❌ **本项目无** |
| `/sash/api/v1/me/daily-check-in/status` | （硬编码） | 签到状态 | ⚠️ 定义未用 |
| `/sash/api/v1/me/daily-check-in/claim` | （硬编码） | 签到领取 | ⚠️ 定义未用 |
| `/sash/api/v1/me/campaigns` | （硬编码） | **活动列表（当前主路径）** | ❌ **本项目无** |
| `/sash/api/v1/me/campaigns/{id}/claim` | （硬编码） | **活动领取（当前主路径）** | ❌ **本项目无** |
| `/api/v1/deviceToken/refresh` | —— | dt- 轮换 | ✅（qoder2api 走 `ExchangeJobToken` 统一处理） |

**qoder2api 独有**：
- `JobTokenURL`（PAT 交换）—— 本项目只支持 Device Flow OAuth，不支持 PAT
- 双区（`.sh` / `.com.cn`）—— 本项目仅 CN
- campaigns 两个端点 —— **签到当前主路径**
- `daily-check-in` 两个端点 —— 本项目已定义常量但实现为空

---

## 3. 模型能力（模型列表）覆盖度对比

两者都调 **同一个上游** `GET /algo/api/v2/model/list?Encode=1`（COSY 签名），但解析深度不同：

| 能力字段 | 上游字段 | qoder2api | 本项目 |
|----------|----------|-----------|--------|
| 模型 key | `key` | ✅ | ✅ |
| 显示名 | `display_name` | ✅ | ✅ |
| 是否启用 | `enable` | ✅ | ✅ |
| 是否默认 | `is_default` | ✅ | ✅ 已解析 |
| **思考模式** | `is_reasoning` | ✅ | ✅ `SupportsReasoning` |
| **视觉能力** | `is_vl` | ⚠️ 未解析 | ✅ `SupportsImages` |
| 最大输入 | `max_input_tokens` | ✅ | ✅ `ContextFromAPI` |
| **上下文窗口** | `context_config.*.token_count` | ✅ 优先读 | ✅ 已优先读（默认档 + 全档位） |
| **最大输出** | 推导（reasoning 32768 / 否则 16384） | ✅ | ✅ 已推导 |
| 价格倍率 | `price_factor` | ✅ | ✅（另走 FetchModelPricing） |
| 场景分类 | `assistant`/`developer`/`chat` 多级回退 | ✅ 三级回退 | ✅ 三级回退 |

> **状态更新（2026-09-25）**：`is_default` / `context_config` / `max_output_tokens` / 三级回退
> **四项均已实现**（2026-09-22 由 issue #27 同批改动补齐，覆盖 `internal/qoder`、
> `internal/qodercn`、`internal/qodercom` 三个渠道）。§3.1 的「两处可改进点」已不再是改进点。

### 3.1 本项目模型能力（原「两处可改进点」已落地）

1. ~~**`context_config.token_count` 未解析**~~ → ✅ **已解析**：取标了 `is_default` 的档作上下文窗口，
   并保留全部档位（`AvailableWindows`）供选档校验；缺失才回退 `max_input_tokens`。
2. ~~**模型分类仅取 `chat`**~~ → ✅ **已三级回退**：`assistant` → `developer` → `chat`，
   上游把模型挪场景时不再硬失败（与 qoder2api 同批 issue #27）。

---

## 4. 用户余额 / 登录授权覆盖度

| 能力 | qoder2api | 本项目 | 结论 |
|------|-----------|--------|------|
| OAuth Device Flow（PKCE+S256） | ✅ | ✅ | 等价 |
| PAT 登录（明文 token） | ✅ `AddAccountByPAT` | ❌ | **qoder2api 更全** |
| jobToken 交换 | ✅ | ❌ | qoder2api 独有 |
| dt- 刷新（轮换） | ✅ | ✅ | 等价 |
| 余额（quota/usage） | ✅ | ✅ | 等价 |
| 余额分桶（userQuota + addOnQuota） | ✅ | ✅ | 等价（本项目 `赠送额度` 条目） |
| 套餐名（plan） | ✅ 展示 | ⚠️ 定义未用 | 本项目可选补 |
| 账号信息（userinfo） | ✅ 展示 | ✅ **已使用**（qodercn/qodercom 取昵称） | 等价 |
| 签到 | ✅ 双路径 | ✅ 双路径（CN）/ 单路径（COM） | 等价 |
| 模型列表动态拉取 | ✅ | ✅ | 等价 |
| 多协议（OpenAI/Anthropic/Codex） | ✅ bridge 层 | 本项目另有 gateway 层 | 各自实现 |
| 双区（CN/Global） | ✅ | ✅（QoderCN + QoderCOM 两渠道） | 等价 |
| 定时签到 + 自动刷新 + 解冻 | ✅ | ✅ 签到（10:15） + token keepalive | 等价 |
| Web 控制台 | ✅ | 本项目另有 Web UI | 各自实现 |

---

## 5. 最终回答

### Q：qoder2api 是否包含本项目渠道的所有必要端点？

**是，且更全。** 逐项对照：

| 类别 | 是否覆盖 | 说明 |
|------|----------|------|
| **模型列表** | ✅ 完全覆盖 | 同一 `/algo/api/v2/model/list`，两边均已多场景回退 |
| **模型能力** | ✅ 覆盖且更强 | qoder2api 额外解析 `context_config.token_count`、`max_output_tokens`、`is_default`（**本项目已补齐**）；本项目独有 `is_vl` 视觉标记 |
| **用户余额** | ✅ 完全覆盖 | 同一 `/api/v2/quota/usage`，分桶口径一致 |
| **用户登录授权** | ✅ 覆盖且更强 | 同一 Device Flow；qoder2api **额外支持 PAT + jobToken 交换** |
| **推理** | ✅ 完全覆盖 | 同一 chat SSE 端点 + COSY 签名 + QoderEncoding |
| **签到** | ✅ 完全覆盖 | 双方均为双路径（本项目 campaigns 优先，与实测一致） |
| **国际区** | ✅ 覆盖 | 本项目以 **QoderCOM 独立渠道**承接（非 region 分支） |

### 结论

1. **qoder2api 可以作为本项目 qoder 渠道的完整端点参考**，无需担心遗漏必要端点。
2. **qoder2api 在 1 处比本项目更全，值得借鉴**：
   - **PAT + jobToken 交换** —— 本项目仅有 OAuth，若想支持用户粘贴 PAT 可补
3. **本项目在 1 处比 qoder2api 更全**：`is_vl`（视觉能力）解析与 `SupportsImages` 暴露。
4. **国际区**（`.sh`）已以 **QoderCOM 独立渠道**接入（类比 workbuddy/workbuddyai），未在 qoder 渠道内做 region 分支。

### 建议的补充改造项（签到等已在 2026-09-22 落地）

| 优先级 | 改造 | 理由 |
|--------|------|------|
| 低 | 启用 `EpPlan` | 面板可展示套餐名（`EpUserInfo` 已用于取昵称） |
| 低 | PAT 登录 + jobToken | 提供 Device Flow 之外的备选登录方式 |
