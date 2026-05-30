-- 0022 oauth_apps:多 provider OAuth 应用配置(运营者一次性配,接 git 平台 OAuth)。
-- 每行一个 provider(gitee|github|gitlab|custom),记录 OAuth 应用的 client_id +
-- client_secret(加密)+ base_url(自建用)+ enabled 开关。
--
-- AC-SEC:client_secret 经 vault secretbox(master key)加密后仅以**密文 BLOB**
-- (client_secret_ciphertext)入库;DB dump 绝无明文 secret。对外(REST)只暴露掩码 +
-- configured 标志;明文绝不常驻/回显/日志。OAuth 拿到的 access_token 经 vault.Create
-- 存成 git_token 凭据(credentials 表),不落本表。
--   provider                : OAuth provider 主键(gitee|github|gitlab|custom)
--   client_id               : OAuth 应用 client id(非敏感,明文)
--   client_secret_ciphertext: vault secretbox 密文 BLOB(空=未设 secret)
--   base_url                : 自建实例基址(custom 必填;gitee/github/gitlab 留空走预设端点)
--   enabled                 : 启用开关(未启用 → authorize 拒绝发起)
--   created_at              : 创建时间(RFC3339)
--   updated_at              : 更新时间(RFC3339)

CREATE TABLE IF NOT EXISTS oauth_apps (
    provider                 TEXT PRIMARY KEY,
    client_id                TEXT NOT NULL DEFAULT '',
    client_secret_ciphertext BLOB,
    base_url                 TEXT NOT NULL DEFAULT '',
    enabled                  INTEGER NOT NULL DEFAULT 0,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);
