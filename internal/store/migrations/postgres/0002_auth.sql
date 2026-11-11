-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS admin_user (
    id            BIGINT PRIMARY KEY CHECK (id = 1),
    username      TEXT COLLATE "C"    NOT NULL,
    password_hash TEXT COLLATE "C"    NOT NULL,
    created_at    TEXT COLLATE "C"    NOT NULL,
    updated_at    TEXT COLLATE "C"    NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token        TEXT COLLATE "C" PRIMARY KEY,
    csrf_token   TEXT COLLATE "C" NOT NULL,
    created_at   TEXT COLLATE "C" NOT NULL,
    expires_at   TEXT COLLATE "C" NOT NULL,
    last_seen_at TEXT COLLATE "C" NOT NULL
);
