-- 0046 dns_providers:DNS 提供商集成层(R3 E3.1)。
-- 每行 = 一个 DNS 提供商接入(Cloudflare / DNSPod / 阿里云 DNS),供 DNS-01 通配符签发
-- (E3.2)、瞬时子域名分配(E3.3)、自动建 A 记录(E3.4)使用。
--
-- 凭据(API token / key)绝不以明文入此表:只存 credential_id,指向 credentials 表里经 vault
-- secretbox 加密的一条 git_token 凭据。取明文仅在 apply 时经 vault.Reveal 在进程内进行,
-- 注入到 0600 临时 Caddyfile,绝不日志/回库/回 API。
--   id            : uuid v4
--   type          : cloudflare | dnspod | alidns(应用层校验枚举)
--   name          : 展示名(用户可读)
--   credential_id : 指向 credentials.id 的 vault 凭据引用(不设外键:与 proxy_routes/servers
--                   一致,删凭据由 vault 在用守卫拦,避免级联误删)
--   base_domain   : 该提供商托管的根域(如 example.com),子域名分配/通配符在其下
CREATE TABLE IF NOT EXISTS dns_providers (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    name          TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    base_domain   TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dns_providers_type ON dns_providers (type);
