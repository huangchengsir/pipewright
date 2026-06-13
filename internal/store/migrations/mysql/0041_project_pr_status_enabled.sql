-- 0041 projects.pr_status_enabled(MySQL):每项目「PR 状态检查」开关(Story 8-9 / FR-8-9 产品化)。
-- 开启后 run 终态据项目仓库识别 GitHub/Gitee,经项目凭据回写该 commit 的提交状态(PR 检查)。默认 0。
ALTER TABLE projects ADD COLUMN pr_status_enabled TINYINT NOT NULL DEFAULT 0;
