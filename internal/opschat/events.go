package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

func pageLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, ErrInvalid
	}
	return limit, nil
}
func EventCursor(id string, seq int64) string { return id + ":" + strconv.FormatInt(seq, 10) }
func cursorSeq(id, cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 2 || parts[0] != id {
		return 0, ErrInvalid
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || n < 0 {
		return 0, ErrInvalid
	}
	return n, nil
}
func (s *Service) entryPage(ctx context.Context, q querier, id, cursor string, limit int, events bool) (EntryPage, error) {
	out := EntryPage{Entries: []Entry{}}
	limit, err := pageLimit(limit)
	if err != nil {
		return out, err
	}
	sr, err := s.session(ctx, q, id)
	if err != nil {
		return out, err
	}
	out.Watermark = sr.View.Watermark
	seq, err := cursorSeq(id, cursor)
	if err != nil {
		return out, err
	}
	if seq > out.Watermark {
		return out, ErrInvalid
	}
	query := `SELECT seq,kind,run_id,call_id,body,created_at FROM ops_chat_entries WHERE session_id=? AND visible=1`
	args := []any{id}
	if events {
		floor := max(int64(0), out.Watermark-4000)
		cutoff := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
		var first sql.NullInt64
		if q.QueryRowContext(ctx, "SELECT MIN(seq) FROM ops_chat_entries WHERE session_id=? AND seq>? AND created_at>=?", id, floor, cutoff).Scan(&first) != nil {
			return out, ErrStorage
		}
		if first.Valid {
			floor = max(floor, first.Int64-1)
		} else {
			floor = out.Watermark
		}
		if seq < floor {
			out.Reset = true
			out.Cursor = EventCursor(id, out.Watermark)
			return out, ErrReset
		}
		query = `SELECT seq,kind,run_id,call_id,body,created_at FROM ops_chat_entries WHERE session_id=? AND seq>? AND created_at>=? ORDER BY seq LIMIT ?`
		args = append(args, seq, cutoff, limit)
	} else {
		if cursor != "" {
			var count int
			if q.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_chat_entries WHERE session_id=? AND seq=? AND visible=1", id, seq).Scan(&count) != nil {
				return out, ErrStorage
			}
			if count != 1 {
				return out, ErrInvalid
			}
			query += " AND seq<?"
			args = append(args, seq)
		}
		query += " ORDER BY seq DESC LIMIT ?"
		args = append(args, limit)
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return out, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		e := Entry{SessionID: id}
		var b []byte
		var created int64
		if rows.Scan(&e.Seq, &e.Kind, &e.RunID, &e.CallID, &b, &created) != nil {
			return out, ErrStorage
		}
		var body struct {
			Text        string
			Status      string
			Summary     *RunSummary
			RefSeq      int64
			RevisionSeq int64
		}
		if err = s.open(b, &body); err != nil {
			return out, err
		}
		e.Text, e.Status = body.Text, body.Status
		e.Summary = body.Summary
		e.ReplaceSeq = body.RefSeq
		e.RevisionSeq = body.RevisionSeq
		if e.RevisionSeq == 0 {
			e.RevisionSeq = e.Seq
		}
		e.CreatedAt = time.UnixMilli(created).UTC()
		out.Entries = append(out.Entries, s.publicEntry(e))
	}
	if rows.Err() != nil {
		return out, ErrStorage
	}
	rows.Close()
	// Events carry references on disk and only the changed object over SSE.
	targetNames := map[entryTargetKey]map[string]string{}
	for i := range out.Entries {
		e := &out.Entries[i]
		if e.ReplaceSeq != 0 {
			var b []byte
			var kind string
			if q.QueryRowContext(ctx, "SELECT body,kind FROM ops_chat_entries WHERE session_id=? AND seq=?", id, e.ReplaceSeq).Scan(&b, &kind) != nil {
				return out, ErrStorage
			}
			var body struct {
				Text, Status string
				RevisionSeq  int64
			}
			if err = s.open(b, &body); err != nil {
				return out, err
			}
			e.Kind, e.Text, e.Status = kind, body.Text, body.Status
			e.RevisionSeq = body.RevisionSeq
		}
		if e.RunID != "" && (e.Kind == "assistant" || e.Kind == "analysis") {
			key := entryTargetKey{run: e.RunID, kind: e.Kind}
			names, loaded := targetNames[key]
			if !loaded {
				names, err = s.entryTargetNames(ctx, q, id, key)
				if err != nil {
					return out, err
				}
				targetNames[key] = names
			}
			e.TargetNames = names
		}
		if e.CallID != "" {
			c, err := s.call(ctx, q, id, e.CallID)
			if err != nil {
				return out, err
			}
			v := s.publicCall(c.Body.View)
			e.Call = &v
		}
		if e.RunID != "" && (e.Kind == "run_status" || e.Kind == "user" || e.Kind == "confirmation_ready" || e.Kind == "cancel_requested") {
			r, err := s.run(ctx, q, id, e.RunID)
			if err != nil {
				return out, err
			}
			e.Run = &r.View
			e.RequestID = r.View.ClientRequestID
		}
		if e.Kind == "run_status" || e.Kind == "session_updated" || e.Kind == "cancel_requested" {
			view := s.publicSession(sr.View)
			e.Session = &view
		}
		if e.Kind == "confirmation_ready" {
			a, err := s.approval(ctx, q, id, e.RunID)
			if err != nil {
				return out, err
			}
			view := s.publicConfirmation(a.View)
			e.Confirmation = &view
		}
		*e = s.publicEntry(*e)
		if events {
			encoded, err := json.Marshal(e)
			if err != nil {
				return out, ErrStorage
			}
			// Escaping can expand bounded SSH output sixfold. Snapshots keep
			// the full projection; this hint stays below the SSE parser cap.
			if len(encoded) > (256<<10)-1024 {
				*e = Entry{SessionID: id, Seq: e.Seq, Kind: "snapshot_required", CreatedAt: e.CreatedAt}
			}
		}
	}
	if len(out.Entries) > 0 {
		out.Cursor = EventCursor(id, out.Entries[len(out.Entries)-1].Seq)
		if !events {
			for i, j := 0, len(out.Entries)-1; i < j; i, j = i+1, j-1 {
				out.Entries[i], out.Entries[j] = out.Entries[j], out.Entries[i]
			}
		}
	} else if events {
		out.Cursor = EventCursor(id, seq)
	}
	return out, nil
}
func (s *Service) Entries(ctx context.Context, id, before string, limit int) (EntryPage, error) {
	var out EntryPage
	if err := s.registerSecrets(); err != nil {
		return out, err
	}
	err := s.transaction(ctx, false, func(tx *sql.Tx) error { var e error; out, e = s.entryPage(ctx, tx, id, before, limit, false); return e })
	return out, err
}
func (s *Service) Events(ctx context.Context, id, after string, limit int) (EntryPage, error) {
	var out EntryPage
	if err := s.registerSecrets(); err != nil {
		return out, err
	}
	err := s.transaction(ctx, false, func(tx *sql.Tx) error { var e error; out, e = s.entryPage(ctx, tx, id, after, limit, true); return e })
	return out, err
}
func (s *Service) List(ctx context.Context, before string, limit int) (SessionPage, error) {
	return s.ListContext(ctx, before, limit, "")
}
func (s *Service) ListContext(ctx context.Context, before string, limit int, key string) (SessionPage, error) {
	out := SessionPage{Sessions: []Session{}}
	if err := s.validateContext(ctx, key); err != nil {
		return out, err
	}
	limit, err := pageLimit(limit)
	if err != nil {
		return out, err
	}
	if err = s.registerSecrets(); err != nil {
		return out, err
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx) error {
		if key == "" {
			if tx.QueryRowContext(ctx, "SELECT active_session FROM ops_chat_state WHERE id=1").Scan(&out.ActiveSessionID) != nil {
				return ErrStorage
			}
		} else {
			err := tx.QueryRowContext(ctx, "SELECT session_id FROM ops_chat_contexts WHERE context_key=?", key).Scan(&out.ActiveSessionID)
			if err != nil && err != sql.ErrNoRows {
				return ErrStorage
			}
		}
		if before != "" {
			if _, e := s.session(ctx, tx, before); e != nil {
				return ErrInvalid
			}
		}
		query := "SELECT id FROM ops_chat_sessions"
		args := []any{}
		if before != "" {
			query += " WHERE id<?"
			args = append(args, before)
		}
		query += " ORDER BY id DESC LIMIT ?"
		args = append(args, limit+1)
		rows, e := tx.QueryContext(ctx, query, args...)
		if e != nil {
			return ErrStorage
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil {
				rows.Close()
				return ErrStorage
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return ErrStorage
		}
		if len(ids) > limit {
			out.Cursor = ids[limit-1]
			ids = ids[:limit]
		}
		for _, id := range ids {
			r, e := s.session(ctx, tx, id)
			if e != nil {
				return e
			}
			out.Sessions = append(out.Sessions, s.publicSession(r.View))
		}
		return nil
	})
	return out, err
}
func (s *Service) Snapshot(ctx context.Context, id string) (*Snapshot, error) {
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	out := &Snapshot{Runs: []Run{}, Calls: []Call{}, Confirmations: []Confirmation{}}
	err := s.transaction(ctx, false, func(tx *sql.Tx) error {
		sr, e := s.session(ctx, tx, id)
		if e != nil {
			return e
		}
		out.Session = s.publicSession(sr.View)
		out.Watermark = sr.View.Watermark
		out.Entries, e = s.entryPage(ctx, tx, id, "", 50, false)
		if e != nil {
			return e
		}
		visibleRuns := map[string]bool{sr.View.ActiveRunID: true}
		for _, entry := range out.Entries.Entries {
			if entry.RunID != "" {
				visibleRuns[entry.RunID] = true
			}
		}
		rows, e := tx.QueryContext(ctx, "SELECT id FROM ops_chat_runs WHERE session_id=? ORDER BY created_at DESC,id DESC LIMIT 100", id)
		if e != nil {
			return ErrStorage
		}
		ids := []string{}
		for rows.Next() {
			var rid string
			if rows.Scan(&rid) != nil {
				rows.Close()
				return ErrStorage
			}
			ids = append(ids, rid)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return ErrStorage
		}
		// The active run must remain visible even after many older analysis turns.
		if sr.View.ActiveRunID != "" {
			found := false
			for _, rid := range ids {
				found = found || rid == sr.View.ActiveRunID
			}
			if !found {
				ids = append(ids, sr.View.ActiveRunID)
			}
		}
		for _, rid := range ids {
			r, e := s.run(ctx, tx, id, rid)
			if e != nil {
				return e
			}
			out.Runs = append(out.Runs, r.View)
			if visibleRuns[rid] {
				calls, e := s.calls(ctx, tx, id, rid)
				if e != nil {
					return e
				}
				for _, c := range calls {
					out.Calls = append(out.Calls, s.publicCall(c.Body.View))
				}
			}
			a, e := s.approval(ctx, tx, id, rid)
			if e == nil {
				out.Confirmations = append(out.Confirmations, s.publicConfirmation(a.View))
			} else if e != ErrNotFound {
				return e
			}
		}
		// Older visible runs may fall outside the metadata window; entry projections
		// already carry their calls. Avoid duplicating current-page output in JSON.
		loaded := map[string]bool{}
		for _, call := range out.Calls {
			loaded[call.ID] = true
		}
		for i := range out.Entries.Entries {
			entry := &out.Entries.Entries[i]
			if entry.Call != nil {
				if !loaded[entry.Call.ID] {
					out.Calls = append(out.Calls, *entry.Call)
					loaded[entry.Call.ID] = true
				}
				entry.Call = nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Subscribe is a bounded wakeup hint only. HTTP must subscribe BEFORE Events,
// deduplicate shared seq, and poll Events every 15 seconds even without wakeups.
func (s *Service) Subscribe(ctx context.Context, id string) (<-chan struct{}, func(), error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, nil, err
	}
	ch := make(chan struct{}, 1)
	done := make(chan struct{})
	var once sync.Once
	s.mu.Lock()
	if !s.started || s.closing {
		s.mu.Unlock()
		return nil, nil, ErrUnavailable
	}
	if s.lost || s.root.Err() != nil {
		s.mu.Unlock()
		return nil, nil, ErrLease
	}
	count := 0
	for _, subs := range s.wake {
		count += len(subs)
	}
	if count >= 100 {
		s.mu.Unlock()
		return nil, nil, ErrQuota
	}
	if s.wake[id] == nil {
		s.wake[id] = map[chan struct{}]bool{}
	}
	s.wake[id][ch] = true
	s.mu.Unlock()
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			registered := s.wake[id][ch]
			delete(s.wake[id], ch)
			if len(s.wake[id]) == 0 {
				delete(s.wake, id)
			}
			if registered {
				close(ch)
			}
			s.mu.Unlock()
			close(done)
		})
	}
	s.mu.Lock()
	var rootDone <-chan struct{}
	if s.root != nil {
		rootDone = s.root.Done()
	}
	s.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		case <-rootDone:
			cancel()
		}
	}()
	return ch, cancel, nil
}
func (s *Service) notify(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.wake[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (s *Service) contextMessages(ctx context.Context, id string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,body FROM ops_chat_entries WHERE session_id=? AND context_allowed=1 ORDER BY seq DESC LIMIT 20`, id)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Message{}
	size := 0
	for rows.Next() {
		var kind string
		var b []byte
		if rows.Scan(&kind, &b) != nil {
			return nil, ErrStorage
		}
		var body struct {
			Text   string
			Status string
		}
		if err = s.open(b, &body); err != nil {
			return nil, err
		}
		text := s.scrub(body.Text, 8<<10)
		if size+len(text) > 32<<10 {
			break
		}
		size += len(text)
		out = append(out, Message{Role: kind, Text: text})
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
