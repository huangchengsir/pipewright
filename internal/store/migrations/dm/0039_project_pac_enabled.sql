-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PROJECTS" ADD "PAC_ENABLED" BIGINT DEFAULT 0 NOT NULL;
