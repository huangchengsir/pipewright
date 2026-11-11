-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PIPELINE_RUNS" ADD "RESOLVED_COMMIT_SHA" VARCHAR(64 CHAR) DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "PIPELINE_RUNS" ADD "RESOLVED_COMMIT_CONFLICT" BIGINT DEFAULT 0 NOT NULL;
