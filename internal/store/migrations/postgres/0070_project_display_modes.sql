CREATE TABLE project_display_modes (
 account_key VARCHAR(128) PRIMARY KEY NOT NULL,
 display_mode VARCHAR(16) NOT NULL CHECK (display_mode IN ('single','multi'))
);
