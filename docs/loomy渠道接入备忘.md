# Loomy 渠道接入备忘（阶段 A 静态取证）

> 状态：**阶段 A 静态取证完成，待运行时取证（A3）**
> 取证日期：2026-09-22 ｜ 客户端：`C:\Program Files\Loomy`（Electron，**讯飞 Loomy**）
> 依据技能：`electron-ai-client-protocol-extract`（Electron 客户端取契约）
> **本文档不含任何 token / AK / SK 值**，只记录端点、字段名与算法。

---

## 1. 一句话结论

Loomy 是**讯飞**的 AI 办公客户端，模型走**自有网关 `loomyad.xunfei.cn`（OpenAI 兼容）**，
鉴权用**登录 session**（`Authorization: Bearer` + `token` 双写），
登录走**讯飞账号体系 `account.xfinfr.com`**（HMAC-SHA1 签名 + 手机验证码/密码/微信）。
→ **端点与登录均可独立复现**，属计划里的**形态 A（正式登录包）**候选。

---

## 2. 安装位置与可读性

| 项 | 值 |
|---|---|
| 安装目录 | `C:\Program Files\Loomy` |
| 主程序 | `Loomy.exe` |
| **明文源码** | `resources\app.asar.unpacked\electron\`（**6.45 MB / 773 文件，未混淆，按功能分目录**） |
| 混淆配置 | `resources\.env.prod` = `LOOMYENC1:` 前缀的 AES-256-GCM 密文 |
| userData | `%APPDATA%\Loomy`（`nexus-debug.log`、`Local State`、`app-ui-state.json`） |
| 内置运行时 | `opencode` / `node-runtime` / `python-archive` / `uv` / `playwright-mcp-runtime` |
| IM 集成 | `lark-cli` / `larksuite-cli` / `dingtalk-workspace-cli` / `wecom` |

### 2.1 `.env.prod` 的"加密"是可解的（**不是障碍**）

`electron\utils\env-file-crypto.js` 把算法与**内嵌口令**一并写死在源码里（文件注释亦自承
"本质是混淆而非真正的密钥保密"）：

```
magic  = "LOOMYENC1:"
payload = base64( salt(16) | iv(12) | tag(16) | ciphertext )
key    = scryptSync(PASSPHRASE, salt, 32)      // AES-256-GCM，口令硬编码在源码内
```

**取证做法（本次采用）**：不抄实现，直接 `import` 客户端自己的 `env-file-crypto.js` 解 `.env.prod`，
解出后**只落端点/键名，值一律脱敏**（脚本：`_loomy_probe/decrypt-env.mjs`）。

---

## 3. 端点表（解 `.env.prod` 实测所得）

| 用途 | 值 |
|---|---|
| **积分/iModel 网关** | `https://loomyad.xunfei.cn` |
| **推理 baseURL** | `https://loomyad.xunfei.cn/api/v1`（由 `pointsBaseUrl` + `/api/v1` 派生） |
| 分享 API | `https://loomyad.xunfei.cn/api/v1` |
| Buddy 目录 | `https://loomyad.xunfei.cn` |
| 更新检查 / 崩溃日志 | `https://loomyad.xunfei.cn` |
| **账号服务** | `https://account.xfinfr.com`（dev 回落 `accounttest.xfinfr.com`） |
| 交易服务 | `https://trade.xfinfr.com` |
| Athena 会话 | `https://api-athena.xfinfr.com` ｜ `https://h5.athena.com` |
| **Nexus 长连** | `wss://dispatch-nexus.xfinfr.com/ws/loomy` ｜ 网关 `https://gateway-nexus.xfinfr.com` |
| 官网 / 微信回调 | `https://loomy.xunfei.cn`（`/oauth/wechat/callback`） |

> iModel baseURL 的派生逻辑在 `electron\bundled-resources.js:_applyManagedProviderRuntimeConfig()`：
> `baseURL = pointsBaseUrl + '/api/v1'`，并强制 `useSessionAuth: true`。

---

## 4. 推理请求形状（`llm/llm-completion.js` 实测）

