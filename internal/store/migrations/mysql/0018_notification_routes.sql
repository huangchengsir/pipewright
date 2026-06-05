-- 0018 notification_routes(MySQL):事件→渠道路由。
CREATE TABLE IF NOT EXISTS notification_routes (
    id          VARCHAR(64) PRIMARY KEY,
    project_id  VARCHAR(64),
    event       VARCHAR(255) NOT NULL,
    channel_id  VARCHAR(64) NOT NULL,
    enabled     TINYINT NOT NULL DEFAULT 1,
    created_at  VARCHAR(32) NOT NULL,
    FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_notification_routes_event ON notification_routes (event);
