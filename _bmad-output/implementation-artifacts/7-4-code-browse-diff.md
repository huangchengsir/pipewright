# Story 7.4: 只读代码浏览 + 代码 diff(FR-4)

status: ready-for-dev
epic: 7
基线: master(27 story;3-6 source 端点 `GET /api/projects/{id}/source/tree|blob` 已冻结预埋〔本 story 消费〕)
covers: FR-4 只读代码浏览 + 代码 diff —— 前端 Monaco 浏览项目源码;**后端读取端点已在 3-6 预埋,本 story 只做前端**(+ 必要时一个轻量 commits 端点)。
**迁移号:无**

## As a / I want / So that
As a 管理员,I want 在平台内只读浏览项目仓库的代码(目录树 + 文件内容,语法高亮),So that 排查问题时不必切到 IDE/网页 git。

## Acceptance Criteria
1. **Given** 一个项目 **When** 打开代码浏览 **Then** 左侧目录树(消费 3-6 `/source/tree`)+ 右侧文件内容(消费 `/source/blob`,Monaco 只读 + 按扩展名语法高亮)。
2. **Given** 点击目录/文件 **When** 导航 **Then** 树展开/文件加载;二进制文件提示「不可预览」(blob DTO 的 binary=true);大文件 truncated 提示。
3. **And** 克隆失败/degraded → 友好提示「源码暂不可读」,不白屏;路径穿越由后端(3-6)已拦,前端不绕过。
4. **And** 纯只读(无编辑/提交);ref 默认项目默认分支(可选切 ref)。

## ⚠️ 本机约束
- 真实 gitee 仓库本机 GFW 拦 → source 端点对真项目 degraded(空树)。前端须优雅处理 degraded(显「源码暂不可读」而非崩)。组件逻辑可用 file:// 夹具项目验(若起后端指夹具),或以 typecheck + 组件结构 + degraded 态截图为主。**Monaco 体积注意 NFR-4**:动态 import(`import('monaco-editor')`),别进主 bundle 抬高首屏。

## ⛓ 契约(消费 3-6 冻结端点,本 story 不新增后端契约;如做 commits 端点则冻结之)
- 复用 `GET /api/projects/{id}/source/tree?ref=&path=` 与 `/source/blob?ref=&path=`(3-6 冻结:tree `{ref,path,entries:[{name,path,type,size?}]}`;blob `{ref,path,size,binary,truncated,content}`)。
- **可选**:`GET /api/projects/{id}/source/commits?ref=&limit=`(轻量提交列表,供 ref/diff 选择)——若做,冻结 `{commits:[{sha,message,author,date}]}`;**不做也可**(本期 ref 默认分支即可,diff 由 7-3 提供)。作者定:**本期最小做代码浏览,commits/diff 视图复用 7-3 的 diff 数据,不新增后端。**

> **冻结点:** 本 story 主要是前端;若新增 commits 端点则冻结其形状。不改 3-6 source 契约。

## 文件切分(独立 worktree;以前端为主)
**后端(Go):** 默认**不改**(消费 3-6 既有端点)。若决定加 commits 端点:`internal/httpapi/source.go` append handler + 3-6 SourceReader append 一个 commits 读法 + router append;**否则后端零改动**。
**前端(web/):**
- `web/src/api/source.ts`(新):`getSourceTree(projectId, {ref,path})` / `getSourceBlob(projectId, {ref,path})`(对接 3-6 端点 DTO)。
- `web/src/components/code/CodeTree.vue`(新):目录树(懒展开,点目录拉子树、点文件 emit)。
- `web/src/components/code/CodeViewer.vue`(新):Monaco 只读(**动态 import monaco**,按扩展名设 language;binary→「不可预览」;truncated 提示;loading/degraded 态)。
- `web/src/views/ProjectCode.vue`(新):双栏布局(CodeTree + CodeViewer)+ ref 显示;degraded 友好态。
- 路由 + 入口:**仅 append** 一个项目「代码」入口/路由(如 `/projects/:id/code`),别动既有项目页/流水线 tab。
- **Monaco 依赖**:若仓库未装 monaco,`npm i monaco-editor` 装(只前端 devDep/dep);动态 import 避免抬主 bundle。**若装 monaco 影响构建/体积过大,退化用轻量高亮(highlight.js / Shiki)或纯 <pre> + 行号**——优先可用,体积可控。

## Dev Notes 护栏
- **纯只读**:无编辑/写;路径穿越/SSRF 由 3-6 后端已拦,前端不构造绕过路径。
- **NFR-4 体积**:Monaco/高亮库**动态 import**,不进主 bundle;若体积失控退化方案。
- degraded 优雅(源码不可读不白屏);二进制/大文件提示。
- 复用 1-6 ui;组件 typecheck 通过。

## 编排者亲验清单
- 前端 `npm --prefix web run typecheck`(必过)+ build;**关注 bundle 体积**(monaco 是否动态分包、主 bundle 未暴涨)。
- 若改后端:`export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/test -race/build。
- 真验(尽力):起后端指 file:// 夹具项目 → 代码浏览树/文件渲染 + 语法高亮;真 gitee 项目 degraded→「源码暂不可读」友好态;二进制文件「不可预览」。**截图肉眼审**(本项目铁律:看截图非正则)。
- worktree 内 commit(分支 story/7-4)。
