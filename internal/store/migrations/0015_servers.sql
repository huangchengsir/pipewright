-- 0015 servers:被登记的目标服务器(FR-14 / Story 4.1)。
-- 一台服务器按引用绑定一条 SSH 凭据:仅存 credential_id 外键,绝无任何明文/私钥/口令
-- (AC-SEC-01 / AC-SEC-02)。Exec/Test 所用的 SSH 密钥由 internal/target 经
-- vault.OpenSecret(credential_id) 在进程内取明文,用完即弃,绝不入库/日志/响应/错误体。
--
-- 本层为 Epic 4 部署 + Epic 6 运维**共享**的通用 SSH exec/session 基座,不含部署语义。
--   id            : uuid v4
--   name          : 服务器展示名(如 web-prod-1)
--   host          : 主机名 / IP
--   port          : SSH 端口(默认 22)
--   user          : SSH 登录用户名
--   credential_id : FK → credentials.id;ON DELETE RESTRICT:仍被服务器引用的凭据不可删
--                   (防悬挂引用:删凭据会令该服务器再不可连)

CREATE TABLE IF NOT EXISTS servers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    host          TEXT NOT NULL,
    port          INTEGER NOT NULL DEFAULT 22,
    user          TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_servers_credential_id ON servers (credential_id);
