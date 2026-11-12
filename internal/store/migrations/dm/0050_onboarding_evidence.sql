-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PIPELINE_CONFIGS" ADD "SAVED_AT" VARCHAR(40 CHAR) NULL;
-- pipewright:statement
ALTER TABLE "PIPELINE_RUNS" ADD "EXECUTION_MODE" VARCHAR(32 CHAR) DEFAULT 'pending' NOT NULL;
-- pipewright:statement
UPDATE pipeline_runs SET execution_mode = 'legacy_unknown';
