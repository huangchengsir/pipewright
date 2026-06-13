-- 0039 projects.pac_enabled:每项目「流水线即代码」开关(GitOps · FR-8-12 产品化)。
-- 开启后,运行时按本次运行分支读仓库根 .pipewright.yml 驱动该 run(缺失/非法则回退库内
-- 配置,绝不中断运行)。默认 0(关闭)→ 老项目行为不变。引擎(pacloader)读此列决定是否覆盖。
ALTER TABLE projects ADD COLUMN pac_enabled INTEGER NOT NULL DEFAULT 0;
