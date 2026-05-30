---
title: "Pipewright — Design"
status: final
created: 2026-05-27
updated: 2026-05-28
# 颜色令牌为 OKLCH。dark 为默认主题,light 为对等主题;凡键名以 `-light` 结尾者为
# light 主题下的覆盖值(对应 mock 中 `[data-theme="light"]` 块)。其余键在两主题同名复用。
colors:
  # ——— 表面 / 结构(dark 默认) ———
  bg: 'oklch(15% 0.003 270)'
  card: 'oklch(20% 0.004 272)'
  card-2: 'oklch(23% 0.005 272)'
  inset: 'oklch(17.5% 0.004 270)'
  canvas: 'oklch(13.5% 0.003 270)'        # 流水线编排画布
  term: 'oklch(8.5% 0.004 270)'           # 终端纯黑 #09090B
  code: 'oklch(13% 0.004 270)'            # 代码 diff 背景
  border: 'oklch(100% 0 0 / .055)'
  border-strong: 'oklch(100% 0 0 / .12)'
  # ——— 文本阶梯 ———
  text: 'oklch(95% 0.004 270)'
  dim: 'oklch(72% 0.008 270)'
  faint: 'oklch(55% 0.01 272)'
  line-num: 'oklch(42% 0.012 270)'
  # ——— 品牌主色:电蓝(唯一品牌强调色) ———
  primary: 'oklch(66% 0.155 258)'
  primary-press: 'oklch(60% 0.155 258)'
  primary-soft: 'oklch(66% 0.155 258 / .2)'
  # ——— AI 语义:青(仅用于 AI 功能) ———
  cyan: 'oklch(82% 0.1 205)'
  cyan-soft: 'oklch(82% 0.1 205 / .14)'
  cyan-line: 'oklch(82% 0.1 205 / .3)'
  # ——— 状态语义 ———
  green: 'oklch(77% 0.15 150)'            # 成功
  green-soft: 'oklch(72% 0.16 150 / .14)'
  green-line: 'oklch(72% 0.16 150 / .35)'
  amber: 'oklch(83% 0.13 82)'             # 进行中 / 警告
  amber-soft: 'oklch(83% 0.13 82 / .14)'
  amber-line: 'oklch(83% 0.13 82 / .4)'
  red: 'oklch(69% 0.17 22)'              # 失败
  red-soft: 'oklch(62% 0.18 22 / .15)'
  red-line: 'oklch(62% 0.18 22 / .4)'
  logblue: 'oklch(74% 0.11 248)'         # 日志中 URL / 链接着色
  # ——— 阴影 / 内阴影(见 Elevation) ———
  shadow: '0 1px 2px oklch(0% 0 0/.4), 0 14px 34px oklch(0% 0 0/.3)'
  modal-shadow: '0 24px 70px oklch(0% 0 0/.55)'
  inner: 'inset 0 1px 12px oklch(0% 0 0/.55)'
  # ——— light 主题覆盖(对应 [data-theme="light"]) ———
  bg-light: 'oklch(96.5% 0.004 270)'
  card-light: 'oklch(100% 0 0)'
  card-2-light: 'oklch(98% 0.004 270)'
  inset-light: 'oklch(97.5% 0.004 270)'
  canvas-light: 'oklch(98.5% 0.004 270)'
  term-light: 'oklch(20% 0.01 270)'
  border-light: 'oklch(20% 0.02 270 / .1)'
  border-strong-light: 'oklch(20% 0.02 270 / .16)'
  text-light: 'oklch(25% 0.01 270)'
  dim-light: 'oklch(45% 0.014 270)'
  faint-light: 'oklch(58% 0.016 270)'
  line-num-light: 'oklch(62% 0.02 270)'
  primary-light: 'oklch(55% 0.17 258)'
  primary-press-light: 'oklch(49% 0.17 258)'
  primary-soft-light: 'oklch(55% 0.17 258 / .12)'
  cyan-light: 'oklch(52% 0.11 215)'
  cyan-soft-light: 'oklch(52% 0.11 215 / .1)'
  green-light: 'oklch(56% 0.14 150)'      # 表格上下文用 50%(对比更稳)
  amber-light: 'oklch(62% 0.12 70)'
  red-light: 'oklch(55% 0.18 22)'
  logblue-light: 'oklch(60% 0.13 248)'
  shadow-light: '0 1px 2px oklch(50% 0.02 270/.06), 0 12px 30px oklch(50% 0.03 270/.1)'
  modal-shadow-light: '0 24px 60px oklch(40% 0.03 270/.22)'
  inner-light: 'inset 0 1px 10px oklch(0% 0 0/.16)'
