-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS run_artifacts (
    id            TEXT COLLATE "C" PRIMARY KEY,
    run_id        TEXT COLLATE "C" NOT NULL,
    type          TEXT COLLATE "C" NOT NULL,
    name          TEXT COLLATE "C" NOT NULL,
    reference     TEXT COLLATE "C" NOT NULL,
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    metadata_json TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at    TEXT COLLATE "C" NOT NULL,


    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);


CREATE INDEX IF NOT EXISTS idx_run_artifacts_run ON run_artifacts (run_id);
