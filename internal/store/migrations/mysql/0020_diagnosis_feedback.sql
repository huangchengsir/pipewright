-- 0020 diagnosis feedback(MySQL):每 run 一条 AI 诊断反馈(run_id UNIQUE upsert)。
CREATE TABLE IF NOT EXISTS diagnosis_feedback (
    id                 VARCHAR(64) PRIMARY KEY,
    run_id             VARCHAR(64) NOT NULL UNIQUE,
    verdict            VARCHAR(255) NOT NULL,
    correct_root_cause LONGTEXT NOT NULL DEFAULT (''),
    diagnosis_snapshot LONGTEXT NOT NULL DEFAULT (''),
    created_at         VARCHAR(32) NOT NULL,
    updated_at         VARCHAR(32) NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_diagnosis_feedback_created_at ON diagnosis_feedback (created_at);