```
POST {baseURL}/chat/completions          # = https://loomyad.xunfei.cn/api/v1/chat/completions
Accept: application/json
Content-Type: application/json
Authorization: Bearer <session>
token: <session>                         # session 模式「双写」
traceparent: 00-<32hex>-<16hex>-01       # ⚠️ 必需，缺失会挂死到超时（源码注释实测）
loomy-version: <app version>             # utils/app-version.js
# 有则带：ChatId / MsgId / TurnId
body: { model, messages, temperature, max_tokens, stream,
        enable_thinking:false, chat_template_kwargs:{ enable_thinking:false } }
```

- 模型列表：`GET {baseURL}/models`（OpenAI 兼容），只对 `useSessionAuth === true` 的 provider 拉取
- `traceparent` 生成规则：`00-` + 16 字节 hex + `-` + 8 字节 hex + `-01`（`utils/request-headers.js`）
- 模型标识形态：`provider/model`（`parseModelId`），例：`imodel/spark-x`

**已知模型 ID**：
| ID | 用途 |
|---|---|
| `imodel/spark-x` | 主对话模型（讯飞星火 X） |
| `imodel/doubao-seed-2.0-mini` | 轻量（知识库媒体摘要硬锁定） |
| `imodel-anthropic` | Anthropic companion provider（走 `/v1/messages`，与 OpenAI 不兼容） |

---

## 5. 登录与鉴权（`xfyun/account-service.js` + `xfyun/sign.js`）

### 5.1 签名算法（**全部明文，可直接移植 Go**）

讯飞标准 HMAC-SHA1 签名（类 AWS SigV1）：

```
stringToSign = METHOD \n ESCAPED_PATH \n ESCAPED_QUERY \n CONTENT_MD5 \n
               CONTENT_TYPE \n DATE \n NONCE \n SIGNED_HEADERS \n CANONICAL_HEADERS
signature    = base64( HMAC-SHA1( accessKeySecret, stringToSign ) )
Authorization: "<magic> <accessKeyId>:<signature>"     # magic 默认 "account"
配套头：Date(UTC)、Nonce(uuid)、Content-Type、Content-MD5
```

- AK/SK 来自 `.env.prod`（`VITE_XFYUN_ACCESS_KEY_ID/SECRET`，值为**应用级凭据、随客户端分发**）
- `canonicalizedHeaders` 只收 `x-` 前缀头，每行以 `\n` 结尾，参与签名前**去掉末尾 `\n`**

### 5.2 账号 API（base = `account.xfinfr.com`）

| 端点 | 用途 |
|---|---|
| `POST /login/phone/sendMsgCode` | 发短信验证码（`{base, param:{ccode,phone,expire:300}}`） |
| `POST /login/phone/checkCode` | **短信登录**（`param:{ccode,phone,mcode,msgid,expire}`）→ session |
| `POST /login/account/getPuKey` | 取 RSA 公钥（密码登录用） |
| `POST /login/account/byPwd` | 账号密码登录 |
| `POST /register/phone/submit` | 手机注册 |
| `POST /userinfo/query/baseInfo` | 用户信息（需 session） |
| `POST /login/account/logout` | 登出 |
| `POST /login/thirdAccount/*`、`/userinfo/thirdAccount/*` | 第三方（微信）绑定/解绑 |
| `GET/POST /userinfo/phone/sendMsgCode` | 给已绑定手机发码 |

响应信封：`{"code":"000000", ...}`（**成功码 `000000`**）；
**登录态失效码 = `020002` / `100002`**（`AUTH_ERROR_CODES`，源码常量）。

### 5.3 session 存储与有效期

- 客户端把 session 存进 electron-store：字段 `session` / `userid` / `phone` / `updatedAt`
  （`auth/auth-session-controller.js`），并同步给 opencode runtime
- **登录请求里 `expire = 14 * 24 * 3600`（14 天）** —— 有效期由客户端主动指定
- ⚠️ **全仓未找到 refresh / renew / keepalive / heartbeat 逻辑** → 判断为**无续期机制，到期需重登**

---

## 6. 积分 / 费率 / 每日任务（`points-service.js`）

base = `https://loomyad.xunfei.cn`，鉴权 = `Authorization: Bearer <session>`（部分接口另带 `token` 头）。

