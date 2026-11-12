-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_runs ADD COLUMN spec_source TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN spec_source_ref TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN spec_source_file TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN spec_source_fallback TEXT COLLATE "C" NOT NULL DEFAULT '';
