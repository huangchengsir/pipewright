-- PostgreSQL: explicit schema, integer booleans, byte-preserving ciphertext.
CREATE TABLE IF NOT EXISTS audit_log (
    id          TEXT COLLATE "C" PRIMARY KEY,
    timestamp   TEXT COLLATE "C" NOT NULL,
    actor       TEXT COLLATE "C" NOT NULL,
    action      TEXT COLLATE "C" NOT NULL,
    target_type TEXT COLLATE "C" NOT NULL,
    target_id   TEXT COLLATE "C",
    detail_json TEXT COLLATE "C" NOT NULL DEFAULT '{}',
    ip          TEXT COLLATE "C",
    created_at  TEXT COLLATE "C" NOT NULL
);


CREATE INDEX IF NOT EXISTS idx_audit_log_timestamp ON audit_log (timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log (action);
CREATE INDEX IF NOT EXISTS idx_audit_log_target_type ON audit_log (target_type);


CREATE FUNCTION pipewright_audit_immutable() RETURNS trigger LANGUAGE plpgsql AS $audit$
BEGIN
 RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = '23514';
END;
$audit$;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log FOR EACH ROW EXECUTE FUNCTION pipewright_audit_immutable();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log FOR EACH ROW EXECUTE FUNCTION pipewright_audit_immutable();
