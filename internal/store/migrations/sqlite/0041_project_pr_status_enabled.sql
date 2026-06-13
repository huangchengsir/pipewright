-- 0041 projects.pr_status_enabled:每项目「PR 状态检查」开关(Story 8-9 / FR-8-9 产品化)。
-- 开启后,run 终态时据项目仓库识别 GitHub/Gitee,经项目凭据回写该 commit 的提交状态(PR 检查)。
-- 默认 0(关闭)→ 老项目行为不变,不会意外往真实仓库回写。历史全局 env PIPEWRIGHT_PR_STATUS=1
-- 仍可全局强开(无视每项目开关)。best-effort:失败仅记日志,绝不影响运行终态。
ALTER TABLE projects ADD COLUMN pr_status_enabled INTEGER NOT NULL DEFAULT 0;
