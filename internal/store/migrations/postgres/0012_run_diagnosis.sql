-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_runs ADD COLUMN failure_log TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN diagnosis_json TEXT COLLATE "C" NOT NULL DEFAULT '';
