-- Database-local identity and transfer receipts are excluded from business copies.
CREATE TABLE database_identity (
 id INTEGER PRIMARY KEY CHECK (id=1),
 instance_id VARCHAR(64) NOT NULL UNIQUE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE TABLE database_migration_batches (
 migration_id VARCHAR(64) NOT NULL,
 table_name VARCHAR(128) NOT NULL,
 batch_no BIGINT NOT NULL,
 row_count BIGINT NOT NULL,
 content_sha256 VARCHAR(64) NOT NULL,
 committed_at BIGINT NOT NULL,
 PRIMARY KEY(migration_id,table_name,batch_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
