-- 0016 notification_channels:多渠道通知配置(FR-19 · Story 5.1)。
-- 平台可配置多条通知渠道(webhook / email / wecom / dingtalk / feishu);后续构建/部署
-- 事件经这些渠道通知(5-2 路由 / 5-3 模板 / 5-4 覆盖消费)。本期实现 webhook + email。
--
-- AC-SEC:敏感字段(如 email 的 SMTP 密码)经 vault secretbox(master key)加密后仅以
-- **密文 BLOB**(secret_ciphertext)入库;DB dump 绝无明文。config_json 仅存非敏感的
-- per-type 配置(webhook 的 url / email 的 smtpHost/smtpPort/from/to/username),
-- 绝不含密码明文。对外(REST)对敏感字段只暴露 hasPassword:bool。
--   id                : uuid v4
--   name              : 渠道展示名(非空)
--   type              : 枚举 webhook | email | wecom | dingtalk | feishu(应用层校验)
--   enabled           : 启用开关(0/1);未启用渠道不发送
--   config_json       : 非敏感 per-type 配置 JSON(webhook={url};email={smtpHost,...})
--   secret_ciphertext : vault secretbox 密文 BLOB(空=无敏感字段;如 email 未设密码)
--   created_at        : 创建时间(RFC3339)
--   updated_at        : 更新时间(RFC3339)

CREATE TABLE IF NOT EXISTS notification_channels (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    type              TEXT NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    config_json       TEXT NOT NULL DEFAULT '{}',
    secret_ciphertext BLOB,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
