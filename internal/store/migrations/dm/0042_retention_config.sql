-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
CREATE TABLE "RETENTION_CONFIG" (
 "ID" BIGINT NOT NULL,
 "ENABLED" BIGINT DEFAULT 0 NOT NULL,
 "KEEP_PER_PROJECT" BIGINT DEFAULT 0 NOT NULL,
 "MAX_AGE_DAYS" BIGINT DEFAULT 0 NOT NULL,
 "UPDATED_AT" VARCHAR(32 CHAR) DEFAULT '' NOT NULL,
 NOT CLUSTER PRIMARY KEY ("ID"),
 CHECK (id = 1)
) STORAGE(USING LONG ROW);
-- pipewright:statement
INSERT INTO retention_config (id, enabled, keep_per_project, max_age_days, updated_at) VALUES (1, 0, 0, 0, '');
