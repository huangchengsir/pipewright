-- 0044 proxy_routes:自动 HTTPS + 域名反向代理(R1)的路由表。
-- 每行 = 一条「域名 → 目标主机上某容器:端口」的反代路由。Pipewright 据此在目标主机上经
-- target.Service(SSH+docker)编排一个 Caddy 容器:渲染 Caddyfile + reload,Caddy 见到带域名
-- 的站点块即自动经 Let's Encrypt(HTTP-01)签发并续期证书(零额外指令)。
--
-- tls_mode 本期只 'auto'(LE);enabled 控制该路由是否渲染进 Caddyfile(停用 = 从反代摘除但留库)。
-- cert_status(pending|issued|failed)由 RefreshStatus 经 TLS 握手探测域名:443 回写;cert_detail
-- 存人话失败原因/到期等(DNS 未指向、80 不可达、LE 失败……),绝不含任何敏感信息。
-- server_id 不设外键:与 metric_samples 等保持一致(删 server 由上层/保留清理收敛,避免级联误删)。
-- domain 全局唯一:同一域名只能绑一条路由(避免两条路由冲突签发同一证书)。
CREATE TABLE IF NOT EXISTS proxy_routes (
    id                 TEXT PRIMARY KEY,
    server_id          TEXT NOT NULL,
    domain             TEXT NOT NULL,
    upstream_container TEXT NOT NULL,
    upstream_port      INTEGER NOT NULL,
    tls_mode           TEXT NOT NULL DEFAULT 'auto',
    enabled            INTEGER NOT NULL DEFAULT 1,
    cert_status        TEXT NOT NULL DEFAULT 'pending',
    cert_detail        TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_routes_domain ON proxy_routes (domain);
CREATE INDEX IF NOT EXISTS idx_proxy_routes_server ON proxy_routes (server_id);