typography:
  # 两套字族:Inter(无衬线 UI)+ JetBrains Mono(等宽,代码/ID/数字)。
  # 基准 root 字号 15px;以下 fontSize 为锚点 mock 中的实际取值。
  display:
    fontFamily: 'Inter'
    fontSize: 1.5rem        # ≈24px 页标题(组件规范页 h1)
    fontWeight: '700'
    letterSpacing: -0.02em
  h1:
    fontFamily: 'Inter'
    fontSize: 1.16rem       # 运行/详情主标题
    fontWeight: '600'
    letterSpacing: -0.01em
  kpi-number:
    fontFamily: 'JetBrains Mono'
    fontSize: 1.7rem        # KPI 大数字
    fontWeight: '700'
    letterSpacing: -0.02em
  section-title:
    fontFamily: 'Inter'
    fontSize: 0.86rem
    fontWeight: '600'
  body:
    fontFamily: 'Inter'
    fontSize: 0.86rem       # ≈13px 正文/说明
    fontWeight: '400'
    lineHeight: '1.6'
  label:
    fontFamily: 'Inter'
    fontSize: 0.83rem
    fontWeight: '500'
  caps:
    fontFamily: 'Inter'
    fontSize: 0.71rem       # 全大写小标签(表头/分组)
    fontWeight: '600'
    letterSpacing: 0.04em
  micro:
    fontFamily: 'Inter'
    fontSize: 0.68rem       # 徽标/计数
    fontWeight: '600'
  mono:
    fontFamily: 'JetBrains Mono'
    fontSize: 0.8rem        # 提交 hash / 分支 / 日志 / 代码
    fontWeight: '400'
    lineHeight: '2'         # 终端行高;代码 diff 用 1.55
rounded:
  sm: 4px        # 复选框
  md: 7px        # 徽标 / 分段项 / 状态 pill
  DEFAULT: 9px   # 按钮 / 输入框 / 小卡
  lg: 11px       # 终端 / diff / action 条
  xl: 14px       # 模态 / 大卡(13–16px 区间)
  card: 15px     # 主内容卡(header/pipe/diag)
  full: 9999px   # 进度条 / 开关 / 置信度环 / 圆点
spacing:
  unit: 4px
  '1': 4px
  '2': 8px
  '3': 12px
  '4': 16px
  '5': 20px
  '6': 24px
  rail: 62px              # 左侧导航轨宽
  main-pad: 48px         # 主区左右内边距
  card-gap: 18px         # 卡片之间
  content-max: 1680px    # 主内容最大宽度(全局规则①:内容铺满,避免两侧大留白)
