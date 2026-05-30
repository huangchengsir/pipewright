# Story 1.6: 组件与状态规范库

status: ready-for-dev(等下一波扇出)
epic: 1
独立于:2-2 / 1-4(纯前端,无后端,可并行)
covers: UX-DR3(动效令牌 + prefers-reduced-motion)· UX-DR8(可访问性基线)· DESIGN.md / states-components 组件契约

## As a / I want / So that
As a 前端开发者,I want 一套可复用的基础组件与状态模式(状态徽标/按钮/Toast/Banner/骨架/空态/错误态/二次确认/表单控件/Tooltip/进度),So that 后续所有业务页面复用统一、符合规范的组件与交互。

## Acceptance Criteria
1. 组件库含 DESIGN.md / states-components 定义的全部组件,颜色一律走 OKLCH 令牌,动效仅 transform/opacity 且遵守 `prefers-reduced-motion`(UX-DR3)。
2. 状态徽标固定**六词**(成功/失败/进行中/部分失败/已回滚/排队),进行中态脉冲,**状态不单靠颜色**(色 + 圆点 + 文字)。
3. Toast 右下角堆叠(成功/信息 4s 自动消失、错误手动关闭);高危操作走 **type-to-confirm**(输入名称匹配前禁用确认)。
4. 焦点环可见、Esc 关闭最顶层浮层、表单错误经 `aria-describedby` 关联(UX-DR8 基线)。

## 范围边界(本期做 / 不做)
- ✅ 做:`web/src/components/ui/` 一套基础组件 + 一个 `/states` 展示页(组件库 living styleguide,逐组件演示各态)+ Toast/Confirm 的可编程 API(composable/store)。
- ✅ 顺带:把现有页面里**重复手搓**的状态徽标/按钮/确认弹窗**可选**收敛到新组件(低风险、不强求一次到位;主要交付组件库本身 + 展示页)。
- ❌ 不做:任何新业务页 · 后端改动(纯 FE)· 把所有既有页面全量重构到新组件(渐进迁移,后续 story 顺手替换)。

## 视觉锚点(务必照做,品味要求高)
- `_bmad-output/planning-artifacts/ux-designs/ux-devopsTool-2026-05-27/mockups/mock-states-components.html`(组件与状态规范页:状态徽标/按钮交互态/Toast4型/Banner/骨架/空态/错误态含 AI 降级/二次确认含输入确认/表单控件态/Tooltip/进度,每节标 token + 行为)。
- `DESIGN.md`(令牌 + 组件规格 + Do/Don't「避免 AI 味」)。
- 既有令牌:`web/src/styles/tokens.css`(或 styles 目录)、Inter + JetBrains Mono、naive-ui。复用,别引新 UI 库,别硬编码调色板。

## 组件清单(冻结命名,后续业务页按此引用)
`web/src/components/ui/`:
- `StatusBadge.vue` —— props `status: 'success'|'failed'|'running'|'partial'|'rolledback'|'queued'`;色+圆点+六词文字;running 脉冲(reduced-motion 关)。
- `AppButton.vue` —— variants `primary|default|ghost|danger|ai`;states hover/focus-visible/active/disabled/loading。
- Toast 系统 —— `composables/useToast.ts`(或 pinia store)+ `ToastHost.vue`(右下角堆叠;success/info 4s 自动消、error 手动关;入退场 transform/opacity)。
- `AppBanner.vue` —— info/warn/error/ai 变体。
- `SkeletonBlock.vue` —— 骨架占位。
- `EmptyState.vue` —— 图标 + 标题 + 描述 + CTA slot。
- `ErrorState.vue` —— 含 **AI 降级**变体(「AI 不可用」优雅态)。
- `ConfirmDialog.vue` + `composables/useConfirm.ts` —— 普通确认 + **type-to-confirm**(传 `confirmText`,输入匹配前禁用确认按钮);Esc 关闭、焦点陷阱。
- `FormField.vue` / 表单控件态 —— label + 控件 + 错误(`aria-describedby` 关联)+ 各态(默认/聚焦/错误/禁用)。
- `AppTooltip.vue` —— 悬浮提示(键盘可达)。
- `ProgressBar.vue` —— 进度。
- `pages/StatesShowcase.vue`(或 `views/StatesShowcase.vue`)+ 路由 `/states` —— living styleguide,逐组件 + 逐态演示,可切主题。

> **冻结点:** 组件文件名 + props 枚举(尤其 StatusBadge 六态命名)定死,后续业务页直接引用,不得改名。

## 文件切分(纯前端,只动 `web/`)
- 新增 `web/src/components/ui/*`、`web/src/composables/useToast.ts`/`useConfirm.ts`、`web/src/views/StatesShowcase.vue`、`web/src/components/ui/ui.css`(或各组件 scoped)。
- 改 `web/src/router`(加 `/states` 路由)+ 可选在 rail 加「组件库」入口(dev-only,可不加)。
- **注意与 2-2/1-4 的 web/src/router 漏斗**:本 story 走独立 worktree,集成时我合并 router。

## Dev Notes 护栏
- 动效只 transform/opacity + `@media (prefers-reduced-motion: reduce)` 全退化。
- 可访问性基线:focus-visible 环、Esc 关顶层浮层、表单错误 aria-describedby、Toast `role=status`/`aria-live`、对话框 `role=dialog`+`aria-modal`+焦点陷阱。
- 不硬编码颜色,全走令牌;状态不单靠颜色(色+形+字)。
- 用户品味高:别 AI 套路、别普通后台模板感,照 states-components mock 的密度与质感。

## 编排者亲验清单
- `npm --prefix web run build`(vue-tsc 0 错 + vite build 成功)。
- 真浏览器:开 `/states` 展示页 → 截图肉眼审每组件各态(状态徽标六词+脉冲、Toast 堆叠自动消/手动关、type-to-confirm 禁用逻辑、空态/错误态/AI 降级、focus 环、Esc 关浮层)· 切暗/亮双主题各截一张 · reduced-motion 下脉冲/入场关闭验证。

## Review Findings (code-review 2026-05-30,9-agent 三层对抗)
**已修 patch:**
- [x] [Patch][Crit] StatesShowcase 移除自挂 ToastHost/ConfirmDialog → 消除 /states 双挂载(双焦点陷阱/重复 toast)[web/views/StatesShowcase.vue]
- [x] [Patch][Crit] useConfirm 连续 open 先 resolve(false) 旧 pending(防 await 永久挂起)[web/composables/useConfirm.ts]
- [x] [Patch] ConfirmDialog aria-labelledby 固定 id;ToastHost 子项去 role=alert(解嵌套 live region)
- [x] [Patch] StatusBadge 去 role=status 噪音;queued 文案 排队中→排队(冻结六词)[web/components/ui/StatusBadge.vue]
- [x] [Patch] useToast 定时器 clearTimeout 清理 [web/composables/useToast.ts]
**deferred:** Toast 堆叠上限 · ProgressBar NaN · 焦点还原已卸载元素 · Tooltip 触屏 · FormField 多错误