| 端点 | 用途 |
|---|---|
| `GET /api/v1/points/records` | 积分记录 v1 |
| `GET /api/v2/points/records` | 积分记录 v2（聚合形状：chatId 聚合 / points 累加 / first-lastCreatedAt） |
| `GET /api/v1/team-points/balance` | 团队积分余额（`currentBalance`） |
| `GET /api/v2/team-points/records` | 团队记录 v2 |
| **`GET /api/v1/pet-work`** + **`POST /api/v1/pet-work/rewards`** | **每日任务（≈签到）**：快照 + 领奖 |
| `POST /api/v1/points/first-login` | 首次登录奖励 |
| `GET/POST /api/v1/points/activation` | 激活状态 / 邀请码绑定 |
| `GET /api/v1/invitation-codes` | 邀请码 |
| `POST /api/v1/points/redemption-codes/redeem` | 兑换码 |
| `GET /api/v1/public/promo-banners` | 促销横幅（**无需鉴权**，`requestPublic`） |
| `POST /api/v1/asr/recognize` | 语音识别 |

> **余额口径（2026-09-23 实测修正）**：`points/records` 的 `data` 同时下发三个字段 ——
> `balance`（常规池：注册奖励 / 新手任务 / 充值，长期有效）、
> `dailyBalance`（每日池：按 `dailyCycleDate` 每日循环，**扣分优先消耗该池**，ledger 里 `consumeSource=daily`）、
> `availableBalance`（**= 前两者之和，真正可消耗的总额**）。
> 实测该账号 `balance=15000 / dailyBalance=4800 / availableBalance=19800` ——
> 只读 `balance` 会把每日积分整块漏掉（面板少显示、pool 路由口径偏低）。
> 故 wild-work 取 **v1 面**（v1 才有 `availableBalance`；v2 面是聊天聚合形状、无该字段）
> 并拆成「积分 / 每日积分」两条明细；若 `availableBalance` 缺失则按两池相加兜底。
> 每日积分的到期时刻上游未下发（只有 `dailyCycleDate`），故条目不填 `expire_at`。

> **签到判定**：`pet-work` 的「快照 + 领奖」是唯一形态的每日任务 → 若上游确实**服务端判定可领**，
> 签到可作为 `DailyCheckin` 实现；否则按计划 §2.3 豁免（`CheckinMinutes: nil` + `noExplicitCheckin`）。

---

## 7. 映射到 wild-work 的实现要点（草案）

| 项 | 方案 |
|---|---|
| Kind / 前缀 | `loomy`（`loomy/<model>`） |
| auth 文件 | `auths/loomy-<uid>.json`，`{auth:{...},account:{...}}`（R6.3） |
| 登录形态 | **形态 A**：`internal/login_loomy/` 实现 `NewClient/Start/Poll/SaveAuth`；`Poll` 走「发码→校验」两步，非浏览器回调 |
| 签名 | 移植 `sign.js` 到 Go（`crypto/hmac`+`sha1`+`md5`+`base64`），AK/SK 作为渠道常量（与 qoder 的 cosy 常量同性质） |
| 推理 | `POST {base}/chat/completions`，头：`Authorization`+`token`+`traceparent`+`loomy-version` |
| 模型表 | 动态 `GET /models`；静态兜底 `imodel/spark-x` 等 |
| 积分 | `UserResourceDetail` ← `/api/v1/points/records`（取 `availableBalance` = 常规池 + 每日池，拆两条明细） |
| 签到 | `pet-work` 快照+领奖；不确定则豁免 |
| 思考档位 | **投影**：客户端三档（low/medium/high）→ `reasoning_effort` + 三件套；按该模型 `reasoning_efforts` Clamp（见 §11） |

---

## 8. 未验证项（留待 A3 运行时取证）

1. `chat/completions` 的**真实响应形态**（SSE 字段、是否下发 `reasoning_content`、usage 结构）
2. session **实际有效期与失效行为**（14 天是绝对还是滑动？`020002` 何时触发）
3. `pet-work` 是否真为可每日领取的签到（服务端返回字段）
4. `imodel` 下**全部可用模型清单**与各自能力（图片/工具/上下文窗口）
5. `/models` 是否需要额外头（`loomy-version` 是否参与）
6. 上游条款对第三方客户端调用的态度（`model-bridge` 规则 1）

---

## 9. 取证依据（文件 → 结论）

