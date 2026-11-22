-- Server-owned product release inputs are encrypted before persistence.
CREATE TABLE IF NOT EXISTS product_release_previews (
 id TEXT COLLATE BINARY NOT NULL,
 tenant_id TEXT COLLATE BINARY NOT NULL,
 actor_id TEXT COLLATE BINARY NOT NULL,
 product_id TEXT COLLATE BINARY NOT NULL,
 plan_id TEXT COLLATE BINARY NOT NULL,
 created_at BIGINT NOT NULL,
 ciphertext BLOB NOT NULL,
 PRIMARY KEY (id)
);
