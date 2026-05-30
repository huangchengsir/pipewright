-- 0009 audit_log:append-only 审计地基(NFR-7 / AC-SEC-03)。
-- 敏感操作(凭据增删改、trigger secret reset、项目增删改、手动触发运行)成功后,
-- 同步追加一行不可篡改的审计记录。审计存储与应用日志分离(独立表 + 可选远端 sink)。
--   id          : 审计条目唯一 id(UUID),也作分页游标的次序键
--   timestamp   : 事件发生时间(RFC3339,UTC);分页主游标
--   actor       : 操作者(如 admin / system)
--   action      : 操作枚举(snake_case):credential_create|credential_update|
--                 credential_delete|trigger_secret_reset|project_create|project_update|
--                 project_delete|run_trigger_manual(只增不改语义)
--   target_type : 目标类型(credential|project|trigger|run …)
--   target_id   : 目标主键(可空,如批量/无具体目标时)
--   detail_json : 结构化细节 JSON;**写入前已过 Masker,绝不含明文 secret/密文/master key**
--   ip          : 来源 IP(可空)
--   created_at  : 落库时间(RFC3339,UTC)
--
-- append-only 硬化:应用层只 INSERT / SELECT;下方 SQLite trigger 在
-- BEFORE UPDATE / BEFORE DELETE 时 RAISE(ABORT),从存储层硬拦篡改,即使绕过应用层
-- 直连 DB 也无法改写/删除已落审计(AC-SEC-03 不可篡改)。

CREATE TABLE IF NOT EXISTS audit_log (
    id          TEXT PRIMARY KEY,
    timestamp   TEXT NOT NULL,
    actor       TEXT NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   TEXT,
    detail_json TEXT NOT NULL DEFAULT '{}',
    ip          TEXT,
    created_at  TEXT NOT NULL
);

-- 分页/过滤索引:列表默认按 timestamp DESC, id DESC 取页;过滤常按 action / target_type。
CREATE INDEX IF NOT EXISTS idx_audit_log_timestamp ON audit_log (timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log (action);
CREATE INDEX IF NOT EXISTS idx_audit_log_target_type ON audit_log (target_type);

-- append-only 硬拦:任何 UPDATE / DELETE 直接 ABORT,审计真不可改。
CREATE TRIGGER IF NOT EXISTS audit_log_no_update
BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only: UPDATE forbidden');
END;

CREATE TRIGGER IF NOT EXISTS audit_log_no_delete
BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only: DELETE forbidden');
END;
