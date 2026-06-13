-- 0039 projects.pac_enabled(MySQL):每项目「流水线即代码」开关(GitOps · FR-8-12 产品化)。
-- 开启后运行时按运行分支读仓库根 .pipewright.yml 驱动该 run(缺失/非法回退库内配置)。默认 0。
ALTER TABLE projects ADD COLUMN pac_enabled TINYINT NOT NULL DEFAULT 0;
