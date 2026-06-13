-- 0038 notify_config(MySQL):通知语言单例配置(id=1);外发通知默认文案语言。
CREATE TABLE IF NOT EXISTS notify_config (
    id          INT PRIMARY KEY CHECK (id = 1),
    language    VARCHAR(16) NOT NULL DEFAULT 'zh-CN',
    updated_at  VARCHAR(32) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT IGNORE INTO notify_config (id, language, updated_at) VALUES (1, 'zh-CN', '');
