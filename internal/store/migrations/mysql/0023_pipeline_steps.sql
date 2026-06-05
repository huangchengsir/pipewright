-- 0023 pipeline_settings.steps_json(MySQL):自定义脚本步骤列表列。
ALTER TABLE pipeline_settings ADD COLUMN steps_json LONGTEXT NOT NULL DEFAULT ('[]');
