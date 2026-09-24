# TraeWork 网页版 API 逆向备忘

本文档记录 TraeWork (Solo) 公开端点及结构，按用途分类。

## 1. 核心模型接口

| 端点 | 方法 | 用途 |
|------|------|------|
| `/api/ide/v1/get_detail_param` | POST | 获取全量模型配置信息（返回 `config_info_list`，用于填充 `/v1/models`） |
| `/api/remote/v1/models` | GET | 拉取当前账号模型定价信息（速率、积分消耗） |
| `/agent/v3/llm_utils_chat` | POST | 核心聊天请求端点 |

## 2. 签到/积分接口

| 端点 | 方法 | 用途 |
|------|------|------|
| `/trae/api/v2/ug/checkin_credits/status` | GET | 获取签到状态 |
| `/trae/api/v2/ug/checkin_credits/claim` | POST | 签到领积分 |
| `/trae/api/v2/pay/web_user_ent_usage` | GET | 当前积分用量查询 |

## 3. Workspace 文件产物接口（新增）

| 端点 | 方法 | 用途 |
|------|------|------|
| `GET https://work.trae.cn/api/remote/v1/chat_sessions/{chatId}/output-artifact-metadata` | GET | 获取指定聊天会话的**输出产物元数据列表**（AI 在该会话生成的文件，每个文件包含 `id`/`name`/`path`/`created_at` 等） |
| `GET https://agent-sandbox-bj-d2-gw.trae.cn/explorer/{sessionId}/file` | GET | 下载 workspace 中指定文件（URL 路径如上） |

## 4. 思考档位（reasoning effort）调研（2026-09-24）

**上游声明**：`get_detail_param` 的每个 config 都带 `reasoning_effort_config`，而**哪些 function 声明档位，按名字分成整齐的两族**（2026-09-24 逐 function 全量 dump）：

| function | configs | `support_thinking: true` |
|---|---|---|
| `solo_agent`（TraeCode） | 64 | **19** |
| `chat_v3` | 55 | **17** |
| `solo_agent_lite` | 41 | **14** |
| `solo_agent_remote` | 41 | **13** |
| `solo_work_lite`（**wild-work 的 traework**） | 42 | **0** |
| `solo_work_remote` | 41 | **0** |
| `solo_design_lite` / `solo_design_remote` | 23 / 22 | **0** |
| `solo_coder` / `solo_builder` | 41 / 3 | **0** |

**规律：`agent` 族（含 `chat_v3`）全部有档位；`work` / `design` / `coder` / `builder` 族一个都没有。**
同一模型在不同族里的档位声明可以不同（`deepseek-v4.1-flash` 在 `agent` 族 = `["light","high","extra_high"]`，在 `work` 族 = 无）。

`solo_agent` 下的档位形态（共 5 种）：

| options | default_level | 模型 |
|---|---|---|
| `["light","high"]` | `high` | Doubao-Seed-2.1-Pro/Turbo、Doubao-Seed-Code、computer_use_subagent |
| `["light","high","extra_high"]` | `high` | **deepseek-v4.1-flash**、qwen3.8-max、qwen3.8-flash、DeepSeek-V4-Flash/Pro-Official |
| `["high","extra_high"]` | `high` | DeepSeek-V4-Flash、DeepSeek-V4-Pro |
| `["light","high","extra_high"]` | `extra_high` | glm-5.3 / glm-5.3-flash / glm-5.3-flashx、kimi-k3 / kimi-k3-auto / kimi-k2.8-preview |
| `["high","extra_high"]` | `high` | glm-5.2 |

**客户端**（TRAE SOLO CN，`%LOCALAPPDATA%\Programs\TRAE SOLO CN`）：

- `product.json` 的 `nameAlias` / `win32NameVersion` = **`TraeWork CN`**、`win32ShellNameShort` = `Trae Work`、`brandName` = `TRAE SOLO` —— 即**这就是 TraeWork 桌面客户端**；
- i18n 内部值 → 显示值：`light→low`、`high→high`、`extra_high→xhigh`，标题「思考强度」；
- `@byted-icube/solo-lite` 读 `reasoning_effort_config.support_thinking` 决定是否渲染档位选择器 —— **UI 是数据驱动的**；
- native 模块 `ai_agent.dll` / `harness.dll` 里 `struct CustomModel with 31 elements` **含 `reasoning_effort` 与 `reasoning_effort_level` 两个字段**；
- 客户端支持多个 function：`solo_agent` / `solo_agent_lite` / `solo_work_lite` / `solo_agent_remote` / `solo_work_remote` / `solo_design_lite` …

