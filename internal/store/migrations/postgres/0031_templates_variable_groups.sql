-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS pipeline_templates (
    id          TEXT COLLATE "C" PRIMARY KEY,
    name        TEXT COLLATE "C" NOT NULL,
    description TEXT COLLATE "C" NOT NULL DEFAULT '',
    stages_json TEXT COLLATE "C" NOT NULL DEFAULT '[]',
    created_at  TEXT COLLATE "C" NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL
);


CREATE UNIQUE INDEX IF NOT EXISTS idx_pipeline_templates_name ON pipeline_templates (name);








CREATE TABLE IF NOT EXISTS variable_groups (
    id          TEXT COLLATE "C" PRIMARY KEY,
    name        TEXT COLLATE "C" NOT NULL,
    description TEXT COLLATE "C" NOT NULL DEFAULT '',
    vars_json   TEXT COLLATE "C" NOT NULL DEFAULT '[]',
    created_at  TEXT COLLATE "C" NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_variable_groups_name ON variable_groups (name);
