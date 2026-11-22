-- Persistent ordered release intentions; no automatic replay of unknown work.
CREATE TABLE product_release_batches (
 id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 tenant_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 actor_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 product_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 request_id VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 fingerprint VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 revision BIGINT NOT NULL,
 plan_json LONGTEXT NOT NULL,
 items_json LONGTEXT NOT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 PRIMARY KEY (id),
 UNIQUE (tenant_id,actor_id,request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
