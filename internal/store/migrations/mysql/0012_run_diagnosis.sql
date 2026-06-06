-- 0012 run diagnosis(MySQL):pipeline_runs 增失败日志 + AI 诊断列。
ALTER TABLE pipeline_runs ADD COLUMN failure_log LONGTEXT NOT NULL DEFAULT ('');
ALTER TABLE pipeline_runs ADD COLUMN diagnosis_json LONGTEXT NOT NULL DEFAULT ('');