**客户端运行日志的实测证据**（`%APPDATA%\TRAE SOLO CN\logs\*\window1\renderer.log`，2026-09-24 抽取）：

客户端对每个模型首次打开模型选择浮层时会打一条 `[ModelSelectPresentation][TooltipDiagnostic] first open`，
内含服务端下发的 feature 路由与**实际渲染出的区块**。逐条解析 41 条记录（覆盖 13 个模型）后：

| 观测项 | 结果 |
|---|---|
| `configuredRendererSections` 含 `reasoning_effort_selector` | **13/13 模型 = 有**（服务端确实下发了该渲染器配置） |
| `renderedSections` 含 `reasoning_effort_selector` | **0/41 条 = 从未真正渲染** |
| `rawFeatures` 里出现 `reasoning_effort` | **从未出现**（只有 `reasoning`，且 `dataKeys: []`） |
| `resolvedFeatureRoutes` | 只解析出 `consumption_rate` / `discount` 等**计费类**路由，**档位路由始终不在其中** |
| 日志中 `extra_high` / `light` 等档位值 | **全量日志 0 命中**（档位值从未出现在客户端任何日志里） |

日志样例（`glm-5.3`，其余 12 个模型同构）：

```
rawFeatures: [access, consumption_rate, discount, reasoning(enable=true,dataKeys=[]), context_windows]
configFeatureOrder:  [..., consumption_rate, reasoning_effort, max_mode, ...]
configuredRendererSections: [{featureRoute:max_mode, renderer:max_mode_switch},
                             {featureRoute:reasoning_effort, renderer:reasoning_effort_selector}]
resolvedFeatureRoutes: [consumption_rate, discount/member_discount]   ← 无 reasoning_effort
renderedSections:      [consumption_rate, discount/member_discount]   ← 无档位选择器
```

→ **修正先前的推论**：并非「客户端走 agent 族所以有档位」。真相是 ——
**档位选择器在客户端是「配置里有、运行时没渲染」的状态**：服务端在 `configFeatureOrder` /
`configuredRendererSections` 里声明了 `reasoning_effort`，但它没有进入 `resolvedFeatureRoutes`
（未通过 feature 开关校验），因此从未出现在 `renderedSections`。客户端 JS 侧的下游逻辑
（`support_thinking===true ? default_level : undefined`）也因此从未被触发。

> 可用**模型列表**反查客户端实际走哪条 function：`agent` 族独有 `Doubao-Seed-Code` / `Doubao_1_6` / `glm-5.1` / `qwen-3.5` / `search_agent_qwen_fast*`，
> `work` 族独有 `Doubao-Seed-2.0-Code` / `glm-5-turbo` / `sagitta` / `aquila` / `seed-code-pro-0430` / `file_search_agent` / `explore_sub_agent_v2`。
> 日志中出现的 13 个模型（`DeepSeek-V4-Flash/Pro 正式版`、`GLM-5.2/5.3`、`Kimi-K2.6/K2.7-Code/K3`、
> `MiniMax-M3`、`Qwen3.7-Plus`、`Qwen3.8-Max`、`Seed-2.1-Pro-0915/Turbo`、`Seed-Evolving`）在两族中都有，
> **无法据此反查 function**；但 `renderedSections` 的结论与 function 无关 —— 13 个模型全部未渲染档位。

**实测（经本渠道所用端点 `/api/agent/v3/llm_utils_chat`，模型 `deepseek-v4.1-flash`）**：

> ⚠️ **全部探针的 `function` 字段写的都是 `solo_agent`** —— 即**测的就是 TraeCode 那条路径**
> （TraeWork 的 `solo_work_lite` 只在 cross-function 对照中作为基线出现）。
> 下文所有「无分离」结论均直接适用于 **traecode 渠道**。

