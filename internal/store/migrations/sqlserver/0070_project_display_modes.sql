CREATE TABLE [project_display_modes] (
 [account_key] NVARCHAR(128) PRIMARY KEY NOT NULL,
 [display_mode] NVARCHAR(16) NOT NULL CHECK ([display_mode] IN ('single','multi'))
);
