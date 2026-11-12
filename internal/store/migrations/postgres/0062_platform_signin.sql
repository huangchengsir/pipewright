-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
ALTER TABLE admin_user ADD COLUMN security_generation BIGINT NOT NULL DEFAULT 1;
ALTER TABLE sessions ADD COLUMN auth_source TEXT COLLATE "C" NOT NULL DEFAULT 'password';
ALTER TABLE sessions ADD COLUMN provider_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN identity_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN absolute_expires_at TEXT COLLATE "C";
ALTER TABLE sessions ADD COLUMN password_verified_at TEXT COLLATE "C";
CREATE INDEX sessions_auth_source ON sessions(auth_source);
CREATE TABLE login_settings (id BIGINT PRIMARY KEY CHECK(id=1), visibility_revision BIGINT NOT NULL DEFAULT 1);
INSERT INTO login_settings(id) VALUES(1);
CREATE TABLE login_provider_settings (
 provider TEXT COLLATE "C" PRIMARY KEY , visible BIGINT NOT NULL DEFAULT 1,
 enabled BIGINT NOT NULL DEFAULT 0, client_id TEXT COLLATE "C" NOT NULL DEFAULT '', secret_cipher BYTEA,
 microsoft_tenant TEXT COLLATE "C" NOT NULL DEFAULT 'common', gitee_mode TEXT COLLATE "C" NOT NULL DEFAULT '',
 config_version BIGINT NOT NULL DEFAULT 1, auth_generation BIGINT NOT NULL DEFAULT 1
);
INSERT INTO login_provider_settings(provider) VALUES('github'),('gitlab'),('gitee'),('google'),('microsoft');
CREATE TABLE login_identities (
 provider TEXT COLLATE "C" PRIMARY KEY , issuer TEXT COLLATE "C" NOT NULL ,
 subject TEXT COLLATE "C" NOT NULL , display_label TEXT COLLATE "C" NOT NULL,
 identity_generation BIGINT NOT NULL DEFAULT 1, bound_at TEXT COLLATE "C" NOT NULL,
 FOREIGN KEY(provider) REFERENCES login_provider_settings(provider)
);
CREATE TABLE login_flows (
 flow_id TEXT COLLATE "C" PRIMARY KEY , state_hash TEXT COLLATE "C" NOT NULL UNIQUE ,
 browser_hash TEXT COLLATE "C" NOT NULL, purpose TEXT COLLATE "C" NOT NULL, provider TEXT COLLATE "C" NOT NULL,
 config_version BIGINT NOT NULL, auth_generation BIGINT NOT NULL, identity_generation BIGINT NOT NULL,
 security_generation BIGINT NOT NULL, session_hash TEXT COLLATE "C" NOT NULL DEFAULT '',
 verifier_cipher BYTEA, nonce_hash TEXT COLLATE "C" NOT NULL, return_to TEXT COLLATE "C" NOT NULL,
 status TEXT COLLATE "C" NOT NULL, expires_at TEXT COLLATE "C" NOT NULL, candidate_cipher BYTEA, candidate_expires_at TEXT COLLATE "C",
 created_at TEXT COLLATE "C" NOT NULL
);
CREATE INDEX login_flows_expiry ON login_flows(expires_at);
