---
baseline_commit: NO_VCS
---

# Story 1.5: 应用外壳与双主题(+ 登录壳外屏)

Status: done

> 本 story 与 **Story 1.2(后端单管理员认证)并行开发**。本 story **拥有整个 `web/` 目录**(前端全归你),**绝不修改任何 Go 文件 / `go.mod` / 迁移**。登录视图按下方「冻结的 Auth API 契约」对接 1.2 的后端。

## Story

As a 管理员,
I want 一个干净的应用外壳(62px 左侧 rail 导航 + 铺满宽度主区 + 右下角明/暗主题切换),使用双主题 OKLCH 令牌,并能在登录壳外屏完成登录,
so that 所有页面拥有一致、可切换、铺满宽度的视觉骨架,且登录后进入控制台。

## Acceptance Criteria

1. **AC1 — 应用外壳**:登录后见 **62px 左侧 rail**(顶部品牌标 `d>`;一级:概览/运行/服务器/通知;底部齿轮=设置),当前项以**电蓝 + 左侧 2.5px 竖条**标识;主内容区**铺满宽度**(`content-max` ~1680px、`main-pad` 48px,与全宽顶栏对齐),不留窄居中列大片空白。
2. **AC2 — 双主题**:右下角常驻明/暗切换,点击即时切换 `data-theme` 并 **localStorage 记忆**;深(默认)/浅两套 **OKLCH 令牌**(取自 DESIGN.md frontmatter)均生效且都"经过设计"(非机械反色)。
3. **AC3 — 设计契约一致**:colors / typography(**Inter + JetBrains Mono**,中文回退 PingFang SC)/ rounded / spacing 与 `DESIGN.md` 契约一致;令牌以 **CSS 自定义属性**集中定义,不散落硬编码;动效仅用 `transform/opacity` 且遵守 `prefers-reduced-motion`。
4. **AC4 — 收口边界 + 登录**:本 story **仅含 app shell(导航/布局/主题/路由占位)+ 登录壳外屏**,**不含任何业务页面**(概览/运行/服务器/通知/设置均为占位空视图,仅标题/空态)。登录页(壳外,无 rail)对接 1.2 契约:输入用户名/口令 → 成功进控制台、失败显字段/banner 错误、锁定显 429 文案;未登录访问受保护路由 → 跳登录。

## 冻结的 Auth API 契约(前后端共享,不得擅改;改动需同步通知 1.2)

- **会话探测** `GET /api/auth/session` → 200 `{ "username": "admin" }`(已登录)/ 401(未登录)。应用启动时调用以决定进控制台还是跳登录。
- **登录** `POST /api/auth/login` body `{ "username": string, "password": string }` →
  - 200 `{ "username": "admin" }`(后端已设 HttpOnly 会话 cookie + 可读 `pipewright_csrf` cookie);
  - 401 `{ "error": { "code": "invalid_credentials", "message": "用户名或口令错误" } }`;
  - 429 `{ "error": { "code": "locked_out", "message": "登录失败次数过多,请 N 分钟后重试" } }`。
- **登出** `POST /api/auth/logout` → 204(清 cookie)。属写请求 → 须带 CSRF header。
- **CSRF 双提交**:后端下发可读 cookie `pipewright_csrf=<token>`;前端**所有写请求**(POST/PUT/PATCH/DELETE)读取该 cookie 值并置于请求头 `X-CSRF-Token`。封装一个 fetch/axios 包装器统一附加,并在收到 **401 自动跳登录**。
- 后端开发期跑在 `go run`(:8080);前端 `vite dev` 经 proxy 转发 `/api` → 后端(见 vite.config.ts;**仅本地开发**,部署时同二进制同端口,无代理)。本机后端可能尚未就绪,登录页在 `/api/auth/session` 不可达时按"未登录"处理并可重试。

## Tasks / Subtasks

