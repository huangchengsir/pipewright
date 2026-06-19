-- 0047 preview_envs + preview_configs:Per-PR 预览环境(R4 E4.1 · 差异化王牌)。
--
-- preview_configs(每项目一行):项目级「PR 预览」开关 + 走哪个 DNS 提供商 + 在哪个根域下分配子域名。
-- 开启后,某 PR 的运行成功部署时,自动经 dnsprovider.AllocateSubdomain 在 base_domain 下分配
-- pr-<n>-<proj>.base_domain 子域名 → DNS-01 证书 + 反代路由,供该 PR 的预览访问。
--   project_id      : 主键(每项目至多一条预览配置)
--   enabled         : 0/1,是否对该项目启用 PR 预览(默认 0,行为不变 = 不动现有部署)
--   dns_provider_id : 引用 dns_providers.id(分配子域名 + DNS-01);不设外键(与 proxy_routes 一致)
--   base_domain     : 在其下分配预览子域名(如 preview.example.com);应是 dns_provider 可管理的根域/子域
CREATE TABLE IF NOT EXISTS preview_configs (
    project_id      TEXT PRIMARY KEY,
    enabled         INTEGER NOT NULL DEFAULT 0,
    dns_provider_id TEXT NOT NULL DEFAULT '',
    base_domain     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

-- preview_envs(每个「项目×PR」一行):一条已分配的 PR 预览环境。
-- 同一 (project_id, pr_number) 唯一:重新部署同一 PR 更新同一行(幂等),不重复建。
--   status        : active | reclaimed(PR 关闭/合并后回收 → reclaimed)
--   route_id      : 指向 proxy_routes.id 的反代路由(回收时删该路由);不设外键(避免级联误删)
--   subdomain     : 分配出的预览 FQDN(pr-<n>-<proj>.base_domain)
--   reclaimed_at  : 回收时刻(active 时为空串)
CREATE TABLE IF NOT EXISTS preview_envs (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    pipeline_id  TEXT NOT NULL DEFAULT '',
    pr_number    INTEGER NOT NULL,
    branch       TEXT NOT NULL DEFAULT '',
    server_id    TEXT NOT NULL DEFAULT '',
    route_id     TEXT NOT NULL DEFAULT '',
    subdomain    TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TEXT NOT NULL,
    reclaimed_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_preview_envs_project_pr ON preview_envs (project_id, pr_number);
CREATE INDEX IF NOT EXISTS idx_preview_envs_project ON preview_envs (project_id);
CREATE INDEX IF NOT EXISTS idx_preview_envs_status ON preview_envs (status);
