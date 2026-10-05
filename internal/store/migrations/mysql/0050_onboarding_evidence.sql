ALTER TABLE pipeline_configs ADD COLUMN saved_at VARCHAR(40) NULL;
ALTER TABLE pipeline_runs ADD COLUMN execution_mode VARCHAR(32) NOT NULL DEFAULT 'pending';
UPDATE pipeline_runs SET execution_mode = 'legacy_unknown';
