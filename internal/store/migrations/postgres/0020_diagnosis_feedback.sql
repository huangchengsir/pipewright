-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS diagnosis_feedback (
    id                 TEXT COLLATE "C" PRIMARY KEY,
    run_id             TEXT COLLATE "C" NOT NULL UNIQUE,
    verdict            TEXT COLLATE "C" NOT NULL,
    correct_root_cause TEXT COLLATE "C" NOT NULL DEFAULT '',
    diagnosis_snapshot TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at         TEXT COLLATE "C" NOT NULL,
    updated_at         TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_diagnosis_feedback_created_at ON diagnosis_feedback (created_at);
