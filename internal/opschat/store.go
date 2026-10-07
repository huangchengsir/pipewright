package opschat

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type sessionRecord struct {
	View  Session
	Body  sessionBody
	Count int
	Bytes int
}
type runRecord struct {
	View Run
	Body runBody
	Hash string
}
type callRecord struct {
	Body    callBody
	Created int64
	Ordinal int
}
type approvalRecord struct {
	View     Confirmation
	ID       string
	Instance string
	Status   string
	Body     approvalBody
}

func databaseError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return ErrStorage
	}
	return nil
}
func (s *Service) transaction(ctx context.Context, lease bool, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if lease {
		// Writes serialize on the singleton. Pure reads never update records.
		if _, err = tx.ExecContext(ctx, "UPDATE ops_chat_state SET lease_until=lease_until WHERE id=1"); err != nil {
			return ErrStorage
		}
		var owner string
		var until int64
		if err = tx.QueryRowContext(ctx, "SELECT lease_owner,lease_until FROM ops_chat_state WHERE id=1").Scan(&owner, &until); err != nil {
			return ErrStorage
		}
		if owner != s.instance || until <= time.Now().UnixMilli() {
			return ErrLease
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if lease {
		if err = s.enforceStorage(ctx, tx); err != nil {
			return err
		}
		var owner string
		var until int64
		if tx.QueryRowContext(ctx, "SELECT lease_owner,lease_until FROM ops_chat_state WHERE id=1").Scan(&owner, &until) != nil {
			return ErrStorage
		}
		if owner != s.instance || until <= time.Now().UnixMilli() {
			return ErrLease
		}
	}
	if err = tx.Commit(); err != nil {
		return ErrStorage
	}
	return nil
}
func (s *Service) session(ctx context.Context, q querier, id string) (sessionRecord, error) {
	var r sessionRecord
	var b []byte
	var created, updated int64
	err := q.QueryRowContext(ctx, `SELECT id,revision,seq,visible_count,body_bytes,active_run,body,created_at,updated_at FROM ops_chat_sessions WHERE id=?`, id).
		Scan(&r.View.ID, &r.View.Revision, &r.View.Watermark, &r.Count, &r.Bytes, &r.View.ActiveRunID, &b, &created, &updated)
	if err != nil {
		return r, databaseError(err)
	}
	if err = s.open(b, &r.Body); err != nil {
		return r, err
	}
	r.View.Title, r.View.Draft, r.View.ServerIDs = r.Body.Title, r.Body.Draft, r.Body.ServerIDs
	r.View.CreatedAt, r.View.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return r, nil
}
func (s *Service) saveSession(ctx context.Context, tx *sql.Tx, r sessionRecord) error {
	b, err := s.seal(r.Body)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE ops_chat_sessions SET revision=?,body=?,updated_at=? WHERE id=?`, r.View.Revision, b, time.Now().UnixMilli(), r.View.ID)
	return databaseError(err)
}
func (s *Service) run(ctx context.Context, q querier, session, id string) (runRecord, error) {
	var r runRecord
	var b []byte
	var created int64
	var canceled int
	err := q.QueryRowContext(ctx, `SELECT id,session_id,request_id,payload_hash,status,cancel_requested,body,created_at FROM ops_chat_runs WHERE session_id=? AND id=?`, session, id).
		Scan(&r.View.ID, &r.View.SessionID, &r.View.ClientRequestID, &r.Hash, &r.View.Status, &canceled, &b, &created)
	if err != nil {
		return r, databaseError(err)
	}
	r.View.CancelRequested = canceled != 0
	r.View.CreatedAt = time.UnixMilli(created).UTC()
	err = s.open(b, &r.Body)
	return r, err
}
func (s *Service) saveRun(ctx context.Context, tx *sql.Tx, r runRecord) error {
	b, err := s.seal(r.Body)
	if err != nil {
		return err
	}
	canceled := 0
	if r.View.CancelRequested {
		canceled = 1
	}
	_, err = tx.ExecContext(ctx, `UPDATE ops_chat_runs SET status=?,cancel_requested=?,body=? WHERE id=? AND session_id=?`, r.View.Status, canceled, b, r.View.ID, r.View.SessionID)
	return databaseError(err)
}
func (s *Service) calls(ctx context.Context, q querier, session, run string) ([]callRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT body,status,created_at,ordinal FROM ops_chat_calls WHERE session_id=? AND run_id=? ORDER BY ordinal`, session, run)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []callRecord{}
	for rows.Next() {
		var b []byte
		var c callRecord
		var status string
		if rows.Scan(&b, &status, &c.Created, &c.Ordinal) != nil {
			return nil, ErrStorage
		}
		if err = s.open(b, &c.Body); err != nil {
			return nil, err
		}
		c.Body.View.Status = status
		out = append(out, c)
	}
	return out, databaseError(rows.Err())
}
func (s *Service) saveCall(ctx context.Context, tx *sql.Tx, c callRecord) error {
	b, err := s.seal(c.Body)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE ops_chat_calls SET status=?,args_hash=?,body=?,ordinal=? WHERE id=? AND session_id=? AND run_id=?`,
		c.Body.View.Status, c.Body.View.ArgsHash, b, c.Ordinal, c.Body.View.ID, c.Body.View.SessionID, c.Body.View.RunID)
	return databaseError(err)
}
func (s *Service) approval(ctx context.Context, q querier, session, run string) (approvalRecord, error) {
	var a approvalRecord
	var b []byte
	var expires int64
	err := q.QueryRowContext(ctx, `SELECT id,status,instance_id,expires_at,body FROM ops_chat_approvals WHERE session_id=? AND run_id=?`, session, run).
		Scan(&a.ID, &a.Status, &a.Instance, &expires, &b)
	if err != nil {
		return a, databaseError(err)
	}
	if err = s.open(b, &a.Body); err != nil {
		return a, err
	}
	a.View = Confirmation{RunID: run, ExpiresAt: time.UnixMilli(expires).UTC(), Calls: a.Body.Calls}
	a.View.Valid = a.Status == "pending" && a.Instance == s.instance && expires > time.Now().UnixMilli()
	if a.View.Valid {
		a.View.Nonce = a.Body.Nonce
	}
	return a, nil
}
func (s *Service) append(ctx context.Context, tx *sql.Tx, e Entry, visible, allowed bool) error {
	e.CreatedAt = time.Now().UTC()
	b, err := s.seal(struct {
		Text   string
		Status string
	}{e.Text, e.Status})
	if err != nil {
		return err
	}
	v, a := 0, 0
	if visible {
		v = 1
	}
	if allowed {
		a = 1
	}
	size := len(e.Text)
	if _, err = tx.ExecContext(ctx, `UPDATE ops_chat_sessions SET seq=seq+1,visible_count=visible_count+?,body_bytes=body_bytes+?,updated_at=? WHERE id=?`, v, size, e.CreatedAt.UnixMilli(), e.SessionID); err != nil {
		return ErrStorage
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, "SELECT seq FROM ops_chat_sessions WHERE id=?", e.SessionID).Scan(&seq); err != nil {
		return ErrStorage
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_chat_entries(session_id,seq,kind,run_id,call_id,context_allowed,visible,body,body_size,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		e.SessionID, seq, e.Kind, e.RunID, e.CallID, a, v, b, size, e.CreatedAt.UnixMilli())
	if err != nil {
		return ErrStorage
	}
	// Retain visible history independently from the bounded event replay window.
	_, err = tx.ExecContext(ctx, `DELETE FROM ops_chat_entries WHERE session_id=? AND visible=0 AND (seq<=? OR created_at<?)`, e.SessionID, seq-4000, time.Now().Add(-30*24*time.Hour).UnixMilli())
	return databaseError(err)
}
func (s *Service) finishTx(ctx context.Context, tx *sql.Tx, r runRecord, status string) error {
	if !terminal(r.View.Status) && (status == Failed || status == Interrupted && r.View.Status == Planning) {
		text := "Operation could not be completed."
		if r.View.Status == Planning {
			text = "Planning could not be completed."
			if r.Body.Analysis != nil {
				text = "Analysis could not be completed."
			}
		}
		if err := s.append(ctx, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "assistant", Text: text, Status: status}, true, false); err != nil {
			return err
		}
	}
	r.View.Status = status
	if err := s.saveRun(ctx, tx, r); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ops_chat_sessions SET active_run='' WHERE id=? AND active_run=?", r.View.SessionID, r.View.ID); err != nil {
		return ErrStorage
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ops_chat_approvals SET status='invalid' WHERE run_id=? AND status='pending'", r.View.ID); err != nil {
		return ErrStorage
	}
	return s.append(ctx, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "run_status", Status: status}, false, false)
}
func terminal(status string) bool {
	switch status {
	case Succeeded, PartialFailed, Failed, Interrupted, Unknown:
		return true
	}
	return false
}
func activeBudget(r sessionRecord) bool { return r.Count+27 <= 2000 }
