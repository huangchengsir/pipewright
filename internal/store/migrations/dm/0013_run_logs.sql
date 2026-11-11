-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
CREATE TABLE "RUN_LOGS" (
 "ID" VARCHAR(64 CHAR) NOT NULL,
 "RUN_ID" VARCHAR(64 CHAR) NOT NULL,
 "SEQ" BIGINT NOT NULL,
 "TS" VARCHAR(32 CHAR) NOT NULL,
 "STREAM" VARCHAR(255 CHAR) NOT NULL,
 "STEP_ORDINAL" BIGINT NOT NULL,
 "TEXT" CLOB NOT NULL,
 NOT CLUSTER PRIMARY KEY ("ID")
) STORAGE(USING LONG ROW);
-- pipewright:statement
CREATE INDEX "IDX_RUN_LOGS_RUN_SEQ" ON "RUN_LOGS" ("RUN_ID", "SEQ");