components:
  button-primary:
    background: '{colors.primary}'
    foreground: '#fff'
    radius: '{rounded.DEFAULT}'
    height: 34px
    shadow: '0 5px 16px {colors.primary-soft}'
    hover: 'background {colors.primary-press} + translateY(-1px)'
  button-secondary:
    background: '{colors.card-2}'
    foreground: '{colors.text}'
    border: '1px solid {colors.border-strong}'
    radius: '{rounded.DEFAULT}'
  button-ghost:
    background: 'transparent'
    foreground: '{colors.dim}'
    border: '1px solid {colors.border-strong}'
  button-danger:
    background: '{colors.red-soft}'
    foreground: '{colors.red}'
    border: '1px solid {colors.red-line}'
  pill-status:
    radius: '{rounded.md}'
    fontSize: '{typography.micro.fontSize}'
    dot: '6px circle'
  card:
    background: '{colors.card}'
    border: '1px solid {colors.border}'
    radius: '{rounded.card}'
    shadow: '{colors.shadow}'
  kpi-card:
    background: '{colors.card}'
    radius: '{rounded.xl}'
    number: '{typography.kpi-number}'
  toast:
    background: '{colors.card-2}'
    border: '1px solid {colors.border-strong}'
    radius: '12px'
    shadow: '{colors.modal-shadow}'
    width: 340px
    accent-bar: '3px left edge, 语义色'
  modal:
    background: '{colors.card}'
    border: '1px solid {colors.border-strong}'
    radius: '{rounded.xl}'
    shadow: '{colors.modal-shadow}'
  input:
    background: '{colors.inset}'
    border: '1px solid {colors.border}'
    radius: '{rounded.DEFAULT}'
    height: 38px
    focus: 'border {colors.primary} + 0 0 0 3px {colors.primary-soft}'
    error: 'border {colors.red} + 0 0 0 3px {colors.red-soft}'
  switch:
    on: '{colors.primary}'
    off: '{colors.border-strong}'
    radius: '{rounded.full}'
  segmented:
    track: '{colors.inset}'
    active: '{colors.card}'
    radius: '{rounded.DEFAULT}'
  terminal:
    background: '{colors.term}'
    radius: '{rounded.lg}'
    shadow: '{colors.inner}'
    line-num: '{colors.line-num}'
  diff-row-add:
    background: '{colors.green-soft}'
    gutter-sign: '{colors.green}'
  diff-row-del:
    background: '{colors.red-soft}'
    gutter-sign: '{colors.red}'
  pipeline-node:
    dot: '13px circle'
    ok: '{colors.green} + 0 0 0 4px {colors.green-soft}'
    bad: '{colors.red} + glow + blink'
    skip: '{colors.inset} + {colors.border-strong}'
  pipeline-connector:
    edge: 'stroke {colors.primary}, width 2, opacity .55, 贝塞尔曲线'
    flow: 'stroke {colors.cyan}, dashed, 流光动画'
  ai-diagnosis-card:
    background: '{colors.card}'
    radius: '{rounded.card}'
    accent: '{colors.cyan}'
    icon: 'activity-pulse line(禁用星芒)'
  confidence-meter:
    track: '{colors.cyan} @ .2 alpha'
    fill: '{colors.cyan}'
    radius: '{rounded.full}'
---

# Pipewright — 视觉身份(DESIGN.md)

> 本文件是 Pipewright 的视觉契约。**当本文件与任意 mock(`mockups/*.html`)冲突时,以本文件为准。** mock 是同一视觉语言的实现样本与可截图参考;`mockups/mock-run-diagnosis.html` 是令牌锚点(其 `:root` / `[data-theme="light"]` 即完整双主题令牌集),`mockups/mock-states-components.html` 是组件与状态目录。

## Brand & Style

Pipewright 是一个轻量、自托管、开源的 CI/CD 与部署编排平台。它的产品立场是"**冷静的工程控制台**"——为运维与开发者在低光、凌晨、出故障的真实场景里服务,刻意与 Jenkins 的杂乱后台拉开距离:干净、克制、信息密度高,每一屏都像一张可以直接放进开源首页的产品截图。

视觉语言由此而来:**深色为默认主题**(贴合 ops 低光使用、降低与传统 CI 工具的视觉关联),**浅色为完全对等的第二主题**(同一套令牌的另一组取值,通过 `data-theme` 切换,二者都必须看起来是经过设计的、而非"深色反色")。层叠的深色表面营造纵深;卡片"浮"在带微弱品牌色辉光的背景之上。**单一品牌强调色是电蓝**,语义状态色(绿/琥珀/红)各司其职,**青色是 AI 的专属语义**——只在 AI 功能出现。

