-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PIPELINE_RUNS" ADD "FAILURE_LOG" CLOB DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "PIPELINE_RUNS" ADD "DIAGNOSIS_JSON" CLOB DEFAULT '' NOT NULL;
