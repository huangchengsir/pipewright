CREATE TABLE project_display_modes (
 account_key TEXT PRIMARY KEY NOT NULL,
 display_mode TEXT NOT NULL CHECK (display_mode IN ('single','multi'))
);
