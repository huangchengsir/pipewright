-- 0027 run_test_reports(MySQL):测试报告 + 质量门禁。REAL→DOUBLE。
CREATE TABLE IF NOT EXISTS run_test_reports (
    id             VARCHAR(64) PRIMARY KEY,
    run_id         VARCHAR(64) NOT NULL,
    stage_id       VARCHAR(64) NOT NULL DEFAULT '',
    stage_name     VARCHAR(255) NOT NULL DEFAULT '',
    format         VARCHAR(255) NOT NULL DEFAULT 'junit',
    total          BIGINT NOT NULL DEFAULT 0,
    passed         BIGINT NOT NULL DEFAULT 0,
    failed         BIGINT NOT NULL DEFAULT 0,
    skipped        BIGINT NOT NULL DEFAULT 0,
    duration_sec   DOUBLE NOT NULL DEFAULT 0,
    coverage_pct   DOUBLE NOT NULL DEFAULT -1,
    gate_enabled   TINYINT NOT NULL DEFAULT 0,
    gate_passed    TINYINT NOT NULL DEFAULT 1,
    gate_reason    VARCHAR(2048) NOT NULL DEFAULT '',
    created_at     VARCHAR(32) NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_run_test_reports_run ON run_test_reports (run_id);
