-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_configs ADD COLUMN saved_at TEXT COLLATE "C" NULL;
ALTER TABLE pipeline_runs ADD COLUMN execution_mode TEXT COLLATE "C" NOT NULL DEFAULT 'pending';
UPDATE pipeline_runs SET execution_mode = 'legacy_unknown';
