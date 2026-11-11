-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS anomaly_rules (
    id         TEXT COLLATE "C" PRIMARY KEY,
    metric     TEXT COLLATE "C" NOT NULL,
    operator   TEXT COLLATE "C" NOT NULL,
    threshold  DOUBLE PRECISION NOT NULL,
    server_id  TEXT COLLATE "C",
    enabled    BIGINT NOT NULL DEFAULT 1,
    created_at TEXT COLLATE "C" NOT NULL
);

CREATE TABLE IF NOT EXISTS anomaly_alerts (
    id          TEXT COLLATE "C" PRIMARY KEY,
    server_id   TEXT COLLATE "C" NOT NULL,
    server_name TEXT COLLATE "C" NOT NULL,
    metric      TEXT COLLATE "C" NOT NULL,
    operator    TEXT COLLATE "C" NOT NULL,
    threshold   DOUBLE PRECISION NOT NULL,
    value       DOUBLE PRECISION NOT NULL,
    message     TEXT COLLATE "C" NOT NULL,
    created_at  TEXT COLLATE "C" NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_anomaly_alerts_created_at ON anomaly_alerts (created_at);
CREATE INDEX IF NOT EXISTS idx_anomaly_alerts_server_id ON anomaly_alerts (server_id);