| 字段位置 | 结果 |
|---|---|
| 顶层 `reasoning_effort`（n=3） | ❌ 无分离（light 4838 vs high 8079，但 high 组内 3396~9655 与 light 重叠） |
| `custom_model.reasoning_effort`（n=2） | ⚠️ 看似分离（light 6020 vs high 8230） |
| `custom_model.reasoning_effort`（n=3 复测） | ❌ **无分离**（light median 1746 vs high median 1747，组内波动更大） |
| `custom_model.reasoning_effort_level`（n=2） | ❌ 无差异 |
| **非法值哨兵**：4 个位置传 `bogus_value_xyz` | 全部 **200 无报错** → 上游**不解析**这些字段 |
| **类型哨兵**（更强判据，见下） | `reasoning_effort` 传 number/object/array → **全部 200 且正常生成** |
| `temperature=0` 确定性复测 | ❌ 仍无分离（base 6305/5709；top.light 3466/3569；top.xhigh 3841/5211；cm.light 6759；cm.xhigh 3048 反转） |
| **跨 function 基线对照** | `solo_work_lite` 基线 rtok 1283/1966；`solo_agent` 基线 1663/2058 —— 同题同模型，两族默认思考量同量级，traework 也能思考（思考≠档位） |

**类型哨兵为什么比非法值更强**（2026-09-24 round9/10，决定性）：

| 请求 | 上游响应 | 说明 |
|---|---|---|
| `function=123`（错类型） | **HTTP 400** 直接拒绝 | 端点**有强类型校验** |
| `function=bogus_fn_xyz`（不存在的名字） | `event:error` `code:4001` `the param is invalid` | 端点**有两层校验且都会报错** |
| `cm.reasoning_effort=123`（错类型） | **200 + 正常生成**（len 2639，content=1） | 该字段**未被反序列化** |
| `cm.reasoning_effort={}` / `["x"]` | **200 + 正常生成** | 同上 |
| `top.reasoning_effort=123` / `reasoning_effort_level=123` | **200 + 正常生成** | 同上 |
| `application_config.reasoning_effort` / `extra_config.reasoning_effort` 传 bogus | 200 + 正常生成 | 亦不解析 |
| **全字段齐射**（round13）：一次性同时发 12 个候选位置 × `extra_high` | ❌ 与基线无差异（base 4364/5060 vs shotgun 5403/4613，区间重叠） | 穷举后仍无字段生效 |

→ 结论：`llm_utils_chat` 的请求 schema 里**根本没有档位字段**。若字段存在，Rust serde 会在**类型不匹配时直接报错**（已用 `function` 证明该机制有效）。

指标用 `usage.reasoning_tokens`（solo_agent 下该字段有返回；TraeWork 不返回）。

**端到端验证（经 wild-work 网关，两渠道同账号同模型，2026-09-24）**：

同一账号（traework/traecode 共用池）、同一模型 `deepseek-v4.1-flash`、同一难题、`temperature=0`，
经网关 `/v1/chat/completions` 各采样 6 次：

| 渠道 | base（未表达档位） | xhigh（显式最高档） | 判定 |
|---|---|---|---|
| `traework/deepseek-v4.1-flash` | 3592 / 3587（n=2） | 2102 / 6150（n=2） | ❌ 无分离 |
| `traecode/deepseek-v4.1-flash` | 3065 / 5792 / 4944 / **6812** / 4714 / 6438 | **7056** / 6515 / 5629 / **5242** / 5034 / 5253 | ❌ **无分离** |

- base 区间 **3065–6812**，xhigh 区间 **5034–7056**，**完全重叠**；
- 关键反例：`base.4 = 6812` **高于** `xhigh.4 = 5242`；n=2 时曾出现「base max 5792 < xhigh min 6515」的假分离，
  **扩到 n=6 即崩塌** —— 与直连探针同一天踩到的同一个坑（n=2 的「似分离」不可信）。

→ **答案：走 traecode 渠道也不能让思考档位生效。**

**结论**：

1. **TraeWork（solo_work_lite）上游明示不支持档位**（`support_thinking:false` ×42）→ wild-work 对 traework **不投影档位是正确的**，不存在「丢了档位能力」。
2. **TraeCode（solo_agent）上游虽声明档位（19/64），但传档位同样无效** ——
   直连四重验证（非法值哨兵 / 类型哨兵 / 全字段齐射 / temperature=0 确定性复测）+ **经网关端到端 n=6** 均无效果。
   原因：`llm_utils_chat` 的请求 schema 里**根本没有档位字段**。
   → **走 traecode 渠道也无法让思考档位生效**，与 traework 表现一致。
3. **客户端能生效、网关不能 —— 根因是「通道不同」而非「字段名不同」**：
   客户端档位选择器**真的渲染**（0.1.69 实测 19/25），请求里也确实带
   `model_info.reasoning_effort_level`；但客户端**主聊天不走 `llm_utils_chat`**，
   而是走 native harness 的本地 RPC（`lite.send_message` → 转发 `start_chat`，经 `127.0.0.1:51000` / vsock）。
   `llm_utils_chat` 只用于标题生成等**辅助任务**。详见 §4.1。
