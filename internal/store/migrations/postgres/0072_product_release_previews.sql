-- Server-owned product release inputs are encrypted before persistence.
CREATE TABLE IF NOT EXISTS product_release_previews (
 id TEXT COLLATE "C" NOT NULL,
 tenant_id TEXT COLLATE "C" NOT NULL,
 actor_id TEXT COLLATE "C" NOT NULL,
 product_id TEXT COLLATE "C" NOT NULL,
 plan_id TEXT COLLATE "C" NOT NULL,
 created_at BIGINT NOT NULL,
 ciphertext BYTEA NOT NULL,
 PRIMARY KEY (id)
);