**动效是一等设计要素**,不是装饰:诊断卡入场、置信度环填充、状态时间线推进、运行中脉冲、hover 抬升、diff 高亮扫过——但全部只走合成器友好属性(`transform` / `opacity` / `clip-path`),并在 `prefers-reduced-motion` 下整体降级。

排版用 **Inter**(无衬线 UI)配 **JetBrains Mono**(等宽,承载提交 hash、分支、日志、ID、数字),强字阶对比制造层次。

## Colors

调色板是"一个品牌色 + 一组语义色 + 一套中性表面阶梯",纪律是**克制**。下列 OKLCH 值取自锚点 mock;每个令牌都有两主题取值(dark 为默认,light 见 frontmatter `-light` 键)。

- **电蓝 Primary(`{colors.primary}` = oklch 66% 0.155 258,light 55% 0.17 258)** 是**唯一**品牌强调色。用于主按钮、激活导航项、tab 下划线、链接、选中态、聚焦光环、流水线连线主线。按压态用 `{colors.primary-press}`,光晕/软底用 `{colors.primary-soft}`。不用作状态色。
- **青 Cyan(`{colors.cyan}` = oklch 82% 0.1 205,light 52% 0.11 215)** 是 **AI 专属语义色**。只出现在 AI 功能:失败诊断卡、置信度环、diff 中"与失败相关"标注、AI 类 toast/banner、AI 生成入口。**它不是第二品牌色,不得用于普通 chrome 或装饰。**
- **绿 = 成功**(`{colors.green}`)、**琥珀 = 进行中 / 警告**(`{colors.amber}`)、**红 = 失败**(`{colors.red}`)。语义固定,不混用。每色都有 `-soft`(底)与 `-line`(边)变体用于 pill / banner / 高亮行。
- **中性表面阶梯**:`{colors.bg}`(页底)→ `{colors.inset}`(凹陷/轨道)→ `{colors.card}`(主卡)→ `{colors.card-2}`(抬升卡/次表面);`{colors.canvas}` 为流水线编排画布(更深 + 网点)。**`{colors.term}` 是终端的纯黑(oklch 8.5%)**,`{colors.code}` 为代码 diff 背景,刻意比卡片更暗以示"这是真实日志/代码"。
- **文本阶梯**:`{colors.text}` → `{colors.dim}` → `{colors.faint}` → `{colors.line-num}`,四级递降,用对比承担层次而非靠加粗或加色。
- **边框**用极低对比的 `{colors.border}`(白 .055 alpha)做"幽灵描边",`{colors.border-strong}` 用于需要可见分隔处。
- **logblue**(`{colors.logblue}`)仅在日志/终端里给 URL 着色,不进 UI chrome。

不要新增颜色,不要再引入第二个装饰色。

## Typography

两套字族,各有职责:

- **Inter** 承载所有 UI 文本:标题、正文、标签、按钮。强字阶对比是手段——页标题(`{typography.display}` 1.5rem / 700)与正文(`{typography.body}` 0.86rem)之间拉开明显落差,层次靠 scale 与文本色阶梯,不靠装饰。负字距(-0.01 ~ -0.02em)用于大标题制造紧致的视觉块。
- **JetBrains Mono** 承载一切"机器事实":提交 hash、分支名、镜像 tag、运行 ID、KPI 大数字(`{typography.kpi-number}`)、终端与代码、`@@` hunk 头。等宽让数字与 ID 可对齐、可扫读,也强化"工程"气质。

全大写小标签(`{typography.caps}`,0.04em 字距)用于表头、分组标题、kicker。终端行高放宽到 2,代码 diff 行高约 1.55。中文回退 `"PingFang SC"`。

## Layout & Spacing

间距基于 **4px 单元**(4 / 8 / 12 / 16 / 20 / 24),卡片间距统一 `{spacing.card-gap}`(18px)。

