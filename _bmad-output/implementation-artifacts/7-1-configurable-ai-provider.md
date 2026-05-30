# Story 7.1: 可配置 AI 提供商(FR-24)

status: ready-for-dev
epic: 7(起手)
基线: master(14 story;1-7 引导「连 AI」步前向链接到此)
covers: FR-24(LLM 提供商/密钥可配,不写死;未配优雅禁用)· NFR-10(AI 降级不阻断核心)

## As a / I want / So that
As a 管理员,I want 配置 LLM 提供商与密钥(Claude/OpenAI/本地 Ollama)、测试连接、选模型、设预算,So that AI 功能走我自己的 LLM,且密钥不写死在代码。

## Acceptance Criteria
1. **Given** 配置 Claude key(存入保险库/加密)**When** 测试连接 **Then** 连接正常并回显延迟;AI 功能走 Claude。
2. **Given** 切换到本地 Ollama **When** 保存 **Then** AI 功能走本地模型。
3. **And** 未配置 AI 时 AI 功能优雅禁用,核心 CI/CD 不受影响(NFR-10);密钥掩码呈现。

## 范围边界(本期做 / 不做)
- ✅ 做:**AI 提供商配置(单例)**——provider(claude|openai|ollama)+ baseUrl + model + apiKey(加密存储,掩码呈现)+ budget(声明,本期不强制执行)+ enabled 开关;**测试连接**(向 provider 发轻量请求,回显延迟/成功/错误);GET/PUT/test 三端点;前端 SettingsAI 表单。`configured` 标志供下游(7-2+)与引导(1-7)消费。
- ❌ 不做:真实 AI 失败诊断(=7-2)· 成功/失败 diff(=7-5)· 代码浏览(=7-4)· budget 强制执行/计量(本期仅存声明,Epic7 用量统计后做)· 流式输出 · 多 provider 并存(本期单一活跃配置)。

## ⚠️ 本机网络约束(亲验须知)
- 真连 Claude(api.anthropic.com)/ OpenAI(api.openai.com)在本机被 GFW 拦、Ollama 未装 → **「测试连接」对真 provider 在 dev 必失败(连接错误),这是正确行为**(用户看到明确错误)。测试机制(发请求/超时/测延迟/错误映射)用**本地 stub HTTP server**(把 baseUrl 指向它)验证成功路径。不配置态/保存/掩码/优雅禁用路径不需网络。

## ⛓ 冻结 API 契约
Base:认证保护,写过 CSRF。错误体 `{"error":{"code,message}}`。
### GET `/api/settings/ai`
→ 200 `{ "configured": false, "enabled": false, "provider": "", "baseUrl": "", "model": "", "apiKeyMasked": "", "budget": { "monthlyTokenLimit": null }, "updatedAt": "RFC3339"|null }`
- `configured` = 有 provider + (provider=ollama 无需 key / 否则有 apiKey)。`apiKeyMasked` 为掩码(经 vault,绝无明文);未设为空串。首次无配置返回上述空默认。
### PUT `/api/settings/ai`
请求 `{ provider, baseUrl, model, apiKey?, budget:{monthlyTokenLimit:number|null}, enabled }` → 200 同 GET DTO。
- `apiKey` **只写**(write-only):省略/空串=保留既有密钥(不清空);非空=轮换为新密钥(vault.SealSecret 加密)。**绝不在 GET/PUT 响应回明文**。
- 校验:provider∈{claude,openai,ollama}(空=清空配置允许);baseUrl 非空(provider 非空时;可给默认);provider≠ollama 且既无既有 key 又无新 key → 422 `api_key_required`。失败 422 定位(`invalid_provider`/`base_url_required`/`api_key_required`)。
- 默认 baseUrl(前端可预填,后端兜底):claude=`https://api.anthropic.com` · openai=`https://api.openai.com` · ollama=`http://localhost:11434`。
### POST `/api/settings/ai/test`
请求(可选 draft,测保存前的配置)`{ provider?, baseUrl?, model?, apiKey? }`;省略字段回退到已保存配置(apiKey 省略=用已存密钥)→ 200 `{ "ok": bool, "latencyMs": number, "detail": "string", "error": "string"|null }`。
- 向 provider 发**轻量探测**(超时 ~8s):claude=`GET {baseUrl}/v1/models`(header `x-api-key`+`anthropic-version: 2023-06-01`)· openai=`GET {baseUrl}/v1/models`(`Authorization: Bearer`)· ollama=`GET {baseUrl}/api/tags`(无 key)。2xx→ok=true+latencyMs;非 2xx/超时/连接错误→ok=false+error(**错误信息绝不含密钥**;映射为人读:连接失败/认证失败/超时)。
- vault 未配置 master key 且需读已存密钥 → 503 `vault_unconfigured`。

> **冻结点:** AI config DTO(configured/enabled/provider/baseUrl/model/apiKeyMasked/budget/updatedAt)+ test 结果(ok/latencyMs/detail/error)定死;下游 7-2~7-5 只**消费 configured/enabled + 用解密 key 调 LLM**,不改外层形状。

