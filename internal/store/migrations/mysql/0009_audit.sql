-- 0009 audit_log(MySQL):append-only 审计;触发器用 SIGNAL 硬拦 UPDATE/DELETE。
-- 触发器用 DROP IF EXISTS + CREATE(而非 CREATE TRIGGER IF NOT EXISTS):后者需 MySQL
-- 8.0.29+,阿里云 RDS 等常见 8.0.28 会语法报错;DROP TRIGGER IF EXISTS 自 5.x 即支持,
-- 既兼容低版本又保持幂等(重跑先删后建)。
CREATE TABLE IF NOT EXISTS audit_log (
    id          VARCHAR(64) PRIMARY KEY,
    `timestamp` VARCHAR(32) NOT NULL,
    actor       VARCHAR(255) NOT NULL,
    action      VARCHAR(255) NOT NULL,
    target_type VARCHAR(255) NOT NULL,
    target_id   VARCHAR(64),
    detail_json LONGTEXT NOT NULL DEFAULT ('{}'),
    ip          VARCHAR(255),
    created_at  VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE INDEX idx_audit_log_timestamp ON audit_log (`timestamp` DESC, id DESC);
CREATE INDEX idx_audit_log_action ON audit_log (action);
CREATE INDEX idx_audit_log_target_type ON audit_log (target_type);

DROP TRIGGER IF EXISTS audit_log_no_update;
CREATE TRIGGER audit_log_no_update
BEFORE UPDATE ON audit_log FOR EACH ROW
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'audit_log is append-only: UPDATE forbidden';

DROP TRIGGER IF EXISTS audit_log_no_delete;
CREATE TRIGGER audit_log_no_delete
BEFORE DELETE ON audit_log FOR EACH ROW
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'audit_log is append-only: DELETE forbidden';
