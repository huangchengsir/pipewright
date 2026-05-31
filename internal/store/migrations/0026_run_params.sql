-- 0026 run_params:参数化手动运行(Epic 8 · Story 8-11 / FR-8-11)。
-- 手动触发可附一组 key=value 参数(如 DEPLOY_ENV=staging),执行时注入 script 步骤容器作环境变量。
-- 以 JSON 存于 pipeline_runs 新列(非敏感;若含密钥应走保险库引用,本期为明文构建参数)。
ALTER TABLE pipeline_runs ADD COLUMN params_json TEXT NOT NULL DEFAULT '{}';