| 文件 | 得到的结论 |
|---|---|
| `utils/env-file-crypto.js` | `.env.prod` 可解（AES-256-GCM + 内嵌口令） |
| `points-config.js` | `LOOMY_POINTS_BASE_URL` 来源与优先级链 |
| `bundled-resources.js` | iModel baseURL = pointsBaseUrl + `/api/v1`，`useSessionAuth: true` |
| `llm/llm-completion.js` | 推理端点、请求头（含 `traceparent` 必需）、body 形状 |
| `image/image-generation-service.js` | session 模式「token + Authorization 双写」 |
| `utils/request-headers.js` | `traceparent` 生成与校验规则 |
| `utils/app-version.js` | `loomy-version` 头 |
| `xfyun/sign.js` | HMAC-SHA1 签名算法全貌 |
| `xfyun/account-service.js` | 登录/账号 API 表、`AUTH_ERROR_CODES`、`expire=14d` |
| `auth/auth-session-controller.js` | session 落盘字段与生命周期（无 refresh） |
| `points-service.js` | 积分/每日任务/费率端点表 |
| `model-service.js` | `/models` 拉取条件与 fallback |

---

## 10. A3 运行时实测结果（2026-09-22 22:05，真实账号）

> 脚本：`_probe/a3-probe.mjs`（读本机凭据 → 发最小请求 → 输出脱敏）；原始响应落
> `_probe/out/loomy-models.json`。**token 全程未打印、未落库。**

**结论：端点 / 鉴权 / 模型目录 / 推理 / 档位 / 积分字段 全部实测通过。**

| 验证项 | 结果 |
|---|---|
| `GET /api/v1/models` | **200**，返回 8 个模型 |
| `POST /api/v1/chat/completions` | **200**，`deepseek-v4-flash-0731` 返回 `reasoning_content` + `usage.completion_tokens_details.reasoning_tokens=16` |
| 鉴权头 | `Authorization: Bearer <session>` + `token: <session>` + `traceparent` + `loomy-version`（服务端接受） |
| **积分字段** | 响应体带 **`usage.points_consumed`**（实测 = 1）→ 积分口径可直接取自响应 |
| **档位能力** | 上游目录下发 `reasoning_enabled: true`、`reasoning_catalog_version: sha256:…`、每模型 `reasoning_efforts:[none,low,medium,high,xhigh]`、`default_reasoning_effort: low` |
| 凭据位置 | `C:\Users\Public\Loomy\<sha256(user)[:12]>\userData\auth-session.json`（**在 `C:\Users\Public` 下 → 跨账户可读**） |
| 凭据字段 | `{ session(32), userid(18), phone(11), updatedAt }`，session **非 JWT**（无法本地读有效期） |
| loomy-version | `0.9.38`（读自 `app.asar.unpacked/package.json`） |

**实测模型表（8 个，全部 `protocol: openai_chat`，全部带全档位）**：

| id | 名称（含倍率） | ctx | max_out | vision |
|---|---|---|---|---|
| `deepseek-v4-flash-0731` | DeepSeek V4 Flash 0731（x3.0） | 1048576 | 384000 | ✗ |
| `MiniMax-M3` | MiniMax M3（x4.0） | 1048576 | 512000 | ✓ 图+视频 |
| `Kimi-k2.6` | Kimi k2.6（x6.5） | 262144 | 65536 | ✓ |
| `qwen-3.8-max` | Qwen 3.8 Max (x12.0) | 1000000 | 65536 | ✗ |
| `GLM-5.3-Flash` | GLM 5.3 Flash(x0.8) | 1048576 | 131072 | ✓ |
| `qwen3.8-flash` | qwen 3.8 flash（x0.8） | 1000000 | 131072 | ✓ |
| `spark-x` | Spark X2.5（x0.1） | 1048576 | 65536 | ✗ |
| `mimo-v2.5` | MiMo V2.5（x3.3） | 1048576 | 131072 | ✓ 图+音频+视频 |

> **倍率写在 `name` 文本里**（`（xN.N）`），无独立倍率字段 → 费率面板需解析名称或另找费率接口。
> `capabilities` 另含 `function_calling/streaming/system_message` 全 true。

**续期结论（维持，并已排除一处误判）**：`utils/remote-keepalive.js` 经查是 **电源保活**
（`powerSaveBlocker` + suspend/resume 重连 IM 通道），**与登录 session 无关**；
全仓仍无 session refresh/renew → **14 天后需重新登录**。

### 对渠道实现的影响（由此实测确定）

