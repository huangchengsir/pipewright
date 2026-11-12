-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PIPELINE_TRIGGERS" ADD "PATH_FILTERS_JSON" CLOB DEFAULT '[]' NOT NULL;
