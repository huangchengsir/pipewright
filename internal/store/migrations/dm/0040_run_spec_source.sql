-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PIPELINE_RUNS" ADD "SPEC_SOURCE" VARCHAR(32 CHAR) DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "PIPELINE_RUNS" ADD "SPEC_SOURCE_REF" VARCHAR(255 CHAR) DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "PIPELINE_RUNS" ADD "SPEC_SOURCE_FILE" VARCHAR(255 CHAR) DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "PIPELINE_RUNS" ADD "SPEC_SOURCE_FALLBACK" VARCHAR(64 CHAR) DEFAULT '' NOT NULL;
