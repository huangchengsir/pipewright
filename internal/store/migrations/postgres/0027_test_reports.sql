-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS run_test_reports (
    id             TEXT COLLATE "C" PRIMARY KEY,
    run_id         TEXT COLLATE "C" NOT NULL,
    stage_id       TEXT COLLATE "C" NOT NULL DEFAULT '',
    stage_name     TEXT COLLATE "C" NOT NULL DEFAULT '',
    format         TEXT COLLATE "C" NOT NULL DEFAULT 'junit',
    total          BIGINT NOT NULL DEFAULT 0,
    passed         BIGINT NOT NULL DEFAULT 0,
    failed         BIGINT NOT NULL DEFAULT 0,
    skipped        BIGINT NOT NULL DEFAULT 0,
    duration_sec   DOUBLE PRECISION NOT NULL DEFAULT 0,
    coverage_pct   DOUBLE PRECISION NOT NULL DEFAULT -1,
    gate_enabled   BIGINT NOT NULL DEFAULT 0,
    gate_passed    BIGINT NOT NULL DEFAULT 1,
    gate_reason    TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at     TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_test_reports_run ON run_test_reports (run_id);
