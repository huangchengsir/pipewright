-- Only actual checkout callbacks populate these fields; do not backfill trigger values.
ALTER TABLE pipeline_runs ADD COLUMN resolved_commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE pipeline_runs ADD COLUMN resolved_commit_conflict INTEGER NOT NULL DEFAULT 0;
