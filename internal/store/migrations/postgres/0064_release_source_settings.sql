-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE release_source_settings (id BIGINT PRIMARY KEY CHECK(id=1), source TEXT COLLATE "C" NOT NULL DEFAULT 'github', revision BIGINT NOT NULL DEFAULT 1);
INSERT INTO release_source_settings(id,source,revision) VALUES(1,'github',1);
