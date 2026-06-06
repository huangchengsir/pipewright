-- 0024 project_crons:项目级定时(cron)触发配置(Epic 8 · Story 8-6 / FR-8-4)。
-- 每项目至多一行(project_id 既是主键也是外键)。expression 为 5 字段 cron(分 时 日 月 周);
-- branch 为定时触发要构建的分支(空 = 项目默认分支,由 run.Create 解析);enabled 控制是否参与调度。
-- 不存敏感信息;删项目级联删定时配置。
--   project_id : FK → projects.id;PK;ON DELETE CASCADE
--   expression : 5 字段 cron 表达式(应用层 cron.Parse 校验)
--   branch     : 触发构建的分支(可空)
--   enabled    : 0/1 是否启用调度

CREATE TABLE IF NOT EXISTS project_crons (
    project_id TEXT PRIMARY KEY,
    expression TEXT NOT NULL DEFAULT '',
    branch     TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

-- 调度器按 enabled 过滤扫描;建索引避免全表扫。
CREATE INDEX IF NOT EXISTS idx_project_crons_enabled ON project_crons (enabled);
