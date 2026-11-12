-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
CREATE TABLE "RELEASE_SOURCE_SETTINGS" (
 "ID" BIGINT NOT NULL,
 "SOURCE" VARCHAR(16 CHAR) DEFAULT 'github' NOT NULL,
 "REVISION" BIGINT DEFAULT 1 NOT NULL,
 NOT CLUSTER PRIMARY KEY ("ID"),
 CHECK (id=1)
) STORAGE(USING LONG ROW);
-- pipewright:statement
INSERT INTO release_source_settings(id,source,revision) VALUES(1,'github',1);
