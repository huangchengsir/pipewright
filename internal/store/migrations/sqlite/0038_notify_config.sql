-- 0038 notify_config:通知语言单例配置(id=1)。
-- 外发通知(飞书/邮件等)的默认文案语言,与界面语言解耦——后台通知 worker 无请求
-- 上下文,发送时从本表读取语言。language 为受支持的 locale 码(zh-CN 默认)。
CREATE TABLE IF NOT EXISTS notify_config (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    language    TEXT NOT NULL DEFAULT 'zh-CN',
    updated_at  TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO notify_config (id, language, updated_at) VALUES (1, 'zh-CN', '');
