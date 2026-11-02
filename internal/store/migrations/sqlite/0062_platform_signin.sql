ALTER TABLE admin_user ADD COLUMN security_generation INTEGER NOT NULL DEFAULT 1;
ALTER TABLE sessions ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'password';
ALTER TABLE sessions ADD COLUMN provider_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN identity_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN absolute_expires_at TEXT;
ALTER TABLE sessions ADD COLUMN password_verified_at TEXT;
CREATE INDEX sessions_auth_source ON sessions(auth_source);
CREATE TABLE login_settings (id INTEGER PRIMARY KEY CHECK(id=1), visibility_revision INTEGER NOT NULL DEFAULT 1);
INSERT INTO login_settings(id) VALUES(1);
CREATE TABLE login_provider_settings (
 provider TEXT PRIMARY KEY COLLATE BINARY, visible INTEGER NOT NULL DEFAULT 1,
 enabled INTEGER NOT NULL DEFAULT 0, client_id TEXT NOT NULL DEFAULT '', secret_cipher BLOB,
 microsoft_tenant TEXT NOT NULL DEFAULT 'common', gitee_mode TEXT NOT NULL DEFAULT '',
 config_version INTEGER NOT NULL DEFAULT 1, auth_generation INTEGER NOT NULL DEFAULT 1
);
INSERT INTO login_provider_settings(provider) VALUES('github'),('gitlab'),('gitee'),('google'),('microsoft');
CREATE TABLE login_identities (
 provider TEXT PRIMARY KEY COLLATE BINARY, issuer TEXT NOT NULL COLLATE BINARY,
 subject TEXT NOT NULL COLLATE BINARY, display_label TEXT NOT NULL,
 identity_generation INTEGER NOT NULL DEFAULT 1, bound_at TEXT NOT NULL,
 FOREIGN KEY(provider) REFERENCES login_provider_settings(provider)
);
CREATE TABLE login_flows (
 flow_id TEXT PRIMARY KEY COLLATE BINARY, state_hash TEXT NOT NULL UNIQUE COLLATE BINARY,
 browser_hash TEXT NOT NULL, purpose TEXT NOT NULL, provider TEXT NOT NULL,
 config_version INTEGER NOT NULL, auth_generation INTEGER NOT NULL, identity_generation INTEGER NOT NULL,
 security_generation INTEGER NOT NULL, session_hash TEXT NOT NULL DEFAULT '',
 verifier_cipher BLOB, nonce_hash TEXT NOT NULL, return_to TEXT NOT NULL,
 status TEXT NOT NULL, expires_at TEXT NOT NULL, candidate_cipher BLOB, candidate_expires_at TEXT,
 created_at TEXT NOT NULL
);
CREATE INDEX login_flows_expiry ON login_flows(expires_at);
