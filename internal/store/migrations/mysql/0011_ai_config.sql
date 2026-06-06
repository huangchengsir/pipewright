-- 0011 ai_config(MySQL):单例 AI 配置(id=1);apiKey 仅密文 BLOB 入库。
CREATE TABLE IF NOT EXISTS ai_config (
    id                  INT PRIMARY KEY CHECK (id = 1),
    provider            VARCHAR(255) NOT NULL DEFAULT '',
    base_url            VARCHAR(1024) NOT NULL DEFAULT '',
    model               VARCHAR(255) NOT NULL DEFAULT '',
    api_key_ciphertext  BLOB,
    budget_json         LONGTEXT NOT NULL DEFAULT ('{"monthlyTokenLimit":null}'),
    enabled             TINYINT NOT NULL DEFAULT 0,
    created_at          VARCHAR(32) NOT NULL,
    updated_at          VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
