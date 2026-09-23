# wild-work

> 多渠道账号聚合桌面工具——把 WorkBuddy(CodeBuddy) 国内版/国际版、TraeWork、QoderCN、QoderCOM（国际版）、千问办公、商汤小浣熊、Loomy（讯飞）的多个账号聚合成一个 OpenAI 兼容 API，并额外提供**无需账号**的 OpenCodeZen 匿名免费通道，双击启动，浏览器管理。

[![GitHub](https://img.shields.io/badge/GitHub-rockswang%2Fworkbuddy--wild-blue)](https://github.com/rockswang/workbuddy-wild)

## 功能

- **三接口协议兼容**：`/v1/chat/completions`（OpenAI Chat）+ `/v1/responses`（OpenAI Responses）+ `/v1/messages`（Anthropic Messages，含 `count_tokens`）+ **`/v1/systemone`（Jev 结构化决策）**，可直接接入 `codex` CLI、`claude` CLI 与需要语义决策的自动化流程
- **请求体指纹脱敏**：自动清除 Claude Code / Codex CLI 注入的模板句，防止上游 11128 内容拦截
- **OpenAI 兼容代理**：`/v1/chat/completions`、`/v1/models`，支持流式/非流式，模型前缀路由
- **错误分类精细化**：区分「请求问题」与「账号问题」——内容拦截/上下文超限不罚号，限流/风控/账号故障分级冷却，429 不再误判余额耗尽
- **十渠道聚合**：WorkBuddyCN(CodeBuddy) + WorkBuddyAI（国际版） + TraeWork + TraeCode + QoderCN + QoderCOM（国际版） + 千问办公 + 商汤小浣熊 + Loomy（讯飞） + OpenCodeZen（`oczen/*`，匿名免费、无需账号）。
  商汤小浣熊支持两条路：面板登录按钮走**协议劫持**（登录期间临时接管 `office-raccoon://` 深链截获网页授权码并自兑 token，结束立即恢复注册表），弹窗里的次按钮「从客户端导入」则复用本机客户端凭据；Loomy 为**导入型**（凭据不由本工具登录产生，从**本机已登录的官方客户端**读取）。模型前缀路由，粘性路由优先复用账号以提升会话缓存利用率，临期积分优先消耗；旧 Qoder（`qoder/*`）路由保留但已从界面下线
- **匿名免费通道**（OpenCodeZen）：内置 `public` 凭证即可调用 Zen 上的免费模型（含 `big-pickle`），无需注册/登录；面板固定一个 `[OpenCodeZen] 匿名` 条目（不可增删停用、无签到、积分显示「不适用」）
- **自动签到**：每日定时签到领额度，token 保活，冷却状态机
- **自动领日活奖励**（WorkBuddy 国际版）：定时自动用免费模型对话保活，自动领取每日活跃奖励，无需手动签到
- **用量与流水统计**：token 流水（渠道×模型）与积分流水（入项/消耗/过期）双口径记账，面板折线图 + 模型用量榜 + 积分流水对账，临期阈值可配
- **Web 管理面板**：账号管理（添加/签到/刷新/停用/删除）、积分明细、模型列表和费率、API 配置、统一设置弹层（含单渠道代理）
- **系统托盘**：常驻右下角，双击打开面板，右键菜单操作
- **跨平台**：Windows（完整支持）、macOS（代码已就绪，CI 构建）、Linux（无头模式）
- **developer→system 角色转换**：自动将下游 Agent 发送的 `<developer>` 角色改写为 `<system>`，避免上游触发内容过滤
- **上游协议头仿真**：出站自动注入官方客户端指纹头（`X-CodeBuddy-Request`、`X-Machine-ID` 等），降低风控误判风险

## 项目渊源

本项目最初算法来源于 [Sliverkiss/](https://github.com/Sliverkiss/) 大佬的 xxx2api 系列项目，本项目针对多渠道进行了聚合，针对 Windows 环境进行了适配，降低了使用门槛，并提供跨平台 Web 管理界面。

### 声明

**本项目只是各上游渠道的集成与聚合，本身不涉及任何逆向分析和破解工作。**

即：本仓库不产出、不包含、不分发任何协议逆向研究成果。各渠道的接口参数、签名算法、风控指纹等底层知识，全部来自社区其他开源项目的公开成果；本项目所做的是**在既有公开成果之上做多渠道路由、账号池调度、协议适配与界面封装**，并提供跨平台、低门槛的使用形态。所有逆向相关的功劳与风险归于下述各上游项目作者。

### 致谢

感谢以下开源项目及其作者。它们分别完成了各渠道的接口逆向与协议封装，是本项目各渠道能够落地的前提：

| 项目 | 贡献 |
|------|------|
| [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) | WorkBuddy(CodeBuddy) 国内版接口逆向，本项目最初的算法来源 |
| [Sliverkiss/qoderwork2api](https://github.com/Sliverkiss/qoderwork2api) | QoderWork 接口逆向，Qoder 渠道参考 |
| [wicm84266964/Buddy2api](https://github.com/wicm84266964/Buddy2api) | 千问办公渠道逆向参考 |
| [wpy030414/xrl-router-plugin-qwenwork](https://github.com/wpy030414/xrl-router-plugin-qwenwork) | 千问办公渠道逆向参考 |
| [iceloon/dsh-workbuddyai-connect](https://github.com/iceloon/dsh-workbuddyai-connect) | WorkBuddy 国际版接口逆向，国际版渠道主要参考 |
| [corrinehu/dsh-workbuddy-connect](https://github.com/corrinehu/dsh-workbuddy-connect) | WorkBuddy 渠道架构参考（MIT） |
| [Zhengyuuuui/qoder2api](https://github.com/Zhengyuuuui/qoder2api) | Qoder 渠道端点与逻辑比对参考 |
| [icebears111/qoderwork2api](https://github.com/icebears111/qoderwork2api) | Qoder 渠道端点与逻辑比对参考 |
| [zhangdailin/Orchids-2api](https://github.com/zhangdailin/Orchids-2api) | Qoder 渠道端点与逻辑比对参考 |
| [jasonxu114514/opencode2api](https://github.com/jasonxu114514/opencode2api) | OpenCodeZen（oczen）匿名渠道 endpoint 特殊要求分析 |
| [FishBottle7/opencode2dsh](https://github.com/FishBottle7/opencode2dsh) | 同上，OpenCode 相关补充参考 |

> 若上述项目作者认为本项目的引用方式不当，请提 issue 联系，我们会立即调整或移除相关内容。

同时感谢给本项目 PR 的各位贡献者！

## 使用方式

### Windows

1. 从 [Releases](https://github.com/rockswang/workbuddy-wild/releases) 下载 `wild-work.exe`
2. 放到任意目录，双击启动
3. 右下角出现 W 图标，**双击托盘图标** → 浏览器打开 Web 管理面板
4. 在面板中点击「＋ WorkBuddyCN」/「＋ WorkBuddyAI」/「＋ TraeWork」/「＋ QoderCN」/「＋ QoderCOM」/「＋ 千问办公」添加账号
   （「＋ 商汤小浣熊」/「＋ Loomy」是**导入**按钮：读取本机已登录客户端的凭据，不需要浏览器登录；
   小浣熊的 refresh_token 是单会话的，导入后请**退出小浣熊客户端**，否则两边会互相抢刷新并报 `refresh_conflict`；
   OpenCodeZen 无需添加账号，启动即已就绪）
5. 根据下方配置说明接入你的 AI 客户端

### 托盘菜单

- **打开主界面**：在浏览器中打开管理面板
- **查看日志**：用记事本打开运行日志
- **退出**：退出程序

### 无头模式（Linux 服务器）

```bash
./wild-work --no-tray
```

启动后打印 API 地址、Key 等信息，阻塞运行，Ctrl+C 退出。

## 配置 AI 客户端

### 1. 获取模型列表

wild-work 的 `/v1/models` 端点返回当前所有可用模型。在终端中执行：

```bash
curl -s -H "Authorization: Bearer WildWorkAPI" http://127.0.0.1:7863/v1/models
```

### 2. 配置 Pi（models.json）

Pi 不支持自动拉取模型列表，需要手动编辑 `~/.pi/agent/models.json`（Windows 路径 `C:\Users\<用户名>\.pi\agent\models.json`），在 `providers` 中加入 wild-work 配置：

```json
{
  "providers": {
    "wild-work": {
      "name": "wild-work",
      "api": "openai-completions",
      "baseUrl": "http://127.0.0.1:7863/v1",
      "apiKey": "WildWorkAPI",
      "models": [
        {
          "id": "workbuddy/auto",
          "name": "自动路由 (WorkBuddy)",
          "reasoning": false,
          "input": ["text"],
          "contextWindow": 168000,
          "maxTokens": 32000,
          "compat": { "maxTokensField": "max_tokens" }
        },
        {
          "id": "workbuddy/deepseek-v4-pro",
          "name": "DeepSeek V4 Pro (WorkBuddy)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 1000000,
          "maxTokens": 50000,
          "compat": {
            "thinkingFormat": "deepseek",
            "supportsReasoningEffort": true,
            "maxTokensField": "max_tokens"
          }
        },
        {
          "id": "workbuddy/glm-5.3",
          "name": "GLM-5.3 (WorkBuddy)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 1000000,
          "maxTokens": 48000,
          "compat": { "maxTokensField": "max_tokens" }
        },
        {
          "id": "traework/DeepSeek-V4-Pro",
          "name": "DeepSeek V4 Pro (TraeWork)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 1000000,
          "maxTokens": 50000,
          "compat": {
            "thinkingFormat": "deepseek",
            "supportsReasoningEffort": true,
            "maxTokensField": "max_tokens"
          }
        },
        {
          "id": "traework/glm-5.2",
          "name": "GLM-5.2 (TraeWork)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 1000000,
          "maxTokens": 48000,
          "compat": { "maxTokensField": "max_tokens" }
        },
        {
          "id": "workbuddyai/deepseek-v4.1-flash",
          "name": "DeepSeek V4.1 Flash (WorkBuddy 国际版)",
          "reasoning": true,
          "input": ["text", "image"],
          "contextWindow": 1000000,
          "maxTokens": 128000,
          "compat": {
            "thinkingFormat": "deepseek",
            "supportsReasoningEffort": true,
            "maxTokensField": "max_tokens"
          }
        }
      ]
    }
  }
}
```

> 上面只列出了部分常用模型，完整列表请通过 `/v1/models` 端点获取后自行添加。

### 3. 其他客户端

支持 OpenAI 兼容 API 的客户端均可接入：

```
Base URL: http://127.0.0.1:7863/v1
API Key:  WildWorkAPI
```

模型 ID 需带渠道前缀：`workbuddy/<model>`、`workbuddyai/<model>`、`traework/<model>`、`traecode/<model>`、`qodercn/<model>`、`qodercom/<model>`、`qwenwork/<model>`、`raccoon/<model>`、`loomy/<model>`、`oczen/<model>`（旧 `qoder/<model>` 仍可用，已从界面下线）。

#### OpenCodeZen 匿名免费模型（`oczen/*`）

无需任何账号或登录，直接可用（免费清单由上游动态下发，随时增删）：

```bash
curl http://127.0.0.1:7863/v1/chat/completions \
  -H "Authorization: Bearer WildWorkAPI" -H "Content-Type: application/json" \
  -d '{"model":"oczen/mimo-v2.6-flash-free","messages":[{"role":"user","content":"你好"}]}'
```

- 常见可用：`oczen/big-pickle`、`oczen/mimo-v2.6-flash-free`、`oczen/mimo-v2.5-free`、
  `oczen/nemotron-3-ultra-free`、`oczen/nemotron-3.5-lightning-free`、`oczen/ling-3.0-flash-fin-free`；
  完整清单见 `/v1/models` 中 `oczen/` 开头的条目。
- 部分免费模型有**地域限制**（如 `muse-spark-*-contributor-free` 在国内直连返回 403 RegionError）：
  列表仍会列出，自备代理即可使用。
- 免费额度/限流由上游 OpenCode Zen 控制，本工具不做额外限制；上游未见请求数配额，
  但高并发时延会上升。

### 4. 三种接口协议

除 OpenAI Chat Completions 外，同一端口还兼容 **OpenAI Responses** 与 **Anthropic Messages**，
可用官方 CLI 直接接入（无需改代码）：

| 端点 | 协议 | 适用客户端 |
|------|------|-----------|
| `POST /v1/chat/completions` | OpenAI Chat | 大多数第三方客户端、`openai` SDK |
| `POST /v1/responses` | OpenAI Responses | `codex` CLI、`openai` SDK 的 Responses API |
| `POST /v1/messages` | Anthropic Messages | `claude` CLI、`anthropic` SDK |
| `POST /v1/messages/count_tokens` | Anthropic count_tokens | Claude Code 上下文预算用 |

鉴权：OpenAI 侧用 `Authorization: Bearer <API Key>`；Anthropic 侧 `x-api-key: <API Key>`
或 `Authorization: Bearer` 均可。

**模型名可省略渠道前缀**，在 `config.json` 的 `compat` 段配置映射：

```json
"compat": {
  "default_channel": "workbuddy",
  "max_tokens_cap": 32000,
  "model_map": {
    "claude-*": "workbuddy/glm-5.2",
    "gpt-5*": "workbuddy/deepseek-v4-pro"
  }
}
```

- 带 `channel/` 前缀的模型名始终优先（如 `workbuddy/hy3`），行为与旧版一致
- 无前缀时依次尝试：`model_map` 精确匹配 → 通配匹配（key 以 `*` 结尾）→ `default_channel`
- `max_tokens_cap` 用于封顶客户端的 `max_tokens`（Anthropic 客户端常发 64000，
  超出部分上游会直接 400）；设 `0` 表示不限制

#### 思考强度（reasoning effort）

客户端可用标准写法指定思考档位，网关归一化后按渠道投影到上游支持的形态：

| 写法 | 来源 |
|------|------|
| `reasoning_effort: "high"` | Chat Completions 顶层（也接受 `reasoningEffort`） |
| `reasoning: {"effort": "high"}` | Responses 标准写法（同时出现时嵌套优先于顶层） |
| `thinking: {"type": "enabled", "budget_tokens": 4096}` | Anthropic / Claude 风格 |
| `output_config: {"effort": "high"}` · `enable_thinking: true` · `disable_reasoning: true` · `think: true` | 其他常见客户端写法 |

- 档位取值：`none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`ultra`；`off` 等同于 `none`。
  取值非法或同一对象内自相矛盾（如 `reasoning:{effort:"high",enabled:false}`）返回 `400 invalid_reasoning_control`。
- **档位按模型能力自动降级**：上游各模型只接受特定档位（如国内版 `deepseek-v4-pro` 只认
  `low/high/xhigh`、`glm-5.1` 只认 `medium`，国际版 `deepseek-v4.1-flash` 只认 `high`）。
  网关按「不超过请求强度的最高支持档」就近降级（支持档全部高于请求档时取最低支持档），
  未收录的模型档位原样透传。能力表优先取上游目录接口返回的 `reasoning.supportedEfforts`
  /`defaultEffort`，缺失时回落内置静态表（见 `internal/reasoning/catalog.go`）。
- 渠道能力：WorkBuddy 国内版/国际版按上述能力表投影；**Qoder 按官方客户端的三处同源写法投影**
  `model_config.is_reasoning` + `parameters.reasoning_effort` + `parameters.enable_thinking`，
  档位来自上游模型目录的 `thinking_config`（每模型一条 ladder，与 WorkBuddy 分表）；
  客户端要求关闭但该模型没有 `disabled` 节点时降到最低档；TraeWork 协议没有该字段。
  ✅ Qoder 的 `agent_chat_generation` 端点**会下发可见思考链**（`reasoning_content` +
  `usage.reasoning_tokens`）—— 前提是请求体与请求头按桌面版实测形状对齐（见 AGENTS.md R21）；
  早期实现少了顶层 `system`、`task_id`、完整 `model_config` 等字段，才误判成「上游不支持」。
  可见性在三个接口一致：Chat 原样透传 `reasoning_content`、Anthropic 转 `thinking` 块、
  Responses 转 `reasoning` item（Responses 侧另受下方「思考摘要」策略控制，默认 `auto`）。
  ⚠️ 请求体形状改动后务必跑 `TestLiveProbeProductionPath`（`-tags live`）回归。
- **DeepSeek 系思考开关**：官方客户端开思考需同时下发 `thinking:{"type":"enabled"}` 与档位，
  缺该字段上游按「不思考」应答（`reasoning_content` 为空）；网关在客户端要开思考时自动补上，
  并给 assistant 消息回填 `reasoning_content`（多轮一致性）。由 `compat.deepseek_thinking`
  （或 `WILDWORK_DEEPSEEK_THINKING=0`，面板「DeepSeek 开关」同款）控制，默认开启。
- 默认档：`compat.reasoning_effort`（或环境变量 `WILDWORK_REASONING_EFFORT`，面板「思考强度」同款）
  在客户端**未指定**时注入，对 WorkBuddy 双面与 Qoder 生效（Qoder 的具体档位按模型 ladder 降级）；
  留空表示不注入。
  客户端显式指定（含显式关闭）时一律以客户端为准，默认档不会把它复活。
  客户端只说「开思考」没给档位时，用该模型声明的默认档（缺失才回退 `high`）。
- `thinking.budget_tokens` 会按预算换算档位：`>= 4096` → `high`，`< 1024` → `low`，
  其余 → `medium`（上游协议没有预算字段，只能折算成档位）。

```json
"compat": {
  "reasoning_effort": "high",
  "deepseek_thinking": true
}
```

#### 思考摘要（Responses 接口）

`/v1/responses` 会把上游的 `reasoning_content` 转成 Responses 的 `reasoning` output item
（`response.reasoning_summary_part.added` / `reasoning_summary_text.delta|done` 事件族），
Codex 据此显示思考摘要。由 `compat.responses_reasoning_summary`（或环境变量
`WILDWORK_RESPONSES_REASONING_SUMMARY`，面板「思考摘要」同款）控制：

| 取值 | 行为 |
|------|------|
| `auto`（默认） | 仅当客户端显式索要摘要时才下发（`reasoning.summary` 非 `none`，或 `include` 含 `reasoning.encrypted_content`，或带 `thinking` 对象）。Codex 发 `reasoning:{summary:"auto"}`，故默认即可看到思考 |
| `on` | 只要上游给了思考链就下发（客户端没要也发） |
| `off` | 从不下发，丢弃思考链（旧版行为） |

- 思考 item 排在 output 首位（`output_index=0`），message / function_call 顺延，与官方顺序一致。
- 思考增量只在文本开始前接受；文本开始后到达的思考片段会被丢弃，避免 item 顺序倒序。
- Anthropic（`thinking` 块）与 Chat（`reasoning_content`）两个接口一直都会下发，不受该配置影响。

```json
"compat": {
  "responses_reasoning_summary": "auto"
}
```

**Codex CLI 配置示例**（`~/.codex/config.toml`）：

```toml
model = "traework/glm-5.2"
model_provider = "wildwork"

[model_providers.wildwork]
name = "wildwork"
base_url = "http://127.0.0.1:7863/v1"
wire_api = "responses"
env_key = "WILDWORK_KEY"
```

**Claude Code 配置示例**（环境变量）：

```bash
export ANTHROPIC_BASE_URL="http://127.0.0.1:7863"
export ANTHROPIC_AUTH_TOKEN="WildWorkAPI"
export ANTHROPIC_MODEL="traework/glm-5.2"
```

> ⚠️ **已知限制**：Responses 接口是无状态实现，不支持 `previous_response_id`
> （会返回 400 并提示）。客户端需在 `input` 中携带完整历史（Codex CLI 默认如此）。
> 另外 WorkBuddy 国内版/国际版上游对某些 agent 系统提示词（如 Codex CLI 自带的那份）
> 会触发内容策略拦截（上游返回 `code=11128 Illegal API invocation from an unapproved channel`），
> 实测 `traework/*`、`qoder/*`、部分 `workbuddy/*` 模型不受影响；如遇拦截请更换渠道模型。

### 5. Jev 结构化决策端点（`/v1/systemone`）

OpenCode Zen 上还有一个免费的结构化决策模型 **Jev（`jev-1.13-free`）**。
它不是聊天模型——不能调 `/v1/chat/completions`（会 500），只能通过专用端点调用。
本网关在 `POST /v1/systemone` 直接透传，用法：

```bash
curl -X POST "http://127.0.0.1:7863/v1/systemone" \
  -H "Authorization: Bearer WildWorkAPI" \
  -H "Content-Type: application/json" \
  -d '{
    "state": "用户反馈：我被重复扣费了，订单号 A-104，要求今天退款。",
    "questions": {
      "refund":  {"type": "noul",   "criteria": {"true": "要求退款", "false": "未提及退款"}},
      "team":    {"type": "choice", "criteria": {"billing": "支付/发票/退款", "technical": "Bug/故障", "other": "以上都不是"}},
      "urgency": {"type": "score",  "criteria": ["低（不紧急）", "中（今天内）", "高（立即处理）"]}
    }
  }'
```

#### 三种问题类型

| 类型 | 问什么 | 返回 | 类比代码 |
|------|--------|------|----------|
| **noul** | 这是不是 X？ | 0~1 的概率 | `if (isX(state))` |
| **choice** | 这是哪个？ | 每个选项的概率 + 最佳选择 | `switch (classify(state))` |
| **score** | 这几分？ | 0~(N-1) 等级分 + 概率分布 | `scoring(state)` |

#### 响应示例

```json
{
  "model": "jev-1.13-free",
  "answers": {
    "refund":  {"type": "noul",   "noul": 0.92},
    "team":    {"type": "choice", "choice": "billing", "confidence": 1, "probabilities": {"billing":1,"technical":0,"other":0}},
    "urgency": {"type": "score",  "score": 1.83, "confidence": 0.74,
                "legend": {"0":"低","1":"中","2":"高"}, "probabilities": {"0":0,"1":0.17,"2":0.83}}
  },
  "usage": {"input_tokens": 444, "output_tokens": 69},
  "cost": "0"
}
```

#### 关键限制

- **必须带 `criteria` 字段**——`noul` 用 dict `{"true":"…","false":"…"}`，`choice` 用 dict（每个选项的判断标准），`score` 用 list（与级别对齐的描述）
- `state` 文本约 32k token 预算（state + questions 共享），英文最强，数学/日期/十六进制请交给代码
- `choice` 最多 255 个选项，`score` 2~10 级
- **免费**：`cost:"0"`，实测 0.5~1.3s 完成决策
- 也可直连上游：`POST https://opencode.ai/zen/v1/systemone` + `Authorization: Bearer public` + OpenCode 伪装头，但本网关已帮你处理了鉴权与伪装

> **别拿它当 GPT 用**——把它当成"语义判断 API"：给它状态和选择题，它回概率和置信度，你的代码根据 typed 值做后续决策。

## 面板操作指南

所有操作在 Web 管理面板中完成，按直觉操作即可：

- **添加账号**：点击渠道按钮 → 确认对话框 → 浏览器窗口登录 → 自动完成
- **账号管理**：卡片显示积分、签到状态（WorkBuddy 国际版显示「自动领日活奖励」）；图标按钮操作（签到 ✓ / 刷新 ↻ / 停用 ⏸ / 删除 ✕）
  - **修改显示名**：点击卡片上的账号名即可修改，用于给账号起好认的别名
  - 账号按固定渠道序展示：OpenCodeZen → WorkBuddyCN → WorkBuddyAI → QoderCN → QoderCOM → TraeWork → 千问办公
  - 积分显示为「可用积分」；若该账号还有本工具用不了的额度（如 TraeWork 官方客户端专用池），会追加显示 `/ N 不可用`
- **积分明细**：鼠标悬停积分数字显示套餐明细（含有效期与可用/不可用小计），条目多时用底部 `‹ ›` 翻页
- **刷新积分**：面板顶部按钮，批量刷新全部账号余额
- **模型列表和费率**：点击「刷新」从上游拉取最新模型定价；上游未返回定价的模型显示 `unknown`，免费模型高亮为 `Free`。
  模型 ID 后的图标标记图像输入 / 思考 / 工具调用能力；悬停模型名可看上下文窗口、能力与**该模型的思考档位**（与 `/v1/models` 同源）；
  Qoder 系模型可直接在费率表里选择**上下文窗口档位**（上游声明的可选档位，选定值随请求下发）
- **设置（统一配置入口）**：点击顶栏右侧 ⚙ 齿轮按钮，包含：
  - *接口与鉴权*：API 监听地址（`127.0.0.1` / `0.0.0.0` / 自定义 + 端口）、API-Key
  - *自动签到与开机自启*：签到时间多组增删、开机自启开关
  - *模型路由*：默认渠道、封顶 tokens、CC/Codex 客户端模型名映射表（`claude-* = workbuddy/glm-5.2` 形式，支持通配）
  - *渠道上游代理*：给单个渠道的上游请求单独配置代理（`http://` / `https://` / `socks5://`），留空直连；
    典型用途：OpenCodeZen 的区域限制模型需经代理访问。还可为 OpenCodeZen 填入自定义 API key（`sk-…`，
    留空用匿名凭证 `public`；自定义 key 有独立配额不受匿名限流影响，且可调用付费模型——需账户余额）

> 顶栏的 API 地址 / API-Key 文本框点击复制；所有配置修改统一走 ⚙ 设置弹层。

## 渠道能力对照

| 渠道 | 模型前缀 | 思考档位 | 积分/余额 | 费率 | 自动签到 | 凭据续期 |
|---|---|---|---|---|---|---|
| WorkBuddy 国内版 | `workbuddy/*` | ✓ | ✓ | ✓ | 09:00 / 21:00 | refresh |
| WorkBuddy 国际版 | `workbuddyai/*` | ✓ | ✓ | ✓ | 内部保活（界面无感） | refresh（≈365 天） |
| TraeWork | `traework/*` | — | ✓ | ✓ | 09:00 / 21:00 | refresh（≈14 天） |
| QoderCN | `qodercn/*` | ✓ | ✓ | ✓ | 10:15 | refresh（≈30 天） |
| QoderCOM | `qodercom/*` | ✓ | ✓ | ✓ | 10:15 | refresh（≈30 天） |
| 千问办公 | `qwenwork/*` | — | ✓ | ✓ | 关闭（避免与官方 App 互抢） | refresh |
| 商汤小浣熊 | `raccoon/*` | — | ✓（四池） | ✓ | — | refresh（access ≈2h，**单会话**） |
| Loomy（讯飞） | `loomy/*` | ✓（全档位） | ✓ | ✓ | — | **无 refresh**（≈14 天需重新导入） |
| 旧 Qoder | `qoder/*` | ✓ | ✓ | ✓ | — | refresh（≈30 天） |

> - **思考档位** = 支持在请求里指定推理强度（`reasoning_effort`），并在 `/v1/models` 与面板费率表里列出可选档位。
> - 模型级的「图像输入 / 思考 / 工具调用」能力标记见上一节；鼠标悬停模型名可看档位详情。
> - 商汤小浣熊的面板按钮走**协议登录**：登录期间把 `office-raccoon://` 协议临时指向本工具，
>   截获网页授权码后自行兑换 token，结束立即恢复注册表。**登录期间请勿启动小浣熊客户端**
>   （它会重新注册协议，把劫持覆盖掉）；弹窗里的「从客户端导入」则复用本机已登录客户端的凭据。
>   Loomy 为**导入型**渠道，只有导入一条路。
> - **导入或协议登录后请退出对应客户端** —— 小浣熊的 refresh_token 是单会话的，两边同时刷新会互相作废。
>   （协议细节与取证见 `docs/raccoon渠道接入备忘.md` §11。）

## 常见问题

### 1. Windows 提示“未知发布者”或报毒？

本工具没有数字签名，因此可能会被 Windows 安全中心或杀毒软件拦截。如果报毒，请将本工具添加到排除项，方法如下：

>设置 → 更新和安全 → Windows 安全中心 → 打开 Windows 安全中心 → 病毒和威胁防护 → “病毒和威胁防护”设置/管理设置 → 排除项/添加或删除排除项 → 添加排除项（文件或文件夹），把 exe 或所在目录加进去。

如果对本仓库发布包不放心，可以克隆到本地让 Agent 帮你审查一遍，然后自行基于源码构建（见 [DEVELOPMENT.md](DEVELOPMENT.md)）。

### 2. TraeWork 积分里的「不可用」是什么？

TraeWork 的额度分两个池，由上游 `available_endpoint` 字段区分：

- `ep=0`（通用池）：本工具能消耗，显示为**可用积分**
- `ep=1`（官方客户端专用池）：只有 Trae 官方客户端能用，本工具消耗不了，显示为**不可用**

所以 TraeWork 卡片会出现类似 `1828可用积分/4400不可用` 的显示——后者再多也帮不上 API 转发。
本工具的可消耗余额只算 `ep=0`，不会因为专用池额度高而误判账号可用。

> 注：早期版本把所有额度混成一个数字（且用 `group_type` 误判可用性），会让 TraeWork 账号看起来余额充足；
> v2.2.1 起修正为按 `available_endpoint` 拆分统计。

### 3. TraeWork DeepSeek V4 Flash 模型响应慢

在官方客户端里这个模型也会排队，我的办法是 `ds4f` 用 WorkBuddy 的，`ds4p` 用 TraeWork 的。

### 4. 如何绑定多个 WorkBuddy 国际版账号？

添加新账号时，因为默认使用当前已登录的 github/Google 等账号自动登录，因此无法快速添加新的账号。
这里给出我的方案，在浏览器右上角菜单选择“新建无痕窗口”，在窗口地址栏输入`http://127.0.0.1:7863`打开 WildWork 主界面，即可在干净环境内新增账号。
也可以打开开发者控制台，单独清除 github.com 和 workbuddy.ai 的 Cookies。

### 5. WorkBuddy 国际版登录后积分显示为0，刷新积分报错
注册 WorkBuddy 国际版新账号时，需要选择地区后奖励积分才发放。

## ⚠️ 风险警告与免责声明

**在使用本工具前，请务必阅读并理解以下内容。下载、运行或继续使用本工具即表示你已阅读、理解并同意承担全部风险与责任。**

### 风险警告

1. **逆向工程风险**：本工具基于对上游平台（WorkBuddy/CodeBuddy、TraeWork、Qoder、千问办公、OpenCode 等）**未公开接口的逆向工程**实现，非官方支持、无任何授权。上游随时可能调整接口、加密方式或风控策略，导致本工具**部分或全部功能立即失效**，作者不承诺修复时效。
2. **封号风险**：使用本工具的请求特征与官方客户端存在差异，**可能违反上游平台的服务条款**，存在账号被**限制功能、降低额度、暂时或永久封禁**的风险。请自行评估后果，**强烈建议使用小号或可承受损失的账号接入，切勿将主力账号用于本工具**。
3. **网络暴露风险**：面板监听 `0.0.0.0` 时，局域网内任何设备均可访问你的 API 与账号管理界面（含登录入口），请确认网络环境可信并自行承担暴露风险。
4. **数据安全风险**：账号凭证（access/refresh token）以明文 JSON 保存在本地 `auths/` 目录，请妥善保管该目录，避免泄露。

### 免责声明

1. 本工具仅供**个人学习、技术研究与测试**用途，严禁用于商业用途、大规模滥用或任何违反法律法规及上游平台服务条款的场景。
2. 本软件按"**现状**"（AS IS）提供，**不提供任何明示或默示的保证**。作者不对使用本软件造成的任何直接或间接损失承担责任，包括但不限于：账号被封禁、积分/额度损失、数据丢失、商业纠纷、法律纠纷等。
3. 使用者应**自行承担**因使用本工具产生的一切风险与后果；若不同意上述条款，请立即停止使用并删除本软件。
4. 本工具与上游各平台及其运营方**无任何关联与合作**；相关商标与品牌权利归其各自所有者所有。
5. 如本工具的使用对上游平台造成影响或平台方提出异议，作者将配合处理；本项目的存在不代表对任何规避行为的价值判断。

## 交流群

微信扫码加入「野活儿老白蹬之家」，有问题欢迎在群里反馈：

<img src="wechat_group.jpg" alt="野活儿老白蹬之家 微信群二维码" width="320">

## License

MIT — 仅供个人学习使用，请遵守各上游平台服务条款。