骨架是**固定左侧图标导航轨**(`{spacing.rail}` = 62px,sticky 全高)+ 自适应主区;主区左右内边距 `{spacing.main-pad}`(48px)。**全局规则①:内容铺满宽度**——主内容区最大宽度约 `{spacing.content-max}`(1680px),并与全宽顶栏/tab 左右对齐;**不允许窄居中列在宽屏两侧留下大片空白**。表单/配置类页面在内容过宽时拆为两栏(而非把单列拉宽显空),保持均匀信息密度。流水线编排为"画布 + 右侧配置抽屉(360px)"布局;设置类页面为"左二级导航 + 表单列"(此处右侧留白是有意的标准布局)。

栅格随容器收窄:KPI 4 列→2 列;双栏主/侧(如诊断页 1.62fr / 1fr)在 ≤980px 折叠为单列。

## Elevation & Depth

纵深由**色调分层 + 浮起阴影 + 微辉光**共同表达,而非硬边框。

- **卡片"浮"在背景上**:`{colors.border}`(白 .055)幽灵描边 + 主卡 `{colors.shadow}`(近距 1px + 远距 14px/34px 双层柔影)+ 页背景的品牌色径向辉光(`bg-glow`,右上角微弱电蓝)。
- **模态**用更重的 `{colors.modal-shadow}`(24px/70px),叠在暗化 scrim(可带细网格纹理)之上。
- **凹陷表面**(终端、代码、进度轨)用 `{colors.inner}` 内阴影,营造"嵌进去"的物理感。
- **状态发光**:成功/失败节点用 `0 0 0 4px *-soft` 的色环,失败节点额外加红色外发光 + 闪烁。
- 阴影是层级语言,不是装饰——同层元素阴影一致,不随意叠加。

## Shapes

圆角是温和而克制的工程感,不走 pill 化也不走硬直角:

- `{rounded.sm}`(4px)复选框 · `{rounded.md}`(7px)状态 pill / 徽标 / 分段项 · `{rounded.DEFAULT}`(9px)按钮 / 输入框 / 小图标块 · `{rounded.lg}`(11px)终端 / diff / action 条 · `{rounded.xl}`(14px)模态 / 大卡 · `{rounded.card}`(15px)主内容卡 · `{rounded.full}`(9999px)进度条 / 开关 / 置信度环 / 所有状态圆点。
- 图标统一为细线描边(stroke 1.6–2.4),纯几何、工程/诊断感;圆点(dot)用于状态指示(6–13px)。

## Components

颜色一律走令牌(绿=成功 · 红=失败 · 琥珀=进行中/警告 · 青=AI/信息 · 蓝=主操作)。完整目录见 `mockups/mock-states-components.html`。

