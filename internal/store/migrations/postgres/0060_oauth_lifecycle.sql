-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE oauth_credentials (
 credential_id TEXT COLLATE "C" PRIMARY KEY REFERENCES credentials(id) ON DELETE CASCADE,
 provider TEXT COLLATE "C" NOT NULL, issuer TEXT COLLATE "C" NOT NULL, client_id TEXT COLLATE "C" NOT NULL,
 redirect_uri TEXT COLLATE "C" NOT NULL, app_revision TEXT COLLATE "C" NOT NULL,
 account_id TEXT COLLATE "C" NOT NULL, login TEXT COLLATE "C" NOT NULL,
 refresh_ciphertext BYTEA NOT NULL, expires_at BIGINT NULL,
 refresh_expires_at BIGINT NULL, refresh_after BIGINT NULL, revision BIGINT NOT NULL DEFAULT 1,
 state TEXT COLLATE "C" NOT NULL, claim_id TEXT COLLATE "C" NOT NULL DEFAULT '',
 claim_owner TEXT COLLATE "C" NOT NULL DEFAULT '', updated_at BIGINT NOT NULL
);
CREATE TABLE oauth_credential_uses (
 id TEXT COLLATE "C" PRIMARY KEY, credential_id TEXT COLLATE "C" NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
 generation BIGINT NOT NULL, owner TEXT COLLATE "C" NOT NULL, created_at BIGINT NOT NULL
);
CREATE INDEX oauth_credential_uses_credential ON oauth_credential_uses(credential_id);
CREATE TABLE oauth_authorizations (
 credential_id TEXT COLLATE "C" PRIMARY KEY REFERENCES credentials(id) ON DELETE CASCADE,
 claim_id TEXT COLLATE "C" NOT NULL, owner TEXT COLLATE "C" NOT NULL, revision BIGINT NOT NULL,
 cipher_digest TEXT COLLATE "C" NOT NULL, state TEXT COLLATE "C" NOT NULL, updated_at BIGINT NOT NULL
);
