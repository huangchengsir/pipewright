-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS preview_configs (
    project_id      TEXT COLLATE "C" PRIMARY KEY,
    enabled         BIGINT NOT NULL DEFAULT 0,
    dns_provider_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    base_domain     TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at      TEXT COLLATE "C" NOT NULL,
    updated_at      TEXT COLLATE "C" NOT NULL
);







CREATE TABLE IF NOT EXISTS preview_envs (
    id           TEXT COLLATE "C" PRIMARY KEY,
    project_id   TEXT COLLATE "C" NOT NULL,
    pipeline_id  TEXT COLLATE "C" NOT NULL DEFAULT '',
    pr_number    BIGINT NOT NULL,
    branch       TEXT COLLATE "C" NOT NULL DEFAULT '',
    server_id    TEXT COLLATE "C" NOT NULL DEFAULT '',
    route_id     TEXT COLLATE "C" NOT NULL DEFAULT '',
    subdomain    TEXT COLLATE "C" NOT NULL DEFAULT '',
    status       TEXT COLLATE "C" NOT NULL DEFAULT 'active',
    created_at   TEXT COLLATE "C" NOT NULL,
    reclaimed_at TEXT COLLATE "C" NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_preview_envs_project_pr ON preview_envs (project_id, pr_number);
CREATE INDEX IF NOT EXISTS idx_preview_envs_project ON preview_envs (project_id);
CREATE INDEX IF NOT EXISTS idx_preview_envs_status ON preview_envs (status);
