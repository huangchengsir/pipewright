-- 0018 notification_routes:通知事件路由(FR-20 · Story 5.2)。
-- 「事件 → 渠道」映射:某事件发生时,经其映射的所有 enabled 渠道发送通知。
-- **未配置任何路由的事件不发送**(FR-20 硬性)。
--
-- 本期全局默认(project_id 恒空 = 全局);Story 5.4 在此基础加 project_id 维度做
-- 每流水线覆盖。一个事件可映射多渠道(多条 route);channel_id 外键级联,渠道删除时
-- 关联 route 一并清理(避免悬挂路由)。
--   id          : uuid v4
--   project_id  : 可空(NULL = 全局默认;5-4 填项目维度覆盖)
--   event       : 事件枚举(应用层校验:build_succeeded | build_failed |
--                 deploy_succeeded | deploy_failed | rollback | health_check_failed)
--   channel_id  : 关联 notification_channels.id(FK ON DELETE CASCADE)
--   enabled     : 启用开关(0/1);未启用的 route 不参与路由
--   created_at  : 创建时间(RFC3339)

CREATE TABLE IF NOT EXISTS notification_routes (
    id          TEXT PRIMARY KEY,
    project_id  TEXT,
    event       TEXT NOT NULL,
    channel_id  TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL,
    FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notification_routes_event ON notification_routes (event);
