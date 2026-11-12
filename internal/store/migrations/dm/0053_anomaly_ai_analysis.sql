-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "ANOMALY_ALERTS" ADD "RULE_ID" VARCHAR(36 CHAR) DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "ANOMALY_ALERTS" ADD "AI_ANALYSIS_ENABLED" BIGINT DEFAULT 0 NOT NULL;
-- pipewright:statement
ALTER TABLE "ANOMALY_ALERTS" ADD "ANALYSIS_STATUS" VARCHAR(16 CHAR) DEFAULT '' NOT NULL;
-- pipewright:statement
ALTER TABLE "ANOMALY_ALERTS" ADD "ANALYSIS_JSON" CLOB NULL;
-- pipewright:statement
ALTER TABLE "ANOMALY_RULES" ADD "AI_ANALYSIS_ENABLED" BIGINT DEFAULT 0 NOT NULL;