4. 若日后要让 traecode 支持档位，需找到真正接受档位的端点，或逆向 native 通道的请求格式。
   **注意**：上游 `reasoning_effort_config` 声明（`support_thinking:true`）只影响**客户端 UI 是否渲染选择器**，
   与请求侧是否被接受**无关** —— 这是本次最容易误判的一点。

### 4.1 深挖补充（2026-09-24 深夜，客户端升级到 0.1.69 后）

**① 推翻「客户端也没渲染档位」的旧结论** —— 那是旧客户端（0.1.52）的日志。当前客户端
`TRAE SOLO CN 0.1.69 / build 2.3.87413`（装于 `E:\Program Files\TRAE SOLO CN`）的 `renderer.log`：

| 观测项 | 旧结论（0.1.52） | **实测（0.1.69）** |
|---|---|---|
| `configuredRendererSections` 含 `reasoning_effort_selector` | 13/13 | **25/25** |
| `renderedSections` 含 `reasoning_effort_selector` | 0/41 ❌ | **19/25 ✅ 真的渲染了** |

→ **用户在客户端看到档位选择器是真的**。未渲染的 6 条集中在 `minimax-m3` / `kimi-k2.6` / `qwen-3.7-plus`（上游 `support_thinking:false`）。

**② 客户端确实在请求里发档位**（`renderer.log` 行 967，真实请求体）：

```
"user_message_context": { ..., "model_info": {
    "provider":"", "is_preset":true, "config_name":"deepseek-v4.1-flash", "config_source":1,
    "model_name":"deepseek-v4.1-flash", "display_model_name":"DeepSeek-V4.1-Flash",
    "use_remote_service":true, "multimodal":true, "prompt_max_tokens":936000,
    ... "reasoning_effort_level":"extra_high" } }
```

→ **档位容器是 `model_info`**，字段名 `reasoning_effort_level`（不是我们先前测的 `custom_model`）。

**③ 但客户端主聊天根本不走 `llm_utils_chat`**（这是先前最大的误判）：

| 证据 | 内容 |
|---|---|
| `renderer.log` | `[API client][rust] invoke cost= 557ms lite.send_message` + `[SoloLiteApiObservability] route=chat.sendMessage → target_method=send_message` |
| `harness.dll` 路由表 | `chat.start_chat /api/v1/chat/start_chat`、`chat.initialize /api/v1/chat/initialize`、`lite.send_message`、`chat.subscribe_events /api/v1/chat/subscribe_events` |
| `harness.dll` 日志串 | **`lite: forwarding send_message model selection to start_chat`** |
| `ai_agent.dll` | `llm_utils_chat failed for` **title / icon / commit message / system_diagnosis / input optimization / project name / branch name / image_to_text / video_to_text / custom_agent_generation** —— **全是辅助任务** |

→ **`llm_utils_chat` 是辅助任务端点，不是主聊天端点**。桌面客户端主链路是
**native harness 的本地 RPC**（`lite.send_message` → 转发 `start_chat`），走 `127.0.0.1:51000`（TCP OPEN，非 HTTP）与 vsock，
**不经过公网 HTTP**。所以「客户端能生效、网关不能」的根因不是字段名，而是**传输通道不同**。

**④ 补测 `model_info` 容器（round14，客户端真实容器）—— 仍不解析**：

| 请求 | 结果 |
|---|---|
| `model_info`（无档位）基线 ×2 | rtok 6824 / 4692 |
| `model_info.reasoning_effort_level=light` ×2 | 3377 / **14026** ← 组内跨度比组间大 |
| `model_info.reasoning_effort_level=extra_high` ×2 | 4810 / 5085 |
| **类型哨兵** `model_info.reasoning_effort_level=123` | **200 正常生成** → 未反序列化 |
| **类型哨兵** `model_info.reasoning_effort=123` | **200 正常生成** → 未反序列化 |

→ 至此**第五个容器位置**（`custom_model` / 顶层 / `application_config` / `extra_config` / `model_info`）确认不解析。

**⑤ 新发现的端点全部打不通（round15/16/17）**：

