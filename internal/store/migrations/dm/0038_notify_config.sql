-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
CREATE TABLE "NOTIFY_CONFIG" (
 "ID" BIGINT NOT NULL,
 "LANGUAGE" VARCHAR(16 CHAR) DEFAULT 'zh-CN' NOT NULL,
 "UPDATED_AT" VARCHAR(32 CHAR) DEFAULT '' NOT NULL,
 NOT CLUSTER PRIMARY KEY ("ID"),
 CHECK (id = 1)
) STORAGE(USING LONG ROW);
-- pipewright:statement
INSERT INTO notify_config (id, language, updated_at) VALUES (1, 'zh-CN', '');
