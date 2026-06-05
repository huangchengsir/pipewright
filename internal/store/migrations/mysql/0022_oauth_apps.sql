-- 0022 oauth_apps(MySQL):每 provider OAuth 应用配置;client_secret 仅密文 BLOB。
CREATE TABLE IF NOT EXISTS oauth_apps (
    provider                 VARCHAR(64) PRIMARY KEY,
    client_id                VARCHAR(255) NOT NULL DEFAULT '',
    client_secret_ciphertext BLOB,
    base_url                 VARCHAR(1024) NOT NULL DEFAULT '',
    enabled                  TINYINT NOT NULL DEFAULT 0,
    created_at               VARCHAR(32) NOT NULL,
    updated_at               VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