| 端点 | 结果 | 说明 |
|---|---|---|
| `/api/v1/chat/start_chat` | **404** | native 内部 RPC 路由，公网网关不暴露 |
| `/api/v1/chat/initialize` | **404** | 同上 |
| `/api/v1/lite/send_message` | **404** | 同上 |
| `/api/ide/v1/llm_raw_chat` | **400** | 存在但 schema 未知 |
| `/api/ide/v2/llm_raw_chat` | **400** | 同上 |
| `/api/agent/v3/create_agent_task`（带真实 session_id + 全字段） | **400** | 同上 |

**⑥ native 侧档位证据（`harness.dll` / `ai_agent.dll` 字节级提取）**：

- `struct LiteSendMessageRequest with 22 elements` 字段序列以
  **`provider, reasoning_effort, reasoning_effort_level, is_preset, config_name, config_source, ...`** 开头
  → **档位是 lite 请求的顶层字段**；
- `struct StartChatRequestData with 71 elements` 含 `provider, reasoning_effort, reasoning_effort_level, mode_type`；
- `ai_agent.dll` 含模块 **`infrastructure/context/reasoning_effort.rs`**，其日志串为
  **`[reasoning_effort] both legacy and modern fields supplied; forwarding both`** 与
  `[reasoning_effort] invalid legacy value, fallback to medium`
  → **native 层确实会解析并转发档位**（`forwarding both` 指同时转发 `reasoning_effort` 与 `reasoning_effort_level`）。

### 4.2 想让 trae 渠道档位生效，可行路径

| 路径 | 可行性 | 说明 |
|---|---|---|
| 继续在 `llm_utils_chat` 上试字段名 | ❌ **已穷尽** | 5 个容器位置 + 全字段齐射 + 类型哨兵，全部不解析 |
| 复刻 native 的 `start_chat` RPC | ⚠️ 理论可行、成本极高 | 需逆向 `127.0.0.1:51000` 的自定义帧协议 + vsock 握手 + `StartChatRequestData` 71 字段必填集 |
| 让 wild-work 客户端直连 Trae 官方客户端 | ✅ 但已非「渠道」 | 即用户直接用 TraeWork 客户端（档位原生生效） |
| 换用真正支持档位的渠道 | ✅ **推荐** | `qodercn` / `workbuddyai` 已实测档位生效（见 `docs/qoderCN渠道接入备忘.md` §8） |

**一句话**：traework/traecode **在公网 HTTP 面上无法让档位生效**（已穷尽证明），客户端能生效是因为它走 native 本地 RPC。
wild-work 若要在 trae 渠道支持档位，等价于**重写一个 native harness 客户端** —— 不建议；档位需求请走 qodercn / workbuddyai。

**仍未查（可选后续）**：
1. `127.0.0.1:51000` 的帧协议（非 HTTP，需抓 native↔renderer 的 IPC）；
2. `create_agent_task` / `llm_raw_chat` 的必填 schema（400 但字段集未知，可从 DLL 的 serde 结构逆推）；
3. Trae VM 侧 vsock 通道（lite 会话在沙箱 VM 内执行）；
4. 是否另有上游域（`agent.trae.cn` vs `trae-api-cn.mchost.guru`）接受档位。

**复现**：`.gotmp/mc-e2e4/` 下的 `dump-models*.ps1`（拉上游原始模型配置，含 `reasoning_effort_config`）、
`t-direct*.ps1`（直连档位探针）、`t-round9/10/11/13.ps1`（类型哨兵 / 响应体判定 / 确定性复测 / 全字段齐射）、
**`t-round14.ps1`（`model_info` 容器 + 类型哨兵）、`t-round15.ps1`（native 端点 404 发现）、
`t-round16.ps1`（`llm_raw_chat` 家族）、`t-round17.ps1`（`create_agent_task` 全字段）**；
`.gotmp/mc-e2e5/` 下的 `t-e2e.ps1` / `t-e2e2.ps1`（经网关的两渠道端到端对比）。
客户端日志：`%APPDATA%\TRAE SOLO CN\logs\<ts>\window1\renderer.log`（含 `TooltipDiagnostic` 渲染记录与真实请求体）。
native 提取：`E:\Program Files\TRAE SOLO CN\resources\app\modules\ai-agent\{harness,ai_agent}.dll`（`strings` + 字节级定位）。

## 说明

- 当前仅记录这两个已知文件相关端点
- 目前未发现列出**整个 workspace 所有文件**的公开 API
- 所有产物均存储在 Trae 官方域名（`trae.cn` / `trae-workspace.com` 子域名）下，无需鉴权即可直接下载（只要知道路径，token 在会话 context 中）

