-- 0025 run_approvals:人工审批门记录(Epic 8 · Story 8-4)。
-- 每个「运行×审批门阶段」一行;记录待批/已批准/已拒绝 + 审批人 + 决定时刻,供 UI 展示 + 审计 + 重启清理。
-- 不存敏感信息;删运行级联删审批记录。
--   id          : uuid v4
--   run_id      : FK → pipeline_runs.id;ON DELETE CASCADE
--   stage_id    : 审批门阶段 id(spec 内)
--   stage_name  : 阶段展示名(冗余,便于列表展示)
--   status      : pending | approved | rejected
--   decided_by  : 审批人(approved/rejected 时);timeout/canceled 亦记于此
--   decided_at  : 决定时刻(RFC3339;pending 时空)
--   created_at  : 待批创建时刻

CREATE TABLE IF NOT EXISTS run_approvals (
    id         TEXT PRIMARY KEY,
    run_id     TEXT NOT NULL,
    stage_id   TEXT NOT NULL,
    stage_name TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',
    decided_by TEXT NOT NULL DEFAULT '',
    decided_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (run_id, stage_id),
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_approvals_run ON run_approvals (run_id);
