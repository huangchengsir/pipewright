-- 0047 preview_envs + preview_configs(MySQL):Per-PR 预览环境(R4 E4.1 · 差异化王牌)。
-- preview_configs(每项目一行):项目级 PR 预览开关 + DNS 提供商 + 根域。开启后某 PR 运行成功部署时,
-- 自动经 dnsprovider.AllocateSubdomain 在 base_domain 下分配 pr-<n>-<proj> 子域名 + DNS-01 + 反代路由。
-- dns_provider_id 不设外键(与 proxy_routes 一致);默认 enabled=0(行为不变)。
CREATE TABLE IF NOT EXISTS preview_configs (
    project_id      VARCHAR(64) PRIMARY KEY,
    enabled         TINYINT NOT NULL DEFAULT 0,
    dns_provider_id VARCHAR(64) NOT NULL DEFAULT '',
    base_domain     VARCHAR(253) NOT NULL DEFAULT '',
    created_at      VARCHAR(32) NOT NULL,
    updated_at      VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- preview_envs(每个「项目×PR」一行):一条已分配的 PR 预览环境。
-- (project_id, pr_number) 唯一:重新部署同一 PR 更新同一行(幂等);status ∈ active|reclaimed;
-- route_id 指向 proxy_routes.id(回收时删该路由),不设外键(避免级联误删)。
CREATE TABLE IF NOT EXISTS preview_envs (
    id           VARCHAR(64) PRIMARY KEY,
    project_id   VARCHAR(64) NOT NULL,
    pipeline_id  VARCHAR(64) NOT NULL DEFAULT '',
    pr_number    INT NOT NULL,
    branch       VARCHAR(255) NOT NULL DEFAULT '',
    server_id    VARCHAR(64) NOT NULL DEFAULT '',
    route_id     VARCHAR(64) NOT NULL DEFAULT '',
    subdomain    VARCHAR(253) NOT NULL DEFAULT '',
    status       VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at   VARCHAR(32) NOT NULL,
    reclaimed_at VARCHAR(32) NOT NULL DEFAULT '',
    UNIQUE INDEX idx_preview_envs_project_pr (project_id, pr_number),
    INDEX idx_preview_envs_project (project_id),
    INDEX idx_preview_envs_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
