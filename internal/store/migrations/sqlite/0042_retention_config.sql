-- 0042 retention_config:运行数据保留策略单例配置(id=1)。
-- runs/run_logs/run_steps/artifacts 此前无限增长、零清理 → 长期运行撑爆磁盘。
-- 本表存全局保留策略:enabled 总开关(默认关,避免升级即删数据);keep_per_project 每项目
-- 保留最近 N 条终态运行(0=不限);max_age_days 删除超过 N 天的终态运行(0=不限)。
-- 后台保留清理器(retention.Sweeper)按此策略定期裁剪;只删终态(success/failed),绝不动
-- 进行中(queued/running/waiting_approval)。删 run 经外键级联连带删其 logs/steps/artifacts 等。
CREATE TABLE IF NOT EXISTS retention_config (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    enabled          INTEGER NOT NULL DEFAULT 0,
    keep_per_project INTEGER NOT NULL DEFAULT 0,
    max_age_days     INTEGER NOT NULL DEFAULT 0,
    updated_at       TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO retention_config (id, enabled, keep_per_project, max_age_days, updated_at) VALUES (1, 0, 0, 0, '');
