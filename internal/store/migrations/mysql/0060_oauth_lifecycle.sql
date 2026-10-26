CREATE TABLE oauth_credentials (
 credential_id VARCHAR(36) NOT NULL PRIMARY KEY,
 provider VARCHAR(32) NOT NULL, issuer TEXT NOT NULL, client_id TEXT NOT NULL,
 redirect_uri TEXT NOT NULL, app_revision VARCHAR(64) NOT NULL,
 account_id VARCHAR(256) NOT NULL, login VARCHAR(256) NOT NULL,
 refresh_ciphertext BLOB NOT NULL, expires_at BIGINT NULL,
 refresh_expires_at BIGINT NULL, refresh_after BIGINT NULL, revision BIGINT NOT NULL DEFAULT 1,
 state VARCHAR(32) NOT NULL, claim_id VARCHAR(36) NOT NULL DEFAULT '',
 claim_owner VARCHAR(36) NOT NULL DEFAULT '', updated_at BIGINT NOT NULL,
 CONSTRAINT fk_oauth_credential FOREIGN KEY (credential_id) REFERENCES credentials(id) ON DELETE CASCADE
);
CREATE TABLE oauth_credential_uses (
 id VARCHAR(36) NOT NULL PRIMARY KEY, credential_id VARCHAR(36) NOT NULL,
 generation BIGINT NOT NULL, owner VARCHAR(36) NOT NULL, created_at BIGINT NOT NULL,
 INDEX oauth_credential_uses_credential (credential_id),
 CONSTRAINT fk_oauth_use_credential FOREIGN KEY (credential_id) REFERENCES credentials(id) ON DELETE CASCADE
);
CREATE TABLE oauth_authorizations (
 credential_id VARCHAR(36) NOT NULL PRIMARY KEY,
 claim_id VARCHAR(36) NOT NULL, owner VARCHAR(36) NOT NULL, revision BIGINT NOT NULL,
 cipher_digest VARCHAR(64) NOT NULL, state VARCHAR(32) NOT NULL, updated_at BIGINT NOT NULL,
 CONSTRAINT fk_oauth_authorization FOREIGN KEY (credential_id) REFERENCES credentials(id) ON DELETE CASCADE
);
