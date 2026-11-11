-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS run_logs (
    id           TEXT COLLATE "C" PRIMARY KEY,
    run_id       TEXT COLLATE "C" NOT NULL,
    seq          BIGINT NOT NULL,
    ts           TEXT COLLATE "C" NOT NULL,
    stream       TEXT COLLATE "C" NOT NULL,
    step_ordinal BIGINT NOT NULL,
    text         TEXT COLLATE "C" NOT NULL
);


CREATE INDEX IF NOT EXISTS idx_run_logs_run_seq ON run_logs (run_id, seq);
