-- 0029 project_concurrency:项目级并发上限配置(FR-8-10 并发/队列控制)。
-- 每项目至多一行(project_id 既是主键也是外键)。max_concurrent 为该项目同时 running 的运行数上限;
-- 0 = 不限项目级(仅受全局 PIPEWRIGHT_MAX_CONCURRENT 约束)。超限的运行保持 queued 直到该项目有空槽。
-- 不存敏感信息;删项目级联删并发配置。
--   project_id     : FK → projects.id;PK;ON DELETE CASCADE
--   max_concurrent : >=0 项目级同时运行上限(0 = 不限项目级,沿用全局上限)

CREATE TABLE IF NOT EXISTS project_concurrency (
    project_id     TEXT PRIMARY KEY,
    max_concurrent INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
