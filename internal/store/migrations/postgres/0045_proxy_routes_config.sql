-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE proxy_routes ADD COLUMN config TEXT COLLATE "C" NOT NULL DEFAULT '';
