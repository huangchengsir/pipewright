-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE credentials ADD COLUMN username TEXT COLLATE "C" NOT NULL DEFAULT '';
