-- 0037 run_steps.stage(MySQL):run_steps 增所属阶段名列。
ALTER TABLE run_steps ADD COLUMN stage VARCHAR(255) NOT NULL DEFAULT '';
