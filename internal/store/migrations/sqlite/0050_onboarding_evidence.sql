ALTER TABLE pipeline_configs ADD COLUMN saved_at TEXT NULL;
ALTER TABLE pipeline_runs ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'pending';
UPDATE pipeline_runs SET execution_mode = 'legacy_unknown';
