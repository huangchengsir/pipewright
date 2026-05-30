-- 0003 credentials:凭据加密保险库(FR-3 / AC-SEC-01)。
-- 仅存密文 BLOB(ciphertext = nonce||secretbox)+ 服务端算出的掩码 + 元数据。
-- 明文 / PEM / master key 绝不入任何列。
--   id           : uuid v4
--   type         : git_token | ssh_key | registry(应用层校验枚举)
--   ciphertext   : NaCl secretbox 密文,前置 24B 随机 nonce
--   masked_value : 服务端掩码字串(如 ghp_••••a91f),可对外展示
--   last_used_at : Vault.Get 取明文时更新;NULL = 从未使用

CREATE TABLE IF NOT EXISTS credentials (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    type         TEXT NOT NULL,
    scope        TEXT NOT NULL DEFAULT '',
    ciphertext   BLOB NOT NULL,
    masked_value TEXT NOT NULL,
    last_used_at TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
