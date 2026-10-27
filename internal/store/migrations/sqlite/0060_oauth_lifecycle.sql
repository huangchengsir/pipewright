CREATE TABLE oauth_credentials (
 credential_id TEXT PRIMARY KEY REFERENCES credentials(id) ON DELETE CASCADE,
 provider TEXT NOT NULL, issuer TEXT NOT NULL, client_id TEXT NOT NULL,
 redirect_uri TEXT NOT NULL, app_revision TEXT NOT NULL,
 account_id TEXT NOT NULL, login TEXT NOT NULL,
 refresh_ciphertext BLOB NOT NULL, expires_at BIGINT NULL,
 refresh_expires_at BIGINT NULL, refresh_after BIGINT NULL, revision BIGINT NOT NULL DEFAULT 1,
 state TEXT NOT NULL, claim_id TEXT NOT NULL DEFAULT '',
 claim_owner TEXT NOT NULL DEFAULT '', updated_at BIGINT NOT NULL
);
CREATE TABLE oauth_credential_uses (
 id TEXT PRIMARY KEY, credential_id TEXT NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
 generation BIGINT NOT NULL, owner TEXT NOT NULL, created_at BIGINT NOT NULL
);
CREATE INDEX oauth_credential_uses_credential ON oauth_credential_uses(credential_id);
CREATE TABLE oauth_authorizations (
 credential_id TEXT PRIMARY KEY REFERENCES credentials(id) ON DELETE CASCADE,
 claim_id TEXT NOT NULL, owner TEXT NOT NULL, revision BIGINT NOT NULL,
 cipher_digest TEXT NOT NULL, state TEXT NOT NULL, updated_at BIGINT NOT NULL
);
