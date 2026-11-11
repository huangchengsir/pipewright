-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS proxy_routes (
    id                 TEXT COLLATE "C" PRIMARY KEY,
    server_id          TEXT COLLATE "C" NOT NULL,
    domain             TEXT COLLATE "C" NOT NULL,
    upstream_container TEXT COLLATE "C" NOT NULL,
    upstream_port      BIGINT NOT NULL,
    tls_mode           TEXT COLLATE "C" NOT NULL DEFAULT 'auto',
    enabled            BIGINT NOT NULL DEFAULT 1,
    cert_status        TEXT COLLATE "C" NOT NULL DEFAULT 'pending',
    cert_detail        TEXT COLLATE "C",
    created_at         TEXT COLLATE "C" NOT NULL,
    updated_at         TEXT COLLATE "C" NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_routes_domain ON proxy_routes (domain);
CREATE INDEX IF NOT EXISTS idx_proxy_routes_server ON proxy_routes (server_id);
