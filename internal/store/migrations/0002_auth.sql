-- 0002 auth:单管理员认证 + 会话表。
-- admin_user 单行约束:CHECK (id = 1) 保证只有一位管理员(单管理员实例)。
-- sessions:服务端会话 token 持久化,token 为 crypto/rand 32B hex。

CREATE TABLE IF NOT EXISTS admin_user (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    username      TEXT    NOT NULL,
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token        TEXT PRIMARY KEY,
    csrf_token   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
