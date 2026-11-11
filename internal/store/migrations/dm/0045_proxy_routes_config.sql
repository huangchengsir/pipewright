-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
ALTER TABLE "PROXY_ROUTES" ADD "CONFIG" CLOB DEFAULT '' NOT NULL;
