-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS notification_templates (
    id             TEXT COLLATE "C" PRIMARY KEY,
    project_id     TEXT COLLATE "C",
    event          TEXT COLLATE "C" NOT NULL,
    channel_id     TEXT COLLATE "C",
    title_template TEXT COLLATE "C" NOT NULL DEFAULT '',
    body_template  TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at     TEXT COLLATE "C" NOT NULL,
    FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notification_templates_event ON notification_templates (event);
