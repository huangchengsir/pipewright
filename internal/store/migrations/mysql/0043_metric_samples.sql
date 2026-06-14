-- 0043 metric_samples(MySQL):服务器指标时序采样(异常检测「看趋势」折线图数据源)。
-- 后台采样器周期性把每台可达服务器的 CPU / 内存 / 磁盘使用率(百分比)写一行;异常检测页据此
-- 画指标随时间走势。各 *_percent 可空(NULL = 该指标该时刻不可得)。按保留窗口定期清理。
CREATE TABLE IF NOT EXISTS metric_samples (
    id             VARCHAR(64) PRIMARY KEY,
    server_id      VARCHAR(64) NOT NULL,
    cpu_percent    DOUBLE NULL,
    memory_percent DOUBLE NULL,
    disk_percent   DOUBLE NULL,
    sampled_at     VARCHAR(32) NOT NULL,
    INDEX idx_metric_samples_server_time (server_id, sampled_at),
    INDEX idx_metric_samples_time (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
