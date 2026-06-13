-- 0042 retention_config(MySQL):运行数据保留策略单例配置(id=1)。
-- enabled 总开关(默认关);keep_per_project 每项目保留最近 N 条终态运行(0=不限);
-- max_age_days 删除超过 N 天的终态运行(0=不限)。后台清理器按此裁剪,仅删终态运行。
CREATE TABLE IF NOT EXISTS retention_config (
    id               INT PRIMARY KEY CHECK (id = 1),
    enabled          TINYINT NOT NULL DEFAULT 0,
    keep_per_project INT NOT NULL DEFAULT 0,
    max_age_days     INT NOT NULL DEFAULT 0,
    updated_at       VARCHAR(32) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT IGNORE INTO retention_config (id, enabled, keep_per_project, max_age_days, updated_at) VALUES (1, 0, 0, 0, '');
