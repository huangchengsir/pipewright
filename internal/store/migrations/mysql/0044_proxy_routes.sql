-- 0044 proxy_routes(MySQL):自动 HTTPS + 域名反向代理(R1)的路由表。
-- 每行 = 一条「域名 → 目标主机上某容器:端口」的反代路由;Pipewright 据此在目标主机上经
-- target.Service(SSH+docker)编排 Caddy 容器(渲染 Caddyfile + reload),Caddy 自动经
-- Let's Encrypt(HTTP-01)签发并续期证书。tls_mode 本期只 'auto';enabled 控制是否渲染进 Caddyfile。
-- cert_status(pending|issued|failed)由 RefreshStatus 经 TLS 握手探测回写;cert_detail 存人话原因。
-- server_id 不设外键(与 metric_samples 一致);domain 全局唯一(同域名只能绑一条路由)。
CREATE TABLE IF NOT EXISTS proxy_routes (
    id                 VARCHAR(64) PRIMARY KEY,
    server_id          VARCHAR(64) NOT NULL,
    domain             VARCHAR(253) NOT NULL,
    upstream_container VARCHAR(255) NOT NULL,
    upstream_port      INT NOT NULL,
    tls_mode           VARCHAR(16) NOT NULL DEFAULT 'auto',
    enabled            TINYINT NOT NULL DEFAULT 1,
    cert_status        VARCHAR(16) NOT NULL DEFAULT 'pending',
    cert_detail        TEXT NULL,
    created_at         VARCHAR(32) NOT NULL,
    updated_at         VARCHAR(32) NOT NULL,
    UNIQUE INDEX idx_proxy_routes_domain (domain),
    INDEX idx_proxy_routes_server (server_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
