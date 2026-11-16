ALTER TABLE [pipeline_configs] ADD [history_version_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL;
GO
ALTER TABLE [pipeline_runs] ADD [history_version_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NULL;
GO
CREATE TABLE [pipeline_config_versions] (
 [id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [project_id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
 [version_seq] BIGINT NOT NULL,
 [created_at] NVARCHAR(32) NOT NULL,
 [actor] NVARCHAR(255) NOT NULL,
 [source] NVARCHAR(32) NOT NULL,
 [change_count] BIGINT NOT NULL,
 [changes_json] NVARCHAR(MAX) NOT NULL,
 [ciphertext] VARBINARY(MAX) NULL,
 PRIMARY KEY NONCLUSTERED ([id]),
 FOREIGN KEY ([project_id]) REFERENCES [pipeline_configs] ([project_id]) ON DELETE CASCADE
);
GO
CREATE UNIQUE NONCLUSTERED INDEX [pipeline_config_versions_sequence] ON [pipeline_config_versions] ([project_id], [version_seq]);
