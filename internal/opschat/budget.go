package opschat

import (
	"context"
	"database/sql"
)

const runStorageReserve int64 = 4 << 20

// LENGTH on BLOB/MEDIUMBLOB counts bytes in both dialects, including cipher overhead.
func (s *Service) storageBytes(ctx context.Context, q querier, id string) (int64, error) {
	var size int64
	query := `SELECT COALESCE((SELECT LENGTH(body) FROM ops_chat_sessions WHERE id=?),0)`
	args := []any{id}
	for _, table := range []string{"ops_chat_entries", "ops_chat_runs", "ops_chat_calls", "ops_chat_approvals"} {
		query += " + COALESCE((SELECT SUM(LENGTH(body)) FROM " + table + " WHERE session_id=?),0)"
		args = append(args, id)
	}
	if err := q.QueryRowContext(ctx, query, args...).Scan(&size); err != nil {
		return 0, ErrStorage
	}
	return size, nil
}
func (s *Service) runStorageBytes(ctx context.Context, q querier, id, run string) (int64, error) {
	var size int64
	query := `SELECT COALESCE((SELECT LENGTH(body) FROM ops_chat_runs WHERE session_id=? AND id=?),0)`
	args := []any{id, run}
	for _, table := range []string{"ops_chat_entries", "ops_chat_calls", "ops_chat_approvals"} {
		query += " + COALESCE((SELECT SUM(LENGTH(body)) FROM " + table + " WHERE session_id=? AND run_id=?),0)"
		args = append(args, id, run)
	}
	if err := q.QueryRowContext(ctx, query, args...).Scan(&size); err != nil {
		return 0, ErrStorage
	}
	return size, nil
}
func (s *Service) enforceStorage(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,active_run,visible_count FROM ops_chat_sessions")
	if err != nil {
		return ErrStorage
	}
	type pending struct {
		id, run string
		count   int
	}
	sessions := []pending{}
	for rows.Next() {
		var r pending
		if rows.Scan(&r.id, &r.run, &r.count) != nil {
			rows.Close()
			return ErrStorage
		}
		sessions = append(sessions, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrStorage
	}
	for _, r := range sessions {
		size, e := s.storageBytes(ctx, tx, r.id)
		if e != nil {
			return e
		}
		if size > 8<<20 || r.count > 2000 {
			return ErrQuota
		}
		if r.run != "" {
			used, e := s.runStorageBytes(ctx, tx, r.id, r.run)
			if e != nil {
				return e
			}
			// Only this run can spend its reserve. Draft/title edits count as historical storage.
			if size+max(int64(0), runStorageReserve-used) > 8<<20 {
				return ErrQuota
			}
		}
	}
	return nil
}

// StorageBytes is a pure, consistent read of actual encrypted serialized body sizes.
func (s *Service) StorageBytes(ctx context.Context, id string) (int64, error) {
	var size int64
	err := s.transaction(ctx, false, func(tx *sql.Tx) error {
		if _, e := s.session(ctx, tx, id); e != nil {
			return e
		}
		var e error
		size, e = s.storageBytes(ctx, tx, id)
		return e
	})
	return size, err
}
