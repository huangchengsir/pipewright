-- 0021 anomaly:可配置运行时异常检测与告警(FR-23 · Story 6.5)。
-- 管理员配置阈值规则(如 CPU>90%、磁盘>85%、内存>90%);系统按规则对服务器指标
-- (经 6-1 SSH 轮询采集)求值,命中即产告警。v1 数据源 = SSH 轮询。
--
-- 形状冻结(供后续接通知/实时流增强消费,不改形状):
--   anomaly_rules:
--     id         : uuid v4
--     metric     : 枚举 cpu | memory | disk(应用层校验);cpu=使用率%(loadavg1/cores×100),
--                  memory=used/total%,disk=used/total%
--     operator   : 枚举 gt | lt(应用层校验)
--     threshold  : 阈值(REAL,百分比数值)
--     server_id  : 作用域;NULL=全局(对所有服务器),非空=仅指定服务器
--     enabled    : 启用开关(0/1);未启用规则不参与检测
--     created_at : 创建时间(RFC3339)
--   anomaly_alerts(命中规则时产生的告警记录,持久化 + 列表可见):
--     id          : uuid v4
--     server_id   : 命中服务器 id
--     server_name : 冗余服务器展示名(产告警时快照,便于列表展示)
--     metric      : 命中规则的 metric
--     operator    : 命中规则的 operator
--     threshold   : 命中规则的阈值
--     value       : 命中时的实际指标值(百分比)
--     message     : 人读告警文案(如「磁盘使用率 92.3% > 85%」)
--     created_at  : 告警时刻(RFC3339)
--
-- server_id 不设外键:规则可全局(NULL),告警为历史快照(server 删除后仍应可查),
-- 故不随 servers 行删除联动。指标采集失败/不可达的服务器跳过(不误报),无告警入库。

CREATE TABLE IF NOT EXISTS anomaly_rules (
    id         TEXT PRIMARY KEY,
    metric     TEXT NOT NULL,
    operator   TEXT NOT NULL,
    threshold  REAL NOT NULL,
    server_id  TEXT,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS anomaly_alerts (
    id          TEXT PRIMARY KEY,
    server_id   TEXT NOT NULL,
    server_name TEXT NOT NULL,
    metric      TEXT NOT NULL,
    operator    TEXT NOT NULL,
    threshold   REAL NOT NULL,
    value       REAL NOT NULL,
    message     TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_anomaly_alerts_created_at ON anomaly_alerts (created_at);
CREATE INDEX IF NOT EXISTS idx_anomaly_alerts_server_id ON anomaly_alerts (server_id);
