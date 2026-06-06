-- 0033 custom_nodes(MySQL):可复用单节点定义;name 唯一。
CREATE TABLE IF NOT EXISTS custom_nodes (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(191) NOT NULL,
    description VARCHAR(2048) NOT NULL DEFAULT '',
    node_type   VARCHAR(255) NOT NULL,
    summary     VARCHAR(2048) NOT NULL DEFAULT '',
    config_json LONGTEXT NOT NULL DEFAULT ('{}'),
    created_at  VARCHAR(32) NOT NULL,
    updated_at  VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE UNIQUE INDEX idx_custom_nodes_name ON custom_nodes (name);
