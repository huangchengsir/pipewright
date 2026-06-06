-- 0002 auth(MySQL):单管理员认证 + 会话表。admin_user 单行约束 CHECK(id=1)(8.0.16+ 强制)。
CREATE TABLE IF NOT EXISTS admin_user (
    id            INT PRIMARY KEY CHECK (id = 1),
    username      VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at    VARCHAR(32)  NOT NULL,
    updated_at    VARCHAR(32)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS sessions (
    token        VARCHAR(191) PRIMARY KEY,
    csrf_token   VARCHAR(191) NOT NULL,
    created_at   VARCHAR(32)  NOT NULL,
    expires_at   VARCHAR(32)  NOT NULL,
    last_seen_at VARCHAR(32)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
