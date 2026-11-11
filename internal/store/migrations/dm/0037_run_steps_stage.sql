-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "RUN_STEPS" ADD "STAGE" VARCHAR(255 CHAR) DEFAULT '' NOT NULL;
