-- 0007 webhook_deliveries:webhook 投递去重 + 运行解析出的目标元数据(FR-1 / Story 3.2)。
--
-- webhook_deliveries:记录每个项目已处理过的 Gitee 投递,做幂等去重(AC4)。
--   同一 (project_id, delivery_id) 重复投递靠 UNIQUE 约束并发安全地拒绝第二次创建运行。
--   delivery_id 取自 X-Gitee-Delivery;缺失时回退为 (event + commit) 派生键,仍入此表去重。
--   run_id      : 命中并创建运行时记录的运行 id(被忽略的投递为空字符串)。
--   outcome     : accepted | <ignored 原因>(event_not_subscribed|no_branch_match|duplicate|unmatched_ignored|unmatched_recorded)
--   created_at  : 处理时刻 RFC3339
--
-- pipeline_runs 新增内部列(供 Epic 4 的 targets 消费;不进入冻结 run-detail DTO 输出):
--   resolved_environment        : webhook 分支映射解析出的环境名(手动触发为空)
--   resolved_target_server_ids  : 解析出的目标服务器引用 id 列表 JSON(手动触发为 '[]')

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    event       TEXT NOT NULL DEFAULT '',
    branch      TEXT NOT NULL DEFAULT '',
    commit_sha  TEXT NOT NULL DEFAULT '',
    run_id      TEXT NOT NULL DEFAULT '',
    outcome     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    UNIQUE (project_id, delivery_id)
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_project_id ON webhook_deliveries (project_id);

ALTER TABLE pipeline_runs ADD COLUMN resolved_environment TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN resolved_target_server_ids TEXT NOT NULL DEFAULT '[]';
