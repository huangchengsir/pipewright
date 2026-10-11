package opschat

import (
	"context"
	"database/sql"
	"time"
	"unicode/utf8"
)

type replySink struct {
	service             *Service
	ctx                 context.Context
	id, run, kind, text string
	seq                 int64
	persisted           int
	updates             int
	last                time.Time
	limit               int
	fallback            bool
}

func (s *Service) reply(ctx context.Context, id, run, kind string, limit int) *replySink {
	return &replySink{service: s, ctx: ctx, id: id, run: run, kind: kind, limit: limit}
}
func (p *replySink) chunk(chunk StreamChunk) error {
	if p.ctx.Err() != nil {
		return p.ctx.Err()
	}
	if chunk.Fallback && !p.fallback {
		p.fallback = true
		if err := p.service.persist(p.id, func(ctx context.Context, tx *sql.Tx) error {
			return p.service.append(ctx, tx, Entry{SessionID: p.id, RunID: p.run, Kind: "notice", Text: "Provider returned a non-streaming response. No second request was made."}, true, false)
		}); err != nil {
			return err
		}
	}
	if !utf8.ValidString(chunk.Text) || len(p.text)+len(chunk.Text) > p.limit {
		return ErrQuota
	}
	p.text += chunk.Text
	if p.text == "" || p.updates >= 180 {
		return nil
	}
	if p.seq == 0 || len(p.text)-p.persisted >= 256 || time.Since(p.last) >= 150*time.Millisecond {
		return p.flush("partial", false)
	}
	return nil
}
func (p *replySink) finish(text, status string, allowed bool) error {
	if len(text) > p.limit || !utf8.ValidString(text) {
		return ErrQuota
	}
	p.text = text
	if p.text == "" && p.seq == 0 {
		return nil
	}
	return p.flush(status, allowed)
}
func (p *replySink) flush(status string, allowed bool) error {
	s := p.service
	if err := s.registerSecrets(); err != nil {
		return err
	}
	text := s.scrub(p.text, p.limit)
	createdSeq := int64(0)
	err := s.persist(p.id, func(ctx context.Context, tx *sql.Tx) error {
		r, e := s.run(ctx, tx, p.id, p.run)
		if e != nil {
			return e
		}
		if terminal(r.View.Status) {
			return ErrConflict
		}
		if (r.View.CancelRequested || p.ctx.Err() != nil) && status == "partial" {
			return ErrConflict
		}
		if r.View.CancelRequested || p.ctx.Err() != nil {
			status = Interrupted
			allowed = false
		}
		if p.seq == 0 {
			if e = s.append(ctx, tx, Entry{SessionID: p.id, RunID: p.run, Kind: p.kind, Text: text, Status: status}, true, allowed); e != nil {
				return e
			}
			return tx.QueryRowContext(ctx, "SELECT seq FROM ops_chat_sessions WHERE id=?", p.id).Scan(&createdSeq)
		}
		var oldSize int
		if tx.QueryRowContext(ctx, "SELECT body_size FROM ops_chat_entries WHERE session_id=? AND seq=? AND run_id=?", p.id, p.seq, p.run).Scan(&oldSize) != nil {
			return ErrStorage
		}
		var revision int64
		if tx.QueryRowContext(ctx, "SELECT seq+1 FROM ops_chat_sessions WHERE id=?", p.id).Scan(&revision) != nil {
			return ErrStorage
		}
		body, e := s.seal(struct {
			Text, Status string
			RevisionSeq  int64
		}{text, status, revision})
		if e != nil {
			return e
		}
		a := 0
		if allowed {
			a = 1
		}
		if _, e = tx.ExecContext(ctx, "UPDATE ops_chat_entries SET body=?,body_size=?,context_allowed=? WHERE session_id=? AND seq=?", body, len(text), a, p.id, p.seq); e != nil {
			return ErrStorage
		}
		if _, e = tx.ExecContext(ctx, "UPDATE ops_chat_sessions SET body_bytes=body_bytes+? WHERE id=?", len(text)-oldSize, p.id); e != nil {
			return ErrStorage
		}
		return s.append(ctx, tx, Entry{SessionID: p.id, RunID: p.run, Kind: "entry_updated", ReplaceSeq: p.seq}, false, false)
	})
	if err == nil {
		if createdSeq != 0 {
			p.seq = createdSeq
		}
		p.persisted = len(p.text)
		p.last = time.Now()
		p.updates++
	}
	return err
}

func (s *Service) interruptReplies(ctx context.Context, tx *sql.Tx, r runRecord) error {
	rows, err := tx.QueryContext(ctx, "SELECT seq,body FROM ops_chat_entries WHERE session_id=? AND run_id=? AND kind IN ('assistant','analysis') AND visible=1", r.View.SessionID, r.View.ID)
	if err != nil {
		return ErrStorage
	}
	type pending struct {
		seq  int64
		text string
	}
	updates := []pending{}
	for rows.Next() {
		var seq int64
		var b []byte
		if rows.Scan(&seq, &b) != nil {
			rows.Close()
			return ErrStorage
		}
		var body struct{ Text, Status string }
		if err = s.open(b, &body); err != nil {
			rows.Close()
			return err
		}
		if body.Status == "partial" {
			updates = append(updates, pending{seq, body.Text})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrStorage
	}
	for _, u := range updates {
		var revision int64
		if tx.QueryRowContext(ctx, "SELECT seq+1 FROM ops_chat_sessions WHERE id=?", r.View.SessionID).Scan(&revision) != nil {
			return ErrStorage
		}
		b, err := s.seal(struct {
			Text, Status string
			RevisionSeq  int64
		}{u.text, Interrupted, revision})
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE ops_chat_entries SET body=?,context_allowed=0 WHERE session_id=? AND seq=?", b, r.View.SessionID, u.seq); err != nil {
			return ErrStorage
		}
		if err = s.append(ctx, tx, Entry{SessionID: r.View.SessionID, RunID: r.View.ID, Kind: "entry_updated", ReplaceSeq: u.seq}, false, false); err != nil {
			return err
		}
	}
	return nil
}
