-- 0004 projects:被纳管的代码仓库项目(FR-1 / FR-3 / Story 2.1)。
-- 项目按引用绑定一个仓库凭据:仅存 credential_id 外键,绝不存任何明文/令牌。
-- 克隆/ls-remote 校验所用的 token 由领域层经 vault.Get(credential_id) 进程内取,用完即弃。
--   id             : uuid v4
--   name           : 项目展示名
--   repo_url       : Gitee/HTTPS 仓库地址(不含凭据;凭据靠引用注入)
--   default_branch : 默认分支(可由 ls-remote 探测远端 HEAD 填充)
--   credential_id  : FK → credentials.id;ON DELETE RESTRICT:仍被项目引用的凭据不可删

CREATE TABLE IF NOT EXISTS projects (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    repo_url       TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT '',
    credential_id  TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    FOREIGN KEY (credential_id) REFERENCES credentials (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_projects_credential_id ON projects (credential_id);
