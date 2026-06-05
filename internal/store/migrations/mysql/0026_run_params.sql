-- 0026 run_params(MySQL):参数化手动运行,pipeline_runs 增 params_json 列。
ALTER TABLE pipeline_runs ADD COLUMN params_json LONGTEXT NOT NULL DEFAULT ('{}');