- **按钮(`button-*`)** — 四级层级。**Primary**:`{colors.primary}` 实底、白字、34–36px 高、`{rounded.DEFAULT}`,带 `0 5px 16px {colors.primary-soft}` 软投影;hover 切 `{colors.primary-press}` 并 `translateY(-1px)`;loading 时禁点并显白色 spinner;disabled `opacity .45` 去阴影。**Secondary**:`{colors.card-2}` 底 + `{colors.border-strong}` 边,hover 边色提亮。**Ghost**:透明底 + 描边,弱化。**Danger**:`{colors.red-soft}` 底 + `{colors.red}` 字 + `{colors.red-line}` 边。
- **状态徽标 Pill** — 固定语义词汇,**不得换词**:`成功 / 失败 / 进行中 / 部分失败 / 已回滚 / 排队中`。结构 = 6px 圆点 + 文字,`{rounded.md}`,语义 `-soft` 底;**进行中**的圆点带 1.1s 脉冲(`blink`);失败/部分失败额外加 `-line` 边。
- **卡片 / 表面(`card`)** — `{colors.card}` 底 + 幽灵描边 + `{colors.shadow}` + `{rounded.card}`,入场 `translateY(13px)→0` 配 `cubic-bezier(.16,1,.3,1)`。次表面/抬升用 `{colors.card-2}`,凹陷区用 `{colors.inset}`。
- **KPI 卡(`kpi-card`)** — 概览页 4 列。小标签(带 `{colors.primary-soft}` 图标块)+ 等宽大数字(`{typography.kpi-number}`)+ 趋势副行(`{colors.green}`/`{colors.red}` 标涨跌)。失败计数用红;"AI 已诊断"提示用青。见 `mockups/mock-overview.html`。
- **Toast(`toast`)** — 右下角堆叠,340px,`{colors.card-2}` 底 + `{colors.modal-shadow}` + 左侧 3px 语义色条。四型:成功(绿)/ 错误(红)/ 警告(琥珀)/ **AI 信息(青)**。成功与信息 4s 自动消失(底部进度条),错误手动关闭;最多 1 个行动链接(`{colors.primary}`)。
- **内联 Banner** — 页/卡内常驻提示,不自动消失。四型用对应语义 `-soft` 底 + `-line` 边 + 线性图标:信息(青)/警告(琥珀)/错误(红)/成功(绿)。可带末尾行动链接。
- **模态 / 确认框(`modal`)** — `{colors.card}` 底 + `{colors.border-strong}` + `{rounded.xl}` + `{colors.modal-shadow}`,叠在暗化 scrim 上。统一语言:头部(图标块 + 标题/副标题)→ 字段网格 → 底部条(次按钮 + 主按钮,测试类操作居左下,危险操作色异化)。**破坏性操作**(回滚/删除/重启)需二次确认;**高危**(重置实例/删服务器)要求输入名称确认,确认按钮在输入匹配前 disabled。见 `mockups/mock-dialogs.html`。
- **表单控件(`input` / `switch` / `segmented` 等)** — **输入框**:`{colors.inset}` 底 + `{colors.border}` 边 + `{rounded.DEFAULT}`,38px 高;focus = `{colors.primary}` 边 + 3px `{colors.primary-soft}` 光环;error = `{colors.red}` 边 + 3px `{colors.red-soft}` 光环 + 红色字段内联提示。**开关**:on `{colors.primary}` / off `{colors.border-strong}`,`{rounded.full}`,旋钮 18px 滑移。**复选框** `{rounded.sm}` 选中填 primary;**单选**圆形选中显内点。**分段控件**:`{colors.inset}` 轨道,激活项 `{colors.card}` + 阴影。**下拉**为 `{colors.inset}` 触发块 + 末尾箭头。
- **Tabs** — 文字 + 底部 2px 下划线;激活下划线为 `{colors.primary}`,文字提到 `{colors.text}` 且加粗;hover 文字到 `{colors.dim}`。
- **表格 / 列表行** — 全宽 CSS Grid(运行列表为 8 列),表头全大写小标签(`{typography.caps}`)+ `{colors.card-2}` 底;按日期分组(`{colors.inset}` 分组条);行高 ≥56px,hover 切 `{colors.inset}`;**失败行**用 `linear-gradient(90deg, {colors.red-soft}, transparent 64%)` 左侧红渐变并提供"查看诊断"入口;进行中行带脉冲点 + mini 进度条。见 `mockups/mock-runs.html`。
- **流水线节点 + 连线(`pipeline-node` / `pipeline-connector`)** — 详情页为"**穿珠**"线性时间线:一条连续主线串起大小一致的圆节点(13px 圆点),`ok` = 绿 + `0 0 0 4px {colors.green-soft}` 色环,`bad` = 红 + 外发光 + 闪烁,`skip` = `{colors.inset}` 灰,段(seg)用绿/红渐变示意流向。编排页(`mockups/mock-project-pipeline.html`)为画布:任务卡 + **n8n/Dify 风格优雅贝塞尔曲线连接器**——底线 `{colors.primary}`(opacity .55)+ 叠加 `{colors.cyan}` 虚线流光(`stroke-dashoffset` 动画),端口实心圆;选中任务卡描 `{colors.primary}` 边。
- **终端(`terminal`)** — `{colors.term}` 纯黑底 + `{rounded.lg}` + `{colors.inner}` 内阴影;mac 三灯标题栏 + 等宽文件名;等宽日志,行号槽 `{colors.line-num}`,命中行用 `{colors.red-soft}` 高亮、行号转红;`FATAL` 红、`[tag]` 灰、URL 用 `{colors.logblue}`、key 用 `{colors.amber}`。运行中带 SSE 标识 + 闪烁光标。
- **代码 Diff 行(`diff-row-*`)** — GitHub 式红绿统一 diff:新增行 `{colors.green-soft}` 底 + 绿号槽 + `+` 号,删除行 `{colors.red-soft}` 底 + 红号槽 + `−` 号;`@@` hunk 头用 `{colors.cyan-soft}` 底(可带上下文说明);轻量语法着色(kw/str/fn 三色);左文件树每文件带 ±徽标与"新文件"标;AI"与失败相关"标注用青。见 `mockups/mock-code-diff.html`。
- **加载骨架** — 数据加载**用骨架而非转圈遮罩**:`{colors.inset}`→白 .06 的 1.4s shimmer,`{rounded.md}` 占位条/圆。
- **空状态** — 虚线 `{colors.border-strong}` 边 + `{colors.inset}` 底:居中线性图标块 + 一句说明 + **明确 CTA**(primary 按钮)。
- **AI 失败诊断卡(`ai-diagnosis-card`)** — 旗舰组件。卡头标"假说,非结论",标题用 `{colors.primary}` 等宽高亮关键变量;"修复建议"action 条在 `{colors.inset}` 上给一键操作;内嵌证据终端;底部反馈闭环(👍/👎)。**降级**:LLM 不可用时显青色虚线降级卡,明确"核心 CI/CD 不受影响"——AI 永不阻断主流程。图标一律用**活动脉冲线**,**禁用星芒**。
- **置信度环 / 标记(`confidence-meter`)** — 青色胶囊:"置信度 88% · 高" + 一条 `{rounded.full}` 进度条(轨道 = cyan @ .2,填充 = `{colors.cyan}`,入场 `width:0→` 填充动画)。
- **进度 / Spinner** — 确定进度用 primary 实心条;不确定用琥珀流光条或 `{colors.primary}` 转圈 spinner。
- **Tooltip** — `{colors.term}` 暗底 + `{colors.border-strong}` 边,出现在元素上方带小箭头,仅放简短补充。

