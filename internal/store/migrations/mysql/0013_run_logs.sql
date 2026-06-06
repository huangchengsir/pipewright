-- 0013 run logs(MySQL):逐行日志持久化。text 为保留字需反引号。
CREATE TABLE IF NOT EXISTS run_logs (
    id           VARCHAR(64) PRIMARY KEY,
    run_id       VARCHAR(64) NOT NULL,
    seq          BIGINT NOT NULL,
    ts           VARCHAR(32) NOT NULL,
    stream       VARCHAR(255) NOT NULL,
    step_ordinal BIGINT NOT NULL,
    `text`       LONGTEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_run_logs_run_seq ON run_logs (run_id, seq);