## 文件切分(零冲突,前后端并行;同一棵树)
**后端(只动 Go):**
- `internal/store/migrations/0011_ai_config.sql`:`ai_config(id INTEGER PRIMARY KEY CHECK(id=1)` 单例 · provider · base_url · model · `api_key_ciphertext BLOB`(vault secretbox,空=无 key)· budget_json · enabled · created_at · updated_at`)。**只存密文,绝无明文 key**。
- `internal/ai/ai.go` + `ai_test.go`:`Service`(Get 惰性空默认 / Save 加密 key〔复用 `vault.SealSecret`,key 省略则保留旧密文〕+ 校验 / Test 探测)。`Config` 模型 + `Provider` 常量 + `TestResult`。Test 用注入的 `*http.Client`(可在测试替换为指向本地 stub 的 client + 短超时)+ per-provider 请求构造。**无 init 副作用**。
- `internal/httpapi/ai_settings.go` + `_test.go`:GET/PUT/POST test handler + DTO + 错误映射。
- `router.go`(改):挂 `GET/PUT /api/settings/ai`(GET auth;PUT auth+CSRF)+ `POST /api/settings/ai/test`(auth+CSRF)。`main.go`(改):`ai.New(db, vault, httpClient)` + `WithAISettings` Option。
**前端(只动 `web/`):**
- `web/src/api/aiSettings.ts`(新):`getAISettings`/`saveAISettings`/`testAIConnection`。
- `web/src/views/settings/SettingsAI.vue`(改):占位换真实——provider 选择(Claude/OpenAI/Ollama 卡片或下拉)+ baseUrl(按 provider 预填默认)+ model + apiKey(只写,掩码占位「已配置 ••••」/未配空)+ budget(月 token 上限,可空)+ enabled 开关 + 「测试连接」按钮(显延迟 ms / 成功√ / 失败错误)+ 保存。复用 1-6 `components/ui/*`(FormField/AppButton/StatusBadge/AppBanner)。Ollama 时隐藏 apiKey。未配置态友好引导。

## Dev Notes 护栏
- **AC-SEC**:apiKey 仅密文入库(vault.SealSecret),GET/PUT/test/日志/错误**绝无明文**;掩码经 vault 解密即时算后即弃(仿 trigger maskSealed)。自动化测试:存 key 后裸 DB grep 无明文 + test 错误不含 key。
- **优雅禁用(NFR-10)**:未配置/未 enabled → `configured/enabled=false`,下游据此跳过 AI,**核心 CI/CD 路径完全不依赖此服务**(ai.Service 为 nil 或未配置不影响 run/deploy)。
- Test 用**可注入 http.Client + 超时**,单测指向本地 stub(`httptest.Server`)验证 claude/openai/ollama 三构造 + 2xx ok + 4xx 认证失败 + 超时;别在单测真连外网。
- 单例 config(id=1)惰性默认 + ON CONFLICT 回读。参数化 SQL。`make mem-check` ≤100MB。无 init 副作用。

## Tasks
1. [后端] 迁移 0011 + `internal/ai`(Config/Provider/Service:Get/Save〔key 保留语义〕/Test〔per-provider 探测 + 注入 client〕)+ 单测(httptest stub:三 provider 2xx ok/4xx 认证失败/超时;key 保留;裸 DB 无明文;掩码)。
2. [后端] `httpapi/ai_settings.go` GET/PUT/test + 错误映射 + 单测(401/403/422 各校验/200 形态/test 走 stub);router 挂载 + main 装配。
3. [前端] `api/aiSettings.ts` + `SettingsAI.vue`(provider/baseUrl/model/key 只写/budget/enabled/测试连接显延迟/保存,Ollama 隐 key,未配引导)。

## 编排者亲验清单(派活后必跑,别只信 agent 自检)
- gofmt/vet/`go test ./...`/`-race ./internal/ai ./internal/httpapi`/`CGO_ENABLED=0 build`/`make mem-check`。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + build。
- 真二进制:GET 未配置默认 configured=false · PUT 存 claude+key → 掩码呈现、裸 DB 无明文 key · PUT 省略 key 二次保存 → 旧 key 保留(test 仍可用)· **test 指向本地 stub server(curl 起个回 200 的 HTTP)→ ok=true+latencyMs;指向死端口 → ok=false+连接错误,错误无明文** · provider=ollama 无 key 可 configured · 切 provider 保存 · 未认证 401 · 无 CSRF 403。
- 真浏览器:SettingsAI 三 provider 切换(Ollama 隐 key)· 填 key(只写掩码)· 测试连接显结果 · budget · enabled · 保存 · 未配置态引导 · 截图肉眼审质量。

## Review Findings (code-review 2026-05-30,综合对抗)
**已修 patch:**
- [x] [Patch][Major] Test 探测对用户可控 baseUrl 无 SSRF 收口 → 加 validProbeURL(恒拒云元数据169.254/链路本地/未指定;回环+私网放行,符合自托管姿态)[ai/ai.go]
- [x] [Patch][Major] GET /api/settings/ai 在 master key 缺失但有存储 key 时整页 503 → 掩码降级为占位"••••",其余字段照常返回(NFR-10)[ai/ai.go load]
- [x] [Patch][Minor] maskKey 按字节切末4位可能切坏多字节 UTF-8 → 改 rune-safe [ai/ai.go]
**deferred:** budget 无上限校验 · Test 路径 load() 调两次(效率)
