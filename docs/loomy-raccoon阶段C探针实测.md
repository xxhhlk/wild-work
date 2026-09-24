# Loomy / 小浣熊 阶段 C 探针实测记录

> 状态：**阶段 C 完成**（2026-09-22 22:20）
> 工具：`_probe/c1-probe.mjs`（流式 / 错误形态 / 关思考矩阵）、`_probe/import-auth.mjs`（凭据导入器）
> 全部在**客户端所在机器**（DESKTOP-K8UJEIA / testuser）执行；**token 全程未打印、未落日志**。
> 消耗：两渠道合计约 20 次小请求（`points_consumed` 每次 1，或 <100 tokens）。
>
> ⚠️ **阅读前必看**：本文档的**档位（关思考/三件套）相关结论已作废**，
> 2026-09-24 以 `usage.completion_tokens_details.reasoning_tokens` 为权威指标重测后推翻；
> 端点 / 错误形态 / 流式形状部分仍然有效。更正对照表见文末「⚠️ 后续更正（2026-09-24，`baaff96`）」。

---

## 1. 验收判据达成情况

| 判据 | 小浣熊 | Loomy |
|---|---|---|
| 流式增量真有非空正文（非缓冲后一次性吐） | ✅ `firstContentEvent=1`（第 2 个事件即有正文），13 事件逐块到达 | ✅ `firstContentEvent=21`（前 20 个事件是思考，符合"思考先于正文"），32 事件 |
| 模型名与目录一致 | ✅ 请求 `raccoon-8c4485` → 响应 `model: raccoon-8c4485` | ✅ 请求 `deepseek-v4-flash-0731` → 响应同名 |
| 探针自证实际发出的 body | ✅ 打印请求体（脱敏） | ✅ 同 |
| 错误形态覆盖 | ✅ 401 / 400 / 静默回落 | ✅ HTTP200+业务码 / 400 |
| 凭据导入器产出标准 auth 文件 | ✅ `auths/raccoon-<昵称>.json` | ✅ `auths/loomy-<userid>.json` |

---

## 2. 流式实测

### 小浣熊
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

### Loomy
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

---

## 3. 错误形态矩阵（**阶段 D 的 Classify 依据**）

### 小浣熊（信封 = LiteLLM 风格）

| 场景 | HTTP | 响应 | 建议映射 |
|---|---|---|---|
| 无效 token | **401** | `{"code":200003,"message":"authorization_verify_error","details":"authorization verify failed"}` | `ErrSessionDead`（触发 refresh，refresh 失败才禁用） |
| 无 token | **401** | `{"code":200001,"message":"authorization_empty_error"}` | `ErrSessionDead` |
| **未知模型名** | **200** | 正常响应，`model` 回填为**默认模型** `raccoon-8c4485` | ⚠️ **上游静默回落到默认模型** → wild-work **必须本地严格校验模型名**，否则会静默消耗默认模型额度 |
| 空 messages | **400** | `{"error":{"code":"400","message":"litellm.BadRequestError: Custom_raccoonException - do not support message is empty or message is bigger than 70MB for model raccoon/raccoon-work (request_id:…)"}}` | `ErrBadParams`（请求级，不罚号） |

### Loomy（信封 = `{error:{...}}`，鉴权错误走 **HTTP 200 + 业务码**）

| 场景 | HTTP | 响应 | 建议映射 |
|---|---|---|---|
| **无效 token** | **200** | `{"code":"100002","desc":"登录已失效，请重新登录","trace_id":"…","data":{}}` | ⚠️ **必须先查业务码再查 HTTP 状态** → `ErrSessionDead` |
| **无 token** | **200** | `{"code":"100002","desc":"缺少 token"}` | 同上 |
| **未知模型名** | **200** | 正常响应，`model` 回填为**默认模型** `deepseek-v4-flash-0731` | ⚠️ 同样**静默回落** → 必须本地校验模型名 |
| 空 messages | **400** | `{"error":{"message":"messages 不能为空","type":"invalid_request_error","param":"messages","code":400,"metadata":{"provider_name":"loomy"}}}` | `ErrBadParams` |

