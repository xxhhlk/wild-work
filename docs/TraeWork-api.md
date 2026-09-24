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

> **推论（回答「客户端为什么有档位」）**：档位选择器只在 `support_thinking:true` 时渲染，而 `solo_work_lite`（wild-work 的 traework）整族为 false ——
> 所以**客户端里能看到档位，说明客户端那条入口走的不是 `solo_work_lite`，而是 `agent` 族某个 function**
> （最可能是 `solo_agent_lite`：客户端数据目录名就是 `solo-lite`、前端包也是 `@byted-icube/solo-lite`）。
> 两者不是同一条上游路径，故「客户端有档位」与「wild-work traework 无档位」并不矛盾。
>
> 可用**模型列表**反查客户端实际走哪条：`agent` 族独有 `Doubao-Seed-Code` / `Doubao_1_6` / `glm-5.1` / `qwen-3.5` / `search_agent_qwen_fast*`，
> `work` 族独有 `Doubao-Seed-2.0-Code` / `glm-5-turbo` / `sagitta` / `aquila` / `seed-code-pro-0430` / `file_search_agent` / `explore_sub_agent_v2`。

**实测（经本渠道所用端点 `/api/agent/v3/llm_utils_chat`，模型 `deepseek-v4.1-flash`）**：

| 字段位置 | 结果 |
|---|---|
| 顶层 `reasoning_effort`（n=3） | ❌ 无分离（light 4838 vs high 8079，但 high 组内 3396~9655 与 light 重叠） |
| `custom_model.reasoning_effort`（n=2） | ⚠️ 看似分离（light 6020 vs high 8230） |
| `custom_model.reasoning_effort`（n=3 复测） | ❌ **无分离**（light median 1746 vs high median 1747，组内波动更大） |
| `custom_model.reasoning_effort_level`（n=2） | ❌ 无差异 |
| **非法值哨兵**：上述 4 个位置传 `bogus_value_xyz` | 全部 **200 无报错** → 上游**不解析**这些字段 |

指标用 `usage.reasoning_tokens`（solo_agent 下该字段有返回；TraeWork 不返回）。

**结论**：

1. **TraeWork（solo_work_lite）上游明示不支持档位**（`support_thinking:false` ×42）→ wild-work 对 traework **不投影档位是正确的**，不存在「丢了档位能力」。
2. **TraeCode（solo_agent）** 上游声明档位，但**经 `llm_utils_chat` 传档位无效** —— 该端点不认这些字段（非法值也不报错）。
3. 客户端 UI 能表达档位，但走的是 **native 通道**（`ai_agent.dll` / `hub_bridge.rs`）。若日后要让 traecode 支持档位，需先逆向该通道的请求格式，或找到真正接受档位的端点。

**复现**：`.gotmp/mc-e2e4/` 下的 `dump-models*.ps1`（拉上游原始模型配置，含 `reasoning_effort_config`）与 `t-direct*.ps1`（直连档位探针，参数化字段位置与值）。

## 说明

- 当前仅记录这两个已知文件相关端点
- 目前未发现列出**整个 workspace 所有文件**的公开 API
- 所有产物均存储在 Trae 官方域名（`trae.cn` / `trae-workspace.com` 子域名）下，无需鉴权即可直接下载（只要知道路径，token 在会话 context 中）

