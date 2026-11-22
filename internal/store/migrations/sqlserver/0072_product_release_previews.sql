-- Server-owned product release inputs are encrypted before persistence.
CREATE TABLE product_release_previews (
 id NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 tenant_id NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 actor_id NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 product_id NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 plan_id NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL,
 created_at BIGINT NOT NULL,
 ciphertext VARBINARY(MAX) NOT NULL,
 PRIMARY KEY NONCLUSTERED (id)
);
