<!-- sky-aitest:begin -->
## sky-aitest 测试中枢路由规则（自动注入，勿手改）

本项目已安装 `sky-aitest` 插件。涉及测试设计 / 执行的请求，按以下规则编排：

**顶层意图分诊**（每轮先判）：
1. 显式 `/sky-aitest design` / `/sky-aitest exec` 优先。
2. 否则按语义 + case 字段：
   - 写用例 / 拆点 / 方案 / 评审 / 需求 / PRD → **design mode**。
   - 跑 / 执行 / 回放 / 回归 / 创单 / 含 `cases/` `scripts/` `platform` `app_launch` → **exec mode**。
   - 既设计又执行 → **design → exec 一条龙**。
3. 拿不准用一句话确认，不猜。

**design mode**：拆点 → 双源检索 → 12 列用例 → 四维度评审 → 四档门禁写 formengine（走 MCP）。重活下沉 `sky-case-*` subagent。

**exec mode**：读 `${CLAUDE_PLUGIN_ROOT}/skills/sky-aitest/methodology/exec/compile-and-route.md` 路由到 REPLAY / E2E_CODEGEN / PLAYWRIGHT_AGENT / EXPLORE / COMPUTER_USE。EXPLORE 走本会话（有状态），其余重活下沉。失败先业务级归因不无限重试。结果默认回写 formengine。
- **起手拉资产**（落共享数据目录，非项目）：`ENGINE=${CLAUDE_PLUGIN_ROOT}/skills/sky-aitest/engine/exec`；`node $ENGINE/scripts/tools/asset-library.js pull <site>` + `ASSETS=$(node $ENGINE/scripts/tools/asset-library.js datadir)`，从 `$ASSETS/{helpers,lib,bootstrap,cases}` 复用；资产在 `${CLAUDE_PLUGIN_DATA}`（跨项目跨版本共享，更新插件不丢）；未命中才从零探索。
- **收尾回传**：新资产拷进 `$ASSETS` 后 `asset-library.js prepare-push <site>`（脱敏 + 打印回写指令，不自动 push，用户确认提 PR）。

**工作区铁律**：exec 一切产物（cases/runs/results/scripts/registry/anchors 等）全在 `<项目根>/.sky-aitest/` 下，**不散到项目根**。**cwd 保持项目根、不 cd**（AI 会忘 cd、Bash cwd 每次重置）；所有工作区路径**显式带 `.sky-aitest/` 前缀**（如截图写 `.sky-aitest/runs/{id}/x.png`，别写裸 `runs/`）。引擎写盘点已硬编码到 `.sky-aitest/`。只有 `.claude/`、`CLAUDE.md`、`.mcp.json` 留根。`.sky-aitest/ auth/ .mcp.json seed.spec.ts runs/ results/ test-results/` 已 gitignore。

**铁律**：
- 非阻塞操作（浏览器 click/input、读写 runs/results、建目录）直接执行不询问；仅登录失效 / 验证码 / 预算耗尽 / 真实数据风险 才停下。
- **探索一律 browser-use CLI**（`run_explore_bridge.js` 包装，逐步看真实 DOM），**禁止直接写 Playwright 脚本探索**（慢、证据散）。
- Playwright spec 失败 / 弹窗首现 / `@tested` 不含当前环境 → 立刻切 browser-use CLI 看真实 DOM，禁猜 selector 重试。
- 失败或被纠正后触发 self-improve，回流到 Skill / 规范 / helper / 锚点。
- 引擎经 `${CLAUDE_PLUGIN_ROOT}` 引用不复制；云效回放包须 vendor engine 自包含。
<!-- sky-aitest:end -->
