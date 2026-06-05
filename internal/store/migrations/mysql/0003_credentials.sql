-- 0003 credentials(MySQL):凭据加密保险库,仅存密文 BLOB + 掩码 + 元数据。
CREATE TABLE IF NOT EXISTS credentials (
    id           VARCHAR(64) PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    type         VARCHAR(255) NOT NULL,
    scope        VARCHAR(255) NOT NULL DEFAULT '',
    ciphertext   BLOB NOT NULL,
    masked_value VARCHAR(255) NOT NULL,
    last_used_at VARCHAR(32),
    created_at   VARCHAR(32) NOT NULL,
    updated_at   VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
