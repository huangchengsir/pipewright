-- 0015 servers(MySQL):目标服务器,按引用绑定 SSH 凭据。user 为保留字需反引号。
CREATE TABLE IF NOT EXISTS servers (
    id            VARCHAR(64) PRIMARY KEY,
    name          VARCHAR(255) NOT NULL,
    host          VARCHAR(255) NOT NULL,
    port          BIGINT NOT NULL DEFAULT 22,
    `user`        VARCHAR(255) NOT NULL,
    credential_id VARCHAR(64) NOT NULL,
    created_at    VARCHAR(32) NOT NULL,
    updated_at    VARCHAR(32) NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_servers_credential_id ON servers (credential_id);
