-- Persistent ordered release intentions; no automatic replay of unknown work.
CREATE TABLE [product_release_batches] (
 [id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [tenant_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [actor_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [product_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [request_id] NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [fingerprint] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [revision] BIGINT NOT NULL,
 [plan_json] NVARCHAR(MAX) NOT NULL,
 [items_json] NVARCHAR(MAX) NOT NULL,
 [created_at] BIGINT NOT NULL,
 [updated_at] BIGINT NOT NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 UNIQUE ([tenant_id],[actor_id],[request_id])
);
