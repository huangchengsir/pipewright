-- 0019 notification_templates(MySQL):通知标题/正文模板。
CREATE TABLE IF NOT EXISTS notification_templates (
    id             VARCHAR(64) PRIMARY KEY,
    project_id     VARCHAR(64),
    event          VARCHAR(255) NOT NULL,
    channel_id     VARCHAR(64),
    title_template LONGTEXT NOT NULL DEFAULT (''),
    body_template  LONGTEXT NOT NULL DEFAULT (''),
    created_at     VARCHAR(32) NOT NULL,
    FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_notification_templates_event ON notification_templates (event);
