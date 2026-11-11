-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_runs ADD COLUMN params_json TEXT COLLATE "C" NOT NULL DEFAULT '{}';