> 与项目既有的 `provider.CodeMarker` 约定一致：**业务码判定必须排在 `status==404` / `status>=500` 之前**
> （AGENTS §6 第 25 条）。Loomy 的 `100002` 已在静态取证中确认属于 `AUTH_ERROR_CODES = {020002, 100002}`。

---

## 4. Loomy「关闭思考」实测矩阵（重要）

同一模型 `deepseek-v4-flash-0731`、同一 prompt，仅改思考字段：

| 变体 | reasoning 字符 | reasoning_tokens | points |
|---|---|---|---|
| `reasoning_effort:"none"` | 98 | 31 | 1 |
| `+ enable_thinking:false` | 81 | 28 | 1 |
| `+ enable_thinking:false + chat_template_kwargs:{enable_thinking:false}` | **59** | **16** | 1 |
| 对照：`reasoning_effort:"low"` | 105 | 45 | 1 |

**结论**：
1. **该模型是"强制思考"型 —— 没有任何字段组合能完全关掉思考**（最低仍 59 字 / 16 reasoning tokens）。
2. 但三件套有**单调递减效果**（98 → 81 → 59），说明字段**确实被部分采纳**。
3. `points_consumed` 恒为 1 → **计费与档位/思考量无关**（疑按次计）。
4. 实现策略：客户端要求关闭时，**三件套一起发**（`reasoning_effort:"none"` + `enable_thinking:false` +
   `chat_template_kwargs.enable_thinking=false`）并在文档/面板注明「最低档仍可能产生少量思考」（上游行为，非本工具缺口）。

> 与小浣熊对比：`reasoning: 0`（默认不产生思考），且有 `completion_tokens_details.reasoning_tokens` 字段位。

---

## 5. 凭据导入器产物（`_probe/import-auth.mjs`）

输出到 `wild-work/auths/`（**该目录已在 `.gitignore` 第 10 行忽略**，不会入库）。

| 渠道 | 文件名 | auth 字段 | account 字段 |
|---|---|---|---|
| raccoon | `raccoon-<昵称>.json` | `accessToken`(380) / `refreshToken`(380) / `expiresAt`(JWT exp) / `domain=/api/web/llm/v2` / `apiHost=https://xiaohuanxiong.com` | `uid=<昵称>` / `enterpriseId=<office_identity>` / `nickname` |
| loomy | `loomy-<userid>.json` | `accessToken`(32) / `refreshToken`(空) / `expiresAt=updatedAt+14d` / `domain=/api/v1` / `apiHost=https://loomyad.xunfei.cn` | `uid=<userid>` / `enterpriseId=""` / `nickname=""` |

- 格式与 `internal/auth.Parse` 的嵌套分支**逐字段对齐**（`accessToken/refreshToken/expiresAt/domain/apiHost/machineId/deviceId/machineToken/machineType`）。
- 写入用 `tmp + rename` 原子替换，`0600`；**默认不覆盖已存在文件**（需 `--force`）。
- Loomy 无 refresh → `expiresAt` 取 `updatedAt + 14d`（客户端登录时请求的 expire），
  避免 `NeedsRefreshLocked` 因 `ExpiresAt<=0` 恒真而触发**永远失败**的刷新；到期后走 `session_dead` 提示重登。
- ⚠️ 小浣熊的 `expiresAt` = access_token 的 JWT `exp`（≈2h）→ **实现必须带 refresh**，否则账号 2 小时后即失效。

---

## 6. 给阶段 D 的输入清单

