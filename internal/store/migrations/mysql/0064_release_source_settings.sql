CREATE TABLE IF NOT EXISTS release_source_settings (id INT PRIMARY KEY, source VARCHAR(16) NOT NULL DEFAULT 'github', revision BIGINT NOT NULL DEFAULT 1);
INSERT IGNORE INTO release_source_settings(id,source,revision) VALUES(1,'github',1);
