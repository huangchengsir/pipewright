-- Server-owned product release inputs are encrypted before persistence.
CREATE TABLE IF NOT EXISTS product_release_previews (
 id VARCHAR(128 CHAR) NOT NULL,
 tenant_id VARCHAR(128 CHAR) NOT NULL,
 actor_id VARCHAR(128 CHAR) NOT NULL,
 product_id VARCHAR(128 CHAR) NOT NULL,
 plan_id VARCHAR(128 CHAR) NOT NULL,
 created_at BIGINT NOT NULL,
 ciphertext BLOB NOT NULL,
 NOT CLUSTER PRIMARY KEY (id)
) STORAGE(USING LONG ROW);
