-- 0027 test_reports:测试报告 + 质量门禁(Epic 8 · Story 8-6 / FR-8-6)。
-- script 步骤声明产出测试报告(JUnit XML,可选 Cobertura 覆盖率)后,平台解析其
-- 通过/失败/跳过计数 + 覆盖率%,持久化一条汇总,并据声明的门禁阈值裁决是否阻断下游部署。
--
-- 每个 run 一条汇总(同 run 多阶段产报告时按 (run_id, stage_id) 区分,聚合展示)。
-- 计数为整数;coverage_pct 为 REAL(-1 = 未提供覆盖率)。gate_* 记录门禁裁决(无门禁则
-- gate_enabled=0)。reason 为人读阻断原因(不含 secret;仅计数/阈值数字)。
CREATE TABLE IF NOT EXISTS run_test_reports (
    id             TEXT PRIMARY KEY,
    run_id         TEXT NOT NULL,
    stage_id       TEXT NOT NULL DEFAULT '',
    stage_name     TEXT NOT NULL DEFAULT '',
    format         TEXT NOT NULL DEFAULT 'junit',
    total          INTEGER NOT NULL DEFAULT 0,
    passed         INTEGER NOT NULL DEFAULT 0,
    failed         INTEGER NOT NULL DEFAULT 0,
    skipped        INTEGER NOT NULL DEFAULT 0,
    duration_sec   REAL NOT NULL DEFAULT 0,
    coverage_pct   REAL NOT NULL DEFAULT -1,
    gate_enabled   INTEGER NOT NULL DEFAULT 0,
    gate_passed    INTEGER NOT NULL DEFAULT 1,
    gate_reason    TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    FOREIGN KEY (run_id) REFERENCES pipeline_runs (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_test_reports_run ON run_test_reports (run_id);
