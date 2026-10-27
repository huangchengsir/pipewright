package selfupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

func (s *Service) load(ctx context.Context, where string, args ...any) (*Task, error) {
	var raw string
	a := []any{s.instance}
	a = append(a, args...)
	e := s.db.QueryRowContext(ctx, "SELECT record_json FROM selfupdate_tasks WHERE instance_id=? AND "+where, a...).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	var st storedTask
	if e = json.Unmarshal([]byte(raw), &st); e != nil {
		return nil, e
	}
	t := st.Task
	t.ETag = st.ETag
	t.LastResumeID = st.ResumeID
	return &t, nil
}
func encoded(t *Task) (string, error) {
	b, e := json.Marshal(storedTask{Task: *t, ETag: t.ETag, ResumeID: t.LastResumeID})
	return string(b), e
}
func (s *Service) save(ctx context.Context, t *Task) error {
	return s.saveWith(ctx, s.db, t)
}
func (s *Service) saveWith(ctx context.Context, q configstate.DBTX, t *Task) error {
	t.UpdatedAt = time.Now().UTC()
	raw, e := encoded(t)
	if e != nil {
		return e
	}
	var active any = s.instance
	if terminal(t.State) {
		active = nil
	}
	r, e := q.ExecContext(ctx, "UPDATE selfupdate_tasks SET status=?,active_key=?,record_json=? WHERE id=? AND instance_id=?", t.State, active, raw, t.ID, s.instance)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) Get(ctx context.Context, id string) (*Task, error) { return s.load(ctx, "id=?", id) }
func (s *Service) Current(ctx context.Context) (*Task, error) {
	return s.load(ctx, "1=1 ORDER BY CASE WHEN active_key IS NULL THEN 1 ELSE 0 END,created_at DESC,id DESC LIMIT 1")
}
func (s *Service) ForTarget(ctx context.Context, target string) (*Task, error) {
	return s.load(ctx, "target_tag=? ORDER BY created_at DESC,id DESC LIMIT 1", target)
}

func (s *Service) finish(parent context.Context, t *Task, state, reason string) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	t.State, t.Reason = state, reason
	// Final state and its exact-owner fence release must survive a crash together.
	return configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		if e := s.saveWith(ctx, q, t); e != nil {
			return e
		}
		_, e := q.ExecContext(ctx, "UPDATE selfupdate_control SET task_id='' WHERE id=1 AND task_id=?", t.ID)
		return e
	})
}