| 项 | 结论 |
|---|---|
| 小浣熊 Classify | 401 + `200001/200003` → `ErrSessionDead`；400 + `litellm.BadRequestError` → `ErrBadParams`；模型名校验**必须本地做** |
| Loomy Classify | **HTTP 200 + `code:"100002"` → `ErrSessionDead`**（业务码优先于状态码）；400 `messages 不能为空` → `ErrBadParams`；模型名校验**必须本地做** |
| 小浣熊 refresh | `POST https://xiaohuanxiong.com/api/electron/auth/v1/refresh`，body `{"refresh_token":…}`，resp `{data:{access_token,refresh_token}}`；**轮换必须落盘**；提前 300s 触发 |
| Loomy 续期 | 无 refresh → `RefreshToken` 返回明确错误（触发 `ErrSessionDead`），面板提示重新登录 |
| 流式解析 | 两渠道同为 `choices[0].delta`，`reasoning_content` 与 `content` 分流；`usage` 在末块；Loomy 另有 `points_consumed` |
| 档位投影 | Loomy：**要投影**（上游目录有全档位），并新建 `RealmLoomy`；关闭档用三件套。小浣熊：**不投影** |
| 模型校验 | 两渠道都会**静默回落默认模型** → `runtimeForModel` 之后需按渠道模型表校验，未知模型直接 400（不转发） |

---

## 7. 剩余未决（留到阶段 D/E 验证）

1. 小浣熊 `sn-*` 模型与 `visible:false` 内部模型是否都能正常调用（本次只测了默认模型）
2. 小浣熊 refresh 的**真实调用**（会轮换 refresh_token，需在自家账号池内做，不能污染客户端）
3. Loomy 的 `reasoning_effort` **对非 deepseek 模型**（MiniMax/Kimi/Qwen/GLM）是否同样可关
4. 两渠道的 **429 / 限流形态**（本次未触发）
5. 工具的 `tool_calls` 流式形状（`function_calling: true` 已声明，未实测）

---

## 8. 产物格式兼容校验（已通过）

用 `internal/auth.Parse` 实测解析导入产物（临时程序 `.gotmp/parsecheck/main.go`，已 gitignore）：

```
OK   raccoon-<昵称>.json         uid="<昵称>"  accessLen=380 refreshLen=380 expiresAt=1790089841
                                apiHost=https://xiaohuanxiong.com  domain=/api/web/llm/v2  needRefresh(5m)=false
OK   loomy-<userid>.json            uid="<userid>" accessLen=32 refreshLen=0 expiresAt=1791277650
                                apiHost=https://loomyad.xunfei.cn  domain=/api/v1  needRefresh(5m)=false
OK   qwenwork-<既有账号>.json    （对照：解析正常，needRefresh(5m)=true）
```

→ **格式与既有渠道完全兼容**，阶段 D 直接加 `Load<Channel>Dir` 即可加载。


---

## ⚠️ 后续更正（2026-09-24，`baaff96`）

本文档的**档位相关结论已作废**，其余（端点/错误形态/流式形状）仍然有效。

| 原文结论 | 更正后 |
|---|---|
| 「关闭思考三件套递减最优，但最低仍产生 59 字思考」 | 单次采样 + 用**思考字符数**当指标，均不可靠。以 `usage.completion_tokens_details.reasoning_tokens` 重测：Loomy 三档在 `deepseek-v4-flash-0731` 上**单调**（low 均 173 / medium 均 297 / high 均 459 rtok）；`max_tokens` 不足会截断思考，进一步掩盖差异 |
| 「小浣熊：**不投影**」 | 更准确的表述：**不接档位且主动剥离** `reasoning_effort` —— 网关认该字段，但实测**默认档最深**，下发任何档位反而削弱约 90%（详见 `raccoon渠道接入备忘.md` §13） |
| 「Loomy：要投影（上游目录有全档位）」 | 正确，但**还须按模型 ladder `Clamp`**（原实现漏了，`baaff96` 已补），否则超限档位（`max`/`ultra`）被原样下发 |
