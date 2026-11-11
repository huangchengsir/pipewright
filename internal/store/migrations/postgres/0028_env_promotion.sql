-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS project_environments (
    project_id  TEXT COLLATE "C" PRIMARY KEY,
    chain_json  TEXT COLLATE "C" NOT NULL DEFAULT '[]',
    updated_at  TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);




CREATE TABLE IF NOT EXISTS environment_variables (
    id            TEXT COLLATE "C" PRIMARY KEY,
    project_id    TEXT COLLATE "C" NOT NULL,
    environment   TEXT COLLATE "C" NOT NULL,
    var_key       TEXT COLLATE "C" NOT NULL,
    value         TEXT COLLATE "C" NOT NULL DEFAULT '',
    is_secret     BIGINT NOT NULL DEFAULT 0,
    credential_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    updated_at    TEXT COLLATE "C" NOT NULL,
    UNIQUE (project_id, environment, var_key),
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_env_vars_proj_env
    ON environment_variables (project_id, environment);





CREATE TABLE IF NOT EXISTS run_promotions (
    id                 TEXT COLLATE "C" PRIMARY KEY,
    project_id         TEXT COLLATE "C" NOT NULL,
    source_run_id      TEXT COLLATE "C" NOT NULL,
    from_environment   TEXT COLLATE "C" NOT NULL DEFAULT '',
    target_environment TEXT COLLATE "C" NOT NULL,
    status             TEXT COLLATE "C" NOT NULL DEFAULT 'pending',
    approval_stage     TEXT COLLATE "C" NOT NULL DEFAULT '',
    promoted_by        TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at         TEXT COLLATE "C" NOT NULL,
    decided_at         TEXT COLLATE "C" NOT NULL DEFAULT '',
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    FOREIGN KEY (source_run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_promotions_proj ON run_promotions (project_id);
CREATE INDEX IF NOT EXISTS idx_run_promotions_run ON run_promotions (source_run_id);