1. **必须新建 realm**：`RealmLoomy`（`reasoning.Caps` 的第四类面），
   `RealmForKind` 与 `SupportsEffortKind` **同一次改**（R23、守门测试 `TestEffortKindHasOwnRealm`）；
   档位来源 = `/models` 的 `reasoning_efforts` + `default_reasoning_effort`（**上游权威值**，无需静态兜底）。
2. **档位要投影到请求**：客户端档位 → 该模型的 `reasoning_efforts` 就近降级；
   实测 `enable_thinking:false` **不被采纳**（仍返回 reasoning_content）→ 关闭思考应发 `reasoning_effort: "none"`。
3. **积分**：`UserResourceDetail` 走 **v1 面** `/api/v1/points/records`，取 `availableBalance`
   （= 常规池 `balance` + 每日池 `dailyBalance`），拆「积分 / 每日积分」两条明细；
   `usage.points_consumed` 可用于单次调用对账。

> **阶段 C 补充（已实测）**：流式事件形状、错误形态矩阵（**鉴权错误 = HTTP 200 + `code:"100002"`**）
> → 见 `docs/loomy-raccoon阶段C探针实测.md`。
>
> 该文档的「三件套单调递减」结论**已作废**（单次采样 + 用思考字符数当指标）；
> 2026-09-24 以 `usage.completion_tokens_details.reasoning_tokens` 为权威指标重测，见 §11。



## 11. 思考档位的客户端映射与实测（2026-09-24 补测）

### 11.1 客户端原文（asar 取证）

```js
const hh="loomy:selected-model", mP="loomy:thinking-level";
const Zd=[{value:"low",label:"低"},{value:"medium",label:"中"},{value:"high",label:"高"}], Ec="medium";
function hP(e){const n=String(e||"").trim().toLowerCase();return Zd.some(r=>r.value===n)?n:Ec}
```

- localStorage 键 `loomy:thinking-level`，**三档 low/medium/high（低/中/高）**，默认 `medium`；
  非法值回落 `medium`（`hP()`）。
- 使用处：`Qe=await Gv(); be = Qe.thinkingEnabled===!1 ? void 0 : gP();`
  `Qe.variant=be; A.metadata={...reasoningLevel:be, reasoningEffort:be}` ——
  写进会话 metadata 的 `reasoningEffort`。
- 另有一处 SDK 侧映射（`model-metadata.js`）：`MODEL_THINKING_EFFORT = { on:'medium', off:'none' }`，
  注释明写「OpenCode 原生模型 options；**OpenAI 兼容 SDK 映射为 reasoning_effort**」。

→ 与 wild-work 的投影路径一致：客户端档位 → `reasoning_effort`。

### 11.2 档位对思考量的影响（权威指标 reasoning_tokens）

同一硬推理 prompt（`1000!` 尾零数），直连上游，每档 3 采样：

| 模型 | low | medium | high | 结论 |
|---|---|---|---|---|
| `deepseek-v4-flash-0731` | 295 / 109 / 114（均 173） | 421 / 109 / 362（均 297） | 448 / 293 / 635（均 459） | **单调递增** |
| `MiniMax-M3` | 1362 / 1011 / 624 | 382 / 738 / 586 | 541 / 1218 / — | 噪声大 |
| `spark-x` | 4129 / 718 / 1300 | 504 超时×3 | 4310 / 超时 / 1188 | 噪声大、上游易 504 |

**结论**：`deepseek-v4-flash-0731` 上三档**确有单调差别**（约 2.6× 跨度），选不同档位实际生效；
其它模型噪声大（spark-x 的 medium 档持续 504，上游该档不稳）。

> **指标教训**：思考**字符数**完全不可用（值域重叠、非单调）；必须用 `reasoning_tokens`。
> 阶段 C 的「98→59 字」结论即因此失效。另需把 `max_tokens` 设足够大，否则 ctok 顶到上限会截断思考。

### 11.3 实现：Clamp 已补

`internal/loomy/client.go` 的 `projectEffort` 此前只做三件套投影、**不调 `Clamp`**（与 qoder 三渠道不一致），
会把客户端发的 `max`/`ultra` 原样下发（上游虽不报错但可能静默忽略）。
2026-09-24 已补：先 `reasoning.Caps.Clamp(RealmLoomy, model, effort)` 再投影三件套；
单测 `TestProjectEffortClamp` 覆盖 ladder 内/超限/窄 ladder/关闭语义/能力未知五种情形。

