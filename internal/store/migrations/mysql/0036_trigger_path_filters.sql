-- 0036 trigger_path_filters(MySQL):pipeline_triggers 增路径过滤 glob 列。
ALTER TABLE pipeline_triggers ADD COLUMN path_filters_json LONGTEXT NOT NULL DEFAULT ('[]');
