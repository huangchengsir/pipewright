-- 0006 runs:流水线运行模型 + 步骤(FR-13 / Story 3.1)。
-- 一次流水线运行 = pipeline_runs 一行;其穿珠时间线节点 = run_steps 多行(ordinal 排序)。
-- 由进程内 worker pool 调度,状态机 queued→running→success|failed|partial_failed|rolled_back。
-- 真实构建/部署执行(=3-3/4-x)与日志内容流(=3-6)不在本期;本期 worker 跑桩 runner。
--
-- pipeline_runs:
--   id             : uuid v4;PK
--   project_id     : FK → projects.id;ON DELETE CASCADE(删项目级联删运行)
--   status         : queued | running | success | failed | partial_failed | rolled_back(应用层状态机校验)
--   trigger_type   : webhook | manual(触发来源;真实触发=3-2)
--   trigger_branch : 触发分支(如 main)
--   trigger_commit : 触发提交短 SHA(可空)
--   trigger_actor  : 触发者(admin | gitee 等)
--   created_at     : 入队时刻 RFC3339
--   started_at     : 转 running 时刻 RFC3339;NULL 表示尚未开始
--   finished_at    : 转终态时刻 RFC3339;NULL 表示未结束
--
-- run_steps:
--   id          : uuid v4;PK
--   run_id      : FK → pipeline_runs.id;ON DELETE CASCADE
--   name        : 步骤展示名(如 拉取源码)
--   status      : pending | running | success | failed | skipped
--   ordinal     : 步骤顺序(从 0 递增;穿珠时间线渲染序)
--   started_at  : RFC3339;NULL 未开始
--   finished_at : RFC3339;NULL 未结束

CREATE TABLE IF NOT EXISTS pipeline_runs (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'queued',
    trigger_type   TEXT NOT NULL DEFAULT 'manual',
    trigger_branch TEXT NOT NULL DEFAULT '',
    trigger_commit TEXT NOT NULL DEFAULT '',
    trigger_actor  TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    started_at     TEXT,
    finished_at    TEXT,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS run_steps (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    ordinal     INTEGER NOT NULL DEFAULT 0,
    started_at  TEXT,
    finished_at TEXT,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_runs_project_id ON pipeline_runs (project_id);
CREATE INDEX IF NOT EXISTS idx_run_steps_run_id ON run_steps (run_id);
