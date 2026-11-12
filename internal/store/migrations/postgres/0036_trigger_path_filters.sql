-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_triggers ADD COLUMN path_filters_json TEXT COLLATE "C" NOT NULL DEFAULT '[]';
