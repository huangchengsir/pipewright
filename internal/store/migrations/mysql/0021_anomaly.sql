-- 0021 anomaly(MySQL):阈值规则 + 告警记录。REAL→DOUBLE;value 为保留字需反引号。
CREATE TABLE IF NOT EXISTS anomaly_rules (
    id         VARCHAR(64) PRIMARY KEY,
    metric     VARCHAR(255) NOT NULL,
    operator   VARCHAR(255) NOT NULL,
    threshold  DOUBLE NOT NULL,
    server_id  VARCHAR(64),
    enabled    TINYINT NOT NULL DEFAULT 1,
    created_at VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS anomaly_alerts (
    id          VARCHAR(64) PRIMARY KEY,
    server_id   VARCHAR(64) NOT NULL,
    server_name VARCHAR(255) NOT NULL,
    metric      VARCHAR(255) NOT NULL,
    operator    VARCHAR(255) NOT NULL,
    threshold   DOUBLE NOT NULL,
    `value`     DOUBLE NOT NULL,
    message     VARCHAR(2048) NOT NULL,
    created_at  VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_anomaly_alerts_created_at ON anomaly_alerts (created_at);
CREATE INDEX idx_anomaly_alerts_server_id ON anomaly_alerts (server_id);
