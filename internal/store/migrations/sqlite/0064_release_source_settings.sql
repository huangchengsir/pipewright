CREATE TABLE release_source_settings (id INTEGER PRIMARY KEY CHECK(id=1), source TEXT NOT NULL DEFAULT 'github', revision INTEGER NOT NULL DEFAULT 1);
INSERT INTO release_source_settings(id,source,revision) VALUES(1,'github',1);
