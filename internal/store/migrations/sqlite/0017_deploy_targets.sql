-- 0017 deploy targets:SSH 部署执行的每机结果记录(FR-10 / Story 4.2)。
-- additive only:新增独立 deploy_targets 表,不改既有 pipeline_runs / run_steps / run_artifacts 列或语义。
--
-- 一次部署把某 run 的某产物经 SSH 部署到一台或多台目标服务器;每台一行结果。
-- 填 3-1 冻结的 run-detail `targets` slot:部署过 → 数组;否则 null。
-- 4-4 回滚把 status 置 rolled_back;4-5 多机扇出只新增多行,不改此形状。
--
--   run_id      : FK → pipeline_runs.id(部署所依附的运行)。
--   server_id   : 目标服务器引用 id(逻辑引用;不加外键,允许服务器删除后历史结果仍可读)。
--   server_name : 冗余只读展示名(部署时快照,服务器改名/删除后历史仍可读)。
--   status      : pending | deploying | success | failed | rolled_back(枚举冻结)。
--   message     : 人读摘要(成功摘要 / 失败原因;**绝无明文密钥**)。
--   started_at  : 部署开始时刻(RFC3339;UTC)。
--   finished_at : 部署结束时刻(RFC3339;UTC;未结束为空)。

CREATE TABLE IF NOT EXISTS deploy_targets (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL,
    server_id   TEXT NOT NULL,
    server_name TEXT NOT NULL,
    status      TEXT NOT NULL,
    message     TEXT NOT NULL DEFAULT '',
    started_at  TEXT NOT NULL,
    finished_at TEXT,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id)
);

-- (run_id) 索引:按 run 拉取部署目标列表(run-detail targets slot 的主查询路径)。
CREATE INDEX IF NOT EXISTS idx_deploy_targets_run ON deploy_targets (run_id);
