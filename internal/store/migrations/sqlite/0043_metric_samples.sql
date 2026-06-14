-- 0043 metric_samples:服务器指标时序采样(异常检测「看趋势」折线图的数据源)。
-- 后台采样器(main 定时,与异常检测同 collector 复用 6-1 SSH 采集)周期性把每台可达服务器的
-- CPU / 内存 / 磁盘使用率(百分比)写一行;异常检测页据此画指标随时间走势(哪怕未触发告警)。
--
-- 各 *_percent 可空(NULL = 该指标该时刻不可得:跨平台 best-effort / 解析失败 / 不可达跳过)。
-- 数据按保留窗口(env PIPEWRIGHT_METRICS_RETENTION_DAYS,默认 7 天)定期清理,避免无限增长。
-- server_id 不设外键:采样为历史快照,server 删除后旧趋势仍可查(由保留清理而非级联删除收敛)。
CREATE TABLE IF NOT EXISTS metric_samples (
    id             TEXT PRIMARY KEY,
    server_id      TEXT NOT NULL,
    cpu_percent    REAL,
    memory_percent REAL,
    disk_percent   REAL,
    sampled_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metric_samples_server_time ON metric_samples (server_id, sampled_at);
CREATE INDEX IF NOT EXISTS idx_metric_samples_time ON metric_samples (sampled_at);
