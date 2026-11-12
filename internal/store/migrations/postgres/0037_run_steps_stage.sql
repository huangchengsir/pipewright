-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE run_steps ADD COLUMN stage TEXT COLLATE "C" NOT NULL DEFAULT '';
