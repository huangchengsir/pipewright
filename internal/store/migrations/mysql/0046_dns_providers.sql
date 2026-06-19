-- 0046 dns_providers(MySQL):DNS 提供商集成层(R3 E3.1)。
-- 每行 = 一个 DNS 提供商接入(Cloudflare / DNSPod / 阿里云 DNS),供 DNS-01 通配符签发
-- (E3.2)、瞬时子域名分配(E3.3)、自动建 A 记录(E3.4)使用。
-- 凭据绝不以明文入此表:只存 credential_id,指向 credentials 表里经 vault secretbox 加密的凭据。
-- 取明文仅在 apply 时经 vault.Reveal 在进程内进行,注入 0600 临时 Caddyfile,绝不日志/回库/回 API。
-- type ∈ cloudflare|dnspod|alidns;credential_id 不设外键(与 proxy_routes/servers 一致)。
CREATE TABLE IF NOT EXISTS dns_providers (
    id            VARCHAR(64) PRIMARY KEY,
    type          VARCHAR(32) NOT NULL,
    name          VARCHAR(255) NOT NULL,
    credential_id VARCHAR(64) NOT NULL,
    base_domain   VARCHAR(253) NOT NULL,
    created_at    VARCHAR(32) NOT NULL,
    updated_at    VARCHAR(32) NOT NULL,
    INDEX idx_dns_providers_type (type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