## Do's and Don'ts

| Do | Don't |
|---|---|
| **内容铺满宽度**(主区 ~1680px、与顶栏对齐);过宽表单拆两栏 | 窄居中列让宽屏两侧大片留白("左右太空") |
| AI 用**克制的青色 + 活动脉冲线图标**表达 | 用 ✦ 星芒等"AI 味"套路图标卖弄 |
| 深色为默认、浅色为**对等**主题(两套都经过设计) | 把浅色当深色的机械反色敷衍了事 |
| 电蓝是唯一品牌色;绿/琥珀/红语义固定 | 引入第二装饰色,或让青色当通用强调色 |
| 状态词汇固定(成功/失败/进行中/部分失败/已回滚/排队) | 随意换词或新造状态文案 |
| 终端/代码用纯黑 `{colors.term}` + 内阴影,坐实"真实日志" | 把日志/代码放进普通浅卡里 |
| 动效只用 `transform`/`opacity`/`clip-path`,且降级 `prefers-reduced-motion` | 动画 `width`/`height`/`top`/`left` 等布局属性,或不可关的炫技动效 |
| 层次靠字阶对比 + 文本色阶梯 + 浮起阴影 | 全局均匀 padding/阴影/字重,层次扁平 |
| 破坏性操作二次确认,高危操作输入名称确认 | 一键执行回滚/删除/重置 |
| AI 不可用时优雅降级、明示不阻断 CI/CD | 让 AI 故障阻断或污染核心运行结果 |
| 信息密度均匀、对比充足、留白平衡——像真实产品截图 | 做成稀疏失衡、低对比的通用后台模板("AI 味") |
