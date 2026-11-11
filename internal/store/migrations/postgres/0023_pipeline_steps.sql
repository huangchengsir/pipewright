-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_settings ADD COLUMN steps_json TEXT COLLATE "C" NOT NULL DEFAULT '[]';
