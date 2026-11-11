-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS metric_samples (
    id             TEXT COLLATE "C" PRIMARY KEY,
    server_id      TEXT COLLATE "C" NOT NULL,
    cpu_percent    DOUBLE PRECISION,
    memory_percent DOUBLE PRECISION,
    disk_percent   DOUBLE PRECISION,
    sampled_at     TEXT COLLATE "C" NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metric_samples_server_time ON metric_samples (server_id, sampled_at);
CREATE INDEX IF NOT EXISTS idx_metric_samples_time ON metric_samples (sampled_at);
