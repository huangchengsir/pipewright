-- Only actual checkout callbacks populate these fields; do not backfill trigger values.
ALTER TABLE pipeline_runs
 ADD COLUMN resolved_commit_sha VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 ADD COLUMN resolved_commit_conflict TINYINT(1) NOT NULL DEFAULT 0;
