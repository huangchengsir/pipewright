-- 0016 notification_channels(MySQL):多渠道通知配置;敏感字段仅密文 BLOB。
CREATE TABLE IF NOT EXISTS notification_channels (
    id                VARCHAR(64) PRIMARY KEY,
    name              VARCHAR(255) NOT NULL,
    type              VARCHAR(255) NOT NULL,
    enabled           TINYINT NOT NULL DEFAULT 1,
    config_json       LONGTEXT NOT NULL DEFAULT ('{}'),
    secret_ciphertext BLOB,
    created_at        VARCHAR(32) NOT NULL,
    updated_at        VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
