package dbmigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// The only post-copy transformations are explicit and transactionally receipted:
// browser sessions and temporary login flows expire, our admission fence clears,
// and one immutable local audit row is appended. All historical business data,
// OAuth credentials, MCP grants and original audit records stay byte-for-byte.
func (t *Target) expectedCount(table string, n int64) int64 {
	if !t.activated {
		return n
	}
	switch table {
	case "sessions", "login_flows":
		return 0
	case "audit_log":
		return n + 1
	}
	return n
}
func (t *Target) verifyTransferredRow(ctx context.Context, table Table, row []any) error {
	if t.activated {
		switch table.Name {
		case "sessions", "login_flows":
			return nil
		case "selfupdate_control":
			row = append([]any(nil), row...)
			for i, c := range table.Columns {
				if c.Name == "task_id" {
					if row[i] != "" && row[i] != t.taskID {
						return ErrMismatch
					}
					row[i] = ""
				}
			}
		}
	}
	return verifyRow(ctx, t.db.DB, t.db.Dialect, table, row)
}
func activationAuditID(task string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(task+":database-activation")).String()
}
func (t *Target) verifyActivationAudit(ctx context.Context) error {
	var actor, action, kind, id, detail, ip, stamp, created string
	e := t.db.DB.QueryRowContext(ctx, "SELECT actor,action,target_type,target_id,detail_json,ip,timestamp,created_at FROM audit_log WHERE id=?", activationAuditID(t.taskID)).Scan(&actor, &action, &kind, &id, &detail, &ip, &stamp, &created)
	if e != nil || actor != "system" || action != "database_migration" || kind != "database" || id != t.taskID || detail != `{"sessionsInvalidated":true,"loginFlowsInvalidated":true}` || ip != "" || stamp != created {
		return ErrMismatch
	}
	if _, e = time.Parse(time.RFC3339, stamp); e != nil {
		return ErrMismatch
	}
	return nil
}

// ConfirmBoot runs only in a new process, before migrations, authentication
// bootstrap or workers. An unknown commit returns immediately; the next explicit
// attempt first inspects the receipt, then verifies the transformed target. It
// never repeats session invalidation or appends a second audit row blindly.
func (r *Runtime) ConfirmBoot(ctx context.Context, actual ActiveConfig) (*State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.confirmBoot(ctx, actual)
}

func (r *Runtime) confirmBoot(ctx context.Context, actual ActiveConfig) (*State, error) {
	if !r.Recovery || r.Source == nil {
		return nil, ErrInvalidState
	}
	s, e := r.Control.State()
	if e != nil {
		return nil, e
	}
	if s == nil || s.Phase != AwaitingRestart || s.Owner == r.Control.owner || profileDigest(actual) != profileDigest(s.Target) {
		return nil, ErrInvalidState
	}
	source, e := Describe(ctx, r.Source, s.Source.Connection.DSN)
	if e != nil || !exactEndpoint(source, s.Source.Endpoint) {
		return nil, ErrIdentity
	}
	db, e := OpenExistingContext(ctx, actual.Connection)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	endpoint, e := Describe(ctx, db, actual.Connection.DSN)
	if e != nil || !exactEndpoint(endpoint, s.Target.Endpoint) {
		return nil, ErrIdentity
	}
	target, e := ResumeTarget(ctx, db, s.TaskID, endpoint.Identity)
	if e != nil {
		return nil, e
	}
	digestInput := sha256.Sum256([]byte(reportDigest(s.Report) + ":" + profileDigest(s.Target)))
	expected := hex.EncodeToString(digestInput[:])
	count, digest, e := target.receipt(ctx, "__activation__", 0)
	switch {
	case e == nil:
		if count != 0 || digest != expected {
			return nil, ErrMismatch
		}
		target.activated = true
	case errors.Is(e, sql.ErrNoRows):
		report, e := VerifySnapshot(ctx, r.Source, target)
		if e != nil || reportDigest(report) != reportDigest(s.Report) {
			return nil, ErrMismatch
		}
		if e = VerifySecrets(ctx, db, r.key); e != nil {
			return nil, e
		}
		if e = VerifyLocalResources(ctx, db, r.key, s.Source.Paths); e != nil {
			return nil, e
		}
		tx, e := db.DB.BeginTx(ctx, nil)
		if e != nil {
			return nil, ErrCommitUnknown
		}
		defer tx.Rollback()
		for _, query := range []string{"DELETE FROM sessions", "DELETE FROM login_flows"} {
			if _, e = tx.ExecContext(ctx, query); e != nil {
				return nil, ErrMismatch
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE selfupdate_control SET task_id='' WHERE id=1 AND task_id=?", s.TaskID); e != nil {
			return nil, ErrMismatch
		}
		stamp := time.Now().UTC().Format(time.RFC3339)
		if _, e = tx.ExecContext(ctx, "INSERT INTO audit_log(id,timestamp,actor,action,target_type,target_id,detail_json,ip,created_at) VALUES(?,?,?,?,?,?,?,?,?)", activationAuditID(s.TaskID), stamp, "system", "database_migration", "database", s.TaskID, `{"sessionsInvalidated":true,"loginFlowsInvalidated":true}`, "", stamp); e != nil {
			return nil, ErrMismatch
		}
		if e = target.insertReceipt(ctx, tx, "__activation__", 0, 0, expected); e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, ErrCommitUnknown
		}
		target.activated = true
	default:
		return nil, ErrCommitUnknown
	}
	report, e := VerifySnapshot(ctx, r.Source, target)
	if e != nil || reportDigest(report) != reportDigest(s.Report) {
		return nil, ErrMismatch
	}
	if e = target.verifyActivationAudit(ctx); e != nil {
		return nil, e
	}
	if e = VerifySecrets(ctx, db, r.key); e != nil {
		return nil, e
	}
	if e = VerifyLocalResources(ctx, db, r.key, s.Source.Paths); e != nil {
		return nil, e
	}
	return r.Control.ConfirmActivation(s.TaskID, s.Revision, actual, report)
}
