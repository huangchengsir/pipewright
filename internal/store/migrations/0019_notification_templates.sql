-- 0019 notification_templates:通知内容自定义(模板 + 变量)(FR-21 · Story 5.3)。
-- 自定义通知的标题/正文模板,占位用 {{name}} 纯文本替换(变量集冻结:project/branch/
-- commit/status/event/durationMs/runId/errorSummary)。未配置模板 → 平台默认文案(5-2 行为不变)。
--
-- 渲染纯文本替换、绝不执行用户模板(无 RCE)。errorSummary 经尽力脱敏后渲染(绝无明文 secret)。
--
-- 匹配优先级(RenderPayload):channel_id 精确 > 该事件通用(channel_id 为空)> 平台默认。
-- 本期 project_id 恒空(全局默认);Story 5.4 在此基础加 project_id 维度做每流水线覆盖,不改形状。
--   id             : uuid v4
--   project_id     : 可空(NULL = 全局默认;5-4 填项目维度覆盖)
--   event          : 事件枚举(应用层校验:build_succeeded | build_failed |
--                    deploy_succeeded | deploy_failed | rollback | health_check_failed)
--   channel_id     : 可空(NULL = 该事件所有渠道通用;非空 = 仅该渠道;FK ON DELETE CASCADE)
--   title_template : 标题模板(含 {{x}} 占位)
--   body_template  : 正文模板(含 {{x}} 占位)
--   created_at     : 创建时间(RFC3339)

CREATE TABLE IF NOT EXISTS notification_templates (
    id             TEXT PRIMARY KEY,
    project_id     TEXT,
    event          TEXT NOT NULL,
    channel_id     TEXT,
    title_template TEXT NOT NULL DEFAULT '',
    body_template  TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notification_templates_event ON notification_templates (event);
