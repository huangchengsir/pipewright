-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE pipeline_runs ADD COLUMN resolved_commit_sha TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN resolved_commit_conflict BIGINT NOT NULL DEFAULT 0;
