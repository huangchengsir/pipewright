-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE projects ADD COLUMN pac_enabled BIGINT NOT NULL DEFAULT 0;
