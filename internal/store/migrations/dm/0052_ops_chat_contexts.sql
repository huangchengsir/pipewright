-- DM8 standard mode explicit migration. VARCHAR character lengths and native FKs.
-- DDL implicitly commits: incomplete versions fail closed and require inspection.
CREATE TABLE "OPS_CHAT_CONTEXTS" (
 "CONTEXT_KEY" VARCHAR(80 CHAR) NOT NULL,
 "SESSION_ID" VARCHAR(36 CHAR) NOT NULL,
 NOT CLUSTER PRIMARY KEY ("CONTEXT_KEY"),
 FOREIGN KEY ("SESSION_ID") REFERENCES "OPS_CHAT_SESSIONS" ("ID") ON DELETE CASCADE
) STORAGE(USING LONG ROW);
-- pipewright:statement
CREATE INDEX "OPS_CHAT_CONTEXTS_SESSION" ON "OPS_CHAT_CONTEXTS" ("SESSION_ID");