---

## 12. 流式「突然无响应」的根因与修复（2026-09-24）

### 12.1 现象

用户报告 loomy 渠道**突然无响应**。`data/app.log` 里两次中断，时长都**恰好 120.00s**：

```
21:57:16.204844 loomy reasoning: model="GLM-5.3-Flash" in="max" out="xhigh"
21:59:16.211716 stream relay end platform=loomy uid=260915192021296374
                err=context deadline exceeded (Client.Timeout or context cancellation while reading body)
   → 间隔 120.0069s
22:07:12.732207 loomy reasoning: ...
22:09:12.735065 stream relay end platform=loomy ... err=context deadline exceeded
   → 间隔 120.0029s
```

`err` 原文 `Client.Timeout or context cancellation while reading body` 是
`http.Client.Timeout` 的**专属措辞**；120s 来自 `config.json` 的 `upstream.timeout_seconds`。

旁证：`data/ledger/usage-202609.jsonl` 里两条 loomy 记录 `"src":"none"`（`pt=0, ct=0`），
时间戳正是两次超时时刻 —— `usage` 末帧从未到达，token 流水整条丢失。

### 12.2 根因

**Go 的 `http.Client.Timeout` 是「整请求」超时** —— 计时器在 `Do()` 返回后**继续跑**，
直到 body 读完。而 SSE 整个生成期都在读 body，所以「慢但一直在出字」的请求
会被**从流中间掐断**。此时响应头早已按 200 发出，无法改状态码。

用户可见症状是两个缺陷叠加：

1. `internal/loomy/sse.go` 在 `sc.Err() != nil` 时**直接 return，不补 `[DONE]`**
   → 客户端收到一条无收尾的截断流 =「突然无响应」。
2. 120s 总超时把「慢但正常」的请求也判死 —— 同一模型前几次 8~13s 完成，这一条挂到 120s。

对照：**traework 是唯一做对的渠道** —— 另有 `StreamHTTP *http.Client{Transport: tr}`
（无总超时，靠 Transport 的 `ResponseHeaderTimeout` 兜底），`ChatStream` 优先用它。

### 12.3 修复

- **流式改用无总超时的 `StreamHTTP`**（照 traework 范式），非流式 `HTTP` 保留总超时。
- 补 `newTransport()`（禁 h2 + Dial/keepalive + TLS 握手 + `ResponseHeaderTimeout: 60s`）。
  此前本包未设 Transport，实际共用 `http.DefaultTransport`（h2 开启、无 `ResponseHeaderTimeout`）；
  `StreamHTTP` 无总超时后**必须**有 `ResponseHeaderTimeout`，否则连响应头都等不到会无限挂住。
- **空闲看门狗** `provider.IdleReader`（默认 90s，可配 `upstream.stream_idle_seconds`，下限 10s）：
  连续该时长读不到任何字节即判上游卡死，关掉 body 并返回 `provider.ErrIdleTimeout`。
  实测正常请求 8~13s 完成，90s 足够宽松 —— 只拦「真的一个字节都不出」。
- **截断时补 error 帧 + `[DONE]`**（`provider.WriteTruncationFrames`），
  错误码 `upstream_timeout`（空闲超时）/ `upstream_stream_error`（其它读错）。
  只 `return err` = 客户端只能一直等；只补 `[DONE]` = 把故障伪装成正常结束。
- 顺带补齐面板/热更新的代理渠道清单（此前漏 raccoon/loomy/monkeycode/traecode，
  面板保存会静默清空这些渠道手配的代理）。

### 12.4 守门测试

- `TestStreamClientHasNoTotalTimeout`：`StreamHTTP.Timeout == 0` 且 `HTTP.Timeout > 0`，
  且 `StreamHTTP` 有 `ResponseHeaderTimeout` 与禁 h2（防总超时/无兜底回归）。
- `TestStreamTruncationEmitsFrames`：流中断必须写出 error 帧 + `[DONE]`，且已透传内容不丢。
- `TestIdleTimeoutDefaults`：装配漏注入时回落 `DefaultIdleTimeout`。
- `internal/provider/stream_test.go`：看门狗超时/透传/计时重置/幂等 Close/帧写出。