- [x] **Task 1 — 依赖与前端结构**(AC: 1,2,3)
  - [ ] `web/package.json` 增:`vue-router`、`pinia`、`naive-ui`、线性图标集(建议 `@vicons/tabler` 或 `lucide-vue-next`,细线描边,**禁星芒/AI 套路图标**)。`npm install`(走 npmmirror,见环境)。
  - [ ] 按领域建结构:`web/src/{layouts,components,views,composables,stores,router,styles,api}`(Vue/TS 命名:组件 PascalCase、组合式 `useXxx`、store `useXxxStore`)。[Source: architecture.md#结构规范]
- [x] **Task 2 — 设计令牌(CSS 自定义属性)**(AC: 2,3)
  - [ ] `web/src/styles/tokens.css`:把 DESIGN.md frontmatter 的 **OKLCH 颜色**落为 `:root` 变量(dark 默认)+ `[data-theme="light"]` 覆盖(凡 `-light` 键);typography / rounded / spacing(rail=62px、main-pad=48px、content-max=1680px、card-gap=18px)同步为变量。
  - [ ] `web/src/styles/global.css`:基线(box-sizing、字体栈 Inter + JetBrains Mono + PingFang SC 回退、bg-glow 右上微弱电蓝径向辉光、滚动条)。字体可走系统回退或自托管子集(**勿引入第二装饰色**)。
  - [ ] Naive UI 主题:用 `NConfigProvider` + `darkTheme`/自定义 `themeOverrides` 把 Naive 主色对齐到 `{colors.primary}` 电蓝,使 Naive 组件与令牌一致。
- [x] **Task 3 — 主题切换**(AC: 2)
  - [ ] `stores/theme.ts`(Pinia):状态 `dark|light`,初值取 localStorage(无则 dark 默认);切换写 `document.documentElement.dataset.theme` + 持久化。
  - [ ] 右下角浮动切换按钮(所有壳内屏常驻);`prefers-reduced-motion` 下切换瞬时无过渡动画。
- [x] **Task 4 — 应用外壳布局**(AC: 1)
  - [ ] `layouts/AppShell.vue`:62px sticky 全高 rail(品牌 `d>` + 四个一级目的地图标 + 底部齿轮设置,当前项电蓝 + 2.5px 左竖条)+ `<router-view>` 主区(铺满宽度,max 1680、padding 48)。
  - [ ] 一级目的地图标:概览(宫格)/运行(流水)/服务器(横条)/通知(铃),细线描边;hover/active/focus 态都"经过设计"(focus 环可见)。
- [x] **Task 5 — 路由 + 占位视图**(AC: 1,4)
  - [ ] `router/index.ts`:`createWebHistory`;壳内路由(用 AppShell 布局):`/`(概览)、`/runs`、`/servers`、`/notifications`、`/settings`(+ 设置二级占位:ai/vault/account/system 可仅留路由)。壳外路由:`/login`(无 rail 布局)。
  - [ ] 占位视图:每个仅渲染页标题 + 简洁空态(「此页将在后续 story 实现」式占位,符合 DESIGN 空态规格),**不实现任何业务逻辑/数据**(收口边界)。
  - [ ] 路由守卫:进入壳内路由前用 `GET /api/auth/session` 判定;401 → 重定向 `/login`(带回跳)。
- [x] **Task 6 — 登录壳外屏(对接 1.2)**(AC: 4)
  - [ ] `api/http.ts`:fetch/axios 包装器,统一附加 `X-CSRF-Token`(读 `pipewright_csrf` cookie)+ 401 自动跳登录 + 统一解析 `{error:{code,message}}`。
  - [ ] `views/Login.vue`:单管理员登录卡(「自托管 · 单管理员」气质,参考 `mockups/mock-login.html` 视觉但以脊柱令牌为准):用户名 + 口令字段(focus/error 态走 DESIGN input 规格)、登录按钮(loading 态)、字段级 + banner 级错误(401 显「用户名或口令错误」、429 显锁定文案);成功 → 进 `/`。
- [x] **Task 7 — 自验 + 收尾**
  - [ ] `npm run build`(`vite build`)成功产 `web/dist`(供 go:embed;**只你重建前端**,后端不重建);TS 通过(`vue-tsc`)。
  - [ ] 自查:rail 当前项竖条、主题切换记忆、内容铺满宽度、reduced-motion 降级、登录失败/锁定文案。无业务页面越界。
  - [ ] 更新本文件 Dev Agent Record(实现说明 / File List / AC 状态);Status → review。

## Review Findings(2026-05-29 三层对抗评审:Blind / Edge / Acceptance)

Decision-needed(已拍板 → 转 Patch):
- [x] [Review][Decision→Patch] 字体加载 = **自托管子集**(作者定):去 Google Fonts CDN `@import`,把 Inter + JetBrains Mono 的 woff2 子集放进 `web/` 经 go:embed,离线可用 + 满足 CSP [web/src/styles/global.css + web/ 字体文件]。

Patch(无歧义可直接修):
- [x] [Review][Patch] 路由守卫每次导航都同步 `await fetchSession()` 无缓存(请求瀑布/阻塞导航),且 `fetchSession` catch 一切异常把 5xx/网络错也当未登录踢用户去登录 → session 进 Pinia 缓存(启动拉一次)+ 区分 401 与 5xx/网络错 [web/src/router/index.ts,api/auth.ts](blind+edge+auditor,Medium)
- [x] [Review][Patch] 登录成功用未校验的 `redirect` query 直接 `router.replace` → 开放重定向风险,加同源站内路径白名单(仅单 `/` 开头、非 `//`)否则回退 `/` [web/src/views/Login.vue](edge,Medium)
- [x] [Review][Patch] http.ts 401 重定向 `location.replace` 硬跳:并发多请求各触发一次 + 永不 resolve 的 promise 挂起 loading + 丢 hash → 加重入保护 + 用完整路径 [web/src/api/http.ts](blind+edge,Low)
- [x] [Review][Patch] 主题 `localStorage` 读写无 try/catch,隐私模式/配额满会抛错连带 app 挂载失败 → 包 try/catch 降级内存态 [web/src/stores/theme.ts](edge,Low)
- [x] [Review][Patch] AppShell 主区 `max-width:1680` 无 `margin:0 auto` 未居中(超宽屏右侧留白)+ 纵向 padding 硬编码(26/90)未走令牌 → 居中对齐 + 间距走 `--main-pad`/令牌 [web/src/layouts/AppShell.vue](auditor,Medium)
- [x] [Review][Patch] Login/AppShell 多处硬编码圆角(18/11px)与手写阴影绕过令牌(AC3「令牌集中不散落」)→ 改走 `--rounded-*`/`--shadow` [web/src/views/Login.vue,layouts/AppShell.vue](auditor,Low)

Dismissed:见 Story 1.2 同节(CSRF cookie 非 HttpOnly = 双提交有意取舍)。

## Dev Notes

### 视觉/体验脊柱(冲突时以脊柱为准,mock 仅参考)
- **令牌锚点** = `mockups/mock-run-diagnosis.html` 的 `:root` / `[data-theme="light"]`;**组件/状态目录** = `mockups/mock-states-components.html`;**登录** = `mockups/mock-login.html`;**外壳 IA** 见 EXPERIENCE.md#Information Architecture(rail 62px、四目的地、底部设置、当前项电蓝竖条、内容铺满)。
- **电蓝是唯一品牌色**;**青色仅 AI 专属**(本 story 不该出现 AI,故几乎不用青);绿/琥珀/红语义固定。**不新增颜色、不引第二装饰色**。[Source: DESIGN.md#Colors / Do's]
- **深色默认、浅色对等**(两套都经设计);层次靠字阶对比 + 文本色阶梯 + 浮起阴影,避免全局均匀 padding/阴影(防"AI 味"通用后台感)。[Source: DESIGN.md#Do's]
- 状态徽标固定六词(成功/失败/进行中/部分失败/已回滚/排队中)——本 story 若需展示组件,沿用;真正状态组件库归 1.6。
- 动效:仅 `transform`/`opacity`/`clip-path`;入场 `translateY`+ease;`prefers-reduced-motion` 整体降级。[Source: DESIGN.md / EXPERIENCE.md#Accessibility]

### 技术基座(承自架构)
- **Vue 3 + Vite + Pinia + Vue Router + Naive UI**;前端 `go:embed` 进单二进制,双运行无差异。[Source: architecture.md#Frontend Architecture;EXPERIENCE.md#Foundation]
- 单二进制约束(1.1):**不做前后端分离部署**;`vite dev` 代理仅本地开发用;生产同端口由 Go 同时供 SPA + API。**勿改 vite `base:'/'`**(1.1 评审已定,深路由 fallback 依赖它)。
- 重依赖(Monaco 等)后续 story 懒加载,本 story 不引入。Naive UI 按需即可。

### 环境(本机受限)
- npm registry 已设 `https://registry.npmmirror.com`;`npm install` 拉新依赖**需 `dangerouslyDisableSandbox`**(沙箱拦网络)。
- 前后端联调:后端(1.2)由另一成员开发,可能未就绪;登录页对 `/api` 不可达需优雅处理(显示可重试,不白屏)。`vite.config.ts` 加 dev proxy `/api` → `http://localhost:8080`。

### 禁止
- ❌ 修改任何 Go 文件 / `go.mod` / `cmd` / `internal` / 迁移(后端归 1.2);❌ 实现业务页面(超出 1.5 收口边界);❌ 改 vite `base`;❌ 引第二装饰色或 AI 套路星芒图标;❌ 动画布局属性(width/height/top/left);❌ 用转圈遮罩替代骨架(本 story 占位用空态即可)。

### References
- [Source: epics.md#Epic 1 / Story 1.5]
- [Source: DESIGN.md(令牌/组件/Do's,锚点 mock-run-diagnosis + mock-states-components + mock-login)]
- [Source: EXPERIENCE.md#Information Architecture / Foundation / 壳外屏(登录)]
- [Source: architecture.md#Frontend Architecture / 命名·结构规范]
- [Source: 1-1-project-scaffold.md(vite base:'/'、go:embed、单二进制约束,勿回退)]

## Dev Agent Record

### Agent Model Used

claude-sonnet via Agent(前端成员,并行)+ claude-opus-4-8 编排者(集成构建与契约联调)。

### Debug Log References

- 实现成员在最终收尾前遇 API 500 中断;编排者接手做收尾验证:`vue-tsc --noEmit`(零类型错误)+ `vite build`(成功,视图代码分割/懒加载,主包 ~83KB gzip)+ 全量二进制 `make`/`go build` 内嵌该 dist 并端到端联调登录契约(配合 1.2 后端,login/session/logout 全过)。

### Completion Notes List

- **已实现**:OKLCH 双主题令牌(`tokens.css` `:root` 深色默认 + `[data-theme="light"]` 覆盖)+ `global.css`(Inter/JetBrains Mono/PingFang SC 回退、bg-glow 径向辉光)+ Naive UI `themeOverrides` 对齐电蓝;Pinia 主题 store + 右下角浮动明暗切换(localStorage 记忆);`AppShell.vue` 62px sticky rail(品牌 `d>` + 概览/运行/服务器/通知 + 底部设置,当前项电蓝 2.5px 竖条)+ 铺满宽度主区;`vue-router` 壳内路由(概览/运行/服务器/通知/设置 + 设置二级)+ 壳外 `/login` + 路由守卫(`/api/auth/session` 401 → 跳登录);`api/http.ts` 包装器(`X-CSRF-Token` 双提交 + 401 自动跳登录);`Login.vue` 登录壳外屏(字段/banner 错误、loading 态、按契约对接 1.2)。
- **占位边界**:概览/运行/服务器/通知/设置均为仅标题 + 空态占位视图,无业务逻辑(收口边界遵守)。
- **AC 状态**:AC1 62px rail + 当前项电蓝竖条 + 铺满宽度 ✅;AC2 右下角即时切换 + localStorage 记忆 + 深浅双令牌 ✅;AC3 令牌走 CSS 自定义属性、Inter+JetBrains Mono、动效仅 transform/opacity + reduced-motion ✅;AC4 仅 shell + 登录、无业务页面、登录对接 1.2 契约 ✅。
- **验证**:`vue-tsc` 零错误;`vite build` 成功产 `web/dist`;内嵌进二进制后真实 SPA 200 text/html、登录流端到端打通。
- **延后/待评审**:**视觉肉眼复审待做**(用户品味高,需就 dark/light 双主题、rail、登录页核对"无 AI 味/对比/留白";本机 Playwright 桥常未连,建议用户 `open` 跑起的二进制肉眼审);`vite.config.ts` 已加 dev proxy `/api`→:8080(仅本地开发)。

### File List

新增:
- `web/src/styles/tokens.css`、`web/src/styles/global.css`、`web/src/views/placeholder.css`、`web/src/views/sub-placeholder.css`
- `web/src/stores/theme.ts`、`web/src/composables/useNaiveTheme.ts`、`web/src/components/ThemeToggle.vue`
- `web/src/layouts/AppShell.vue`、`web/src/router/index.ts`
- `web/src/api/http.ts`、`web/src/api/auth.ts`
- `web/src/views/Login.vue`、`Overview.vue`、`Runs.vue`、`Servers.vue`、`Notifications.vue`、`Settings.vue`
- `web/src/views/settings/SettingsAI.vue`、`SettingsAccount.vue`、`SettingsVault.vue`、`SettingsSystem.vue`

修改:
- `web/package.json`(+ vue-router/pinia/naive-ui/@vicons/tabler)、`web/package-lock.json`
- `web/src/main.ts`(挂 router/pinia/naive)、`web/src/App.vue`(壳容器)、`web/vite.config.ts`(dev proxy)
- `web/dist/*`(vite 构建产物,go:embed)

## Change Log

- 2026-05-29:创建 Story 1.5 上下文(应用外壳+双主题+登录壳外屏),与 1.2 并行;冻结 Auth API 契约。Status → in-progress。
- 2026-05-29:实现前端外壳(62px rail + 铺满宽度)+ OKLCH 双主题切换 + 路由占位 + 登录壳外屏(对接 1.2 契约);vue-tsc/vite build 全绿,内嵌后端二进制端到端登录联调通过。视觉肉眼复审待做。Status → review。
- 2026-05-29:真浏览器 E2E(Playwright headless 驱动内嵌前端二进制 :18090)跑通 6/6 —— 路由守卫跳登录、错误口令显 banner、登录成功进控制台、主题切换 dark↔light、主题刷新记忆、占位页切换。**E2E 抓到并修复一处真实前端 bug**:`web/src/api/http.ts` 全局「401→跳登录」拦截器会劫持**登录接口自身的 401**(错误口令),导致 banner 不显示 + redirect 参数套娃 + 后续正确登录被弹回;修法:`/api/auth/*` 端点的 401 放行给调用方处理(Login 显 banner、守卫当未登录),已重建 dist 复跑 6/6 绿。视觉复审(深/浅双主题 + rail + 登录页)已通过截图确认,"冷静工程控制台"气质达标、无 AI 味。
- 2026-05-29:`bmad-code-review` 三层对抗评审 → 6 前端 patch + 1 decision(字体=自托管子集)全部应用:字体改 `@fontsource/*` woff2 自托管 go:embed(去 Google Fonts CDN,36 woff2 离线可用)、路由守卫 Pinia session 缓存 + 区分 401/5xx 网络错、登录 redirect 同源白名单防开放重定向、http.ts 401 重定向重入保护 + 全路径、主题 localStorage try/catch 容错、AppShell 主区 margin:0 auto 居中 + 间距走令牌、硬编码圆角改 `--rounded-*`。**编排者 E2E 验证又抓修 1 回归**:session 缓存致登录成功后守卫读陈旧"未登录"弹回 → 登录成功 `setUser` 写缓存。vue-tsc/vite build 全绿,真浏览器 E2E 6/6(路由守卫/错误 banner/登录成功/主题切换/记忆/占位)。Status → done。
