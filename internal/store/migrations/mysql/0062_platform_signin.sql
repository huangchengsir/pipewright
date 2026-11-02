ALTER TABLE admin_user ADD COLUMN security_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE sessions ADD COLUMN auth_source VARCHAR(32) NOT NULL DEFAULT 'password';
ALTER TABLE sessions ADD COLUMN provider_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN identity_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN absolute_expires_at VARCHAR(40);
ALTER TABLE sessions ADD COLUMN password_verified_at VARCHAR(40);
CREATE INDEX sessions_auth_source ON sessions(auth_source);
CREATE TABLE login_settings (id BIGINT PRIMARY KEY CHECK(id=1), visibility_revision BIGINT NOT NULL DEFAULT 1);
INSERT INTO login_settings(id) VALUES(1);
CREATE TABLE login_provider_settings (
 provider VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, visible BIGINT NOT NULL DEFAULT 1,
 enabled BIGINT NOT NULL DEFAULT 0, client_id VARCHAR(2048) NOT NULL DEFAULT '', secret_cipher BLOB,
 microsoft_tenant VARCHAR(64) NOT NULL DEFAULT 'common', gitee_mode VARCHAR(32) NOT NULL DEFAULT '',
 config_version BIGINT NOT NULL DEFAULT 1, auth_generation BIGINT NOT NULL DEFAULT 1
);
INSERT INTO login_provider_settings(provider) VALUES('github'),('gitlab'),('gitee'),('google'),('microsoft');
CREATE TABLE login_identities (
 provider VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, issuer VARBINARY(2048) NOT NULL,
 subject VARBINARY(2048) NOT NULL, display_label TEXT NOT NULL,
 identity_generation BIGINT NOT NULL DEFAULT 1, bound_at VARCHAR(40) NOT NULL,
 FOREIGN KEY(provider) REFERENCES login_provider_settings(provider)
);
CREATE TABLE login_flows (
 flow_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, state_hash VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 browser_hash TEXT NOT NULL, purpose TEXT NOT NULL, provider VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 config_version BIGINT NOT NULL, auth_generation BIGINT NOT NULL, identity_generation BIGINT NOT NULL,
 security_generation BIGINT NOT NULL, session_hash VARCHAR(64) NOT NULL DEFAULT '',
 verifier_cipher BLOB, nonce_hash TEXT NOT NULL, return_to TEXT NOT NULL,
 status TEXT NOT NULL, expires_at VARCHAR(40) NOT NULL, candidate_cipher BLOB, candidate_expires_at VARCHAR(40),
 created_at VARCHAR(40) NOT NULL
);
CREATE INDEX login_flows_expiry ON login_flows(expires_at);
