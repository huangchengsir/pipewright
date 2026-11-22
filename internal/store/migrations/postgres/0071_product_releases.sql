-- Persistent ordered release intentions; no automatic replay of unknown work.
CREATE TABLE product_release_batches (
 id TEXT COLLATE "C" NOT NULL,
 tenant_id TEXT COLLATE "C" NOT NULL,
 actor_id TEXT COLLATE "C" NOT NULL,
 product_id TEXT COLLATE "C" NOT NULL,
 request_id TEXT COLLATE "C" NOT NULL,
 fingerprint TEXT COLLATE "C" NOT NULL,
 revision BIGINT NOT NULL,
 plan_json TEXT NOT NULL,
 items_json TEXT NOT NULL,
 created_at BIGINT NOT NULL,
 updated_at BIGINT NOT NULL,
 PRIMARY KEY (id),
 UNIQUE (tenant_id,actor_id,request_id)
);
