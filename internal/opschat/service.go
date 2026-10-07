package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type job struct {
	retry   bool
	session string
	cancel  context.CancelFunc
	done    chan struct{}
}
type Service struct {
	db          *sql.DB
	vault       vault.Vault
	executor    target.LimitedExecutor
	model       Model
	audit       audit.Recorder
	masker      *mask.Masker
	instance    string
	mu          sync.Mutex
	root        context.Context
	stop        context.CancelFunc
	started     bool
	closing     bool
	lost        bool
	jobs        map[string]*job
	wake        map[string]map[chan struct{}]bool
	slots       chan struct{}
	serverSlots map[string]chan struct{}
	leaseDone   chan struct{}
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Vault == nil {
		return nil, ErrUnavailable
	}
	if o.Masker == nil {
		o.Masker = mask.NewMasker()
	}
	s := &Service{db: o.DB, vault: o.Vault, executor: o.Executor, model: o.Model, audit: o.LocalRecorder, masker: o.Masker, instance: uuid.NewString(), jobs: map[string]*job{}, wake: map[string]map[chan struct{}]bool{}, slots: make(chan struct{}, 4), serverSlots: map[string]chan struct{}{}}
	check, err := s.seal("pipewright.opschat.keycheck.v1")
	if err != nil {
		return nil, err
	}
	var stored []byte
	err = s.db.QueryRow("SELECT keycheck FROM ops_chat_state WHERE id=1").Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if s.db.QueryRow("SELECT COUNT(*) FROM ops_chat_sessions").Scan(&count) != nil {
			return nil, ErrStorage
		}
		if count != 0 {
			return nil, ErrDecrypt
		}
		_, err = s.db.Exec("INSERT INTO ops_chat_state(id,keycheck) VALUES(1,?)", check)
		if err != nil {
			if s.db.QueryRow("SELECT keycheck FROM ops_chat_state WHERE id=1").Scan(&stored) != nil {
				return nil, ErrStorage
			}
		} else {
			stored = check
		}
	} else if err != nil {
		return nil, ErrStorage
	}
	var plain string
	if s.open(stored, &plain) != nil || plain != "pipewright.opschat.keycheck.v1" {
		return nil, ErrDecrypt
	}
	return s, nil
}
func (s *Service) Capabilities() Capabilities {
	p := ProviderInfo{}
	if s.model != nil {
		p = s.model.Info()
	}
	s.mu.Lock()
	available := s.started && !s.closing && !s.lost
	s.mu.Unlock()
	return Capabilities{Available: available, ModelAvailable: s.model != nil && validProvider(p), Provider: p, Limits: resourceLimits}
}
func (s *Service) ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.closing {
		return ErrUnavailable
	}
	if s.lost {
		return ErrLease
	}
	return nil
}
func validID(id string) bool { _, err := uuid.Parse(id); return err == nil }
func validTargets(ids []string) bool {
	if len(ids) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *Service) Create(ctx context.Context, in CreateInput) (*Session, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validTargets(in.ServerIDs) || !utf8.ValidString(in.Title) || utf8.RuneCountInString(in.Title) > 80 {
		return nil, ErrInvalid
	}
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	title := s.scrub(in.Title, 320)
	named := title != ""
	if title == "" {
		title = "New session"
	}
	body := sessionBody{Title: title, ServerIDs: append([]string{}, in.ServerIDs...), Named: named}
	sealed, err := s.seal(body)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	now := time.Now().UnixMilli()
	err = s.transaction(ctx, true, func(tx *sql.Tx) error {
		var count int
		if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_chat_sessions").Scan(&count) != nil {
			return ErrStorage
		}
		if count >= 100 {
			return ErrQuota
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO ops_chat_sessions(id,revision,body,created_at,updated_at) VALUES(?,1,?,?,?)", id, sealed, now, now)
		if e != nil {
			return ErrStorage
		}
		return s.append(ctx, tx, Entry{SessionID: id, Kind: "session_updated"}, false, false)
	})
	if err != nil {
		return nil, err
	}
	s.notify(id)
	return s.Get(ctx, id)
}
func (s *Service) Get(ctx context.Context, id string) (*Session, error) {
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	r, err := s.session(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	view := s.publicSession(r.View)
	return &view, nil
}
func (s *Service) Patch(ctx context.Context, id string, in PatchInput) (*Session, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if in.Title != nil && (!utf8.ValidString(*in.Title) || utf8.RuneCountInString(*in.Title) > 80) {
		return nil, ErrInvalid
	}
	if in.Draft != nil && (!utf8.ValidString(*in.Draft) || len(*in.Draft) > 8<<10) {
		return nil, ErrInvalid
	}
	if in.ServerIDs != nil && !validTargets(*in.ServerIDs) {
		return nil, ErrInvalid
	}
	if err := s.registerSecrets(); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, true, func(tx *sql.Tx) error {
		r, e := s.session(ctx, tx, id)
		if e != nil {
			return e
		}
		if r.View.Revision != in.Revision {
			return ErrConflict
		}
		if in.ServerIDs != nil && r.View.ActiveRunID != "" {
			return ErrConflict
		}
		if in.Title != nil {
			r.Body.Title = s.scrub(*in.Title, 320)
			r.Body.Named = true
		}
		if in.Draft != nil {
			r.Body.Draft = s.scrub(*in.Draft, 8<<10)
		}
		if in.ServerIDs != nil {
			r.Body.ServerIDs = append([]string{}, (*in.ServerIDs)...)
		}
		r.View.Revision++
		if e = s.saveSession(ctx, tx, r); e != nil {
			return e
		}
		return s.append(ctx, tx, Entry{SessionID: id, Kind: "session_updated"}, false, false)
	})
	if err != nil {
		return nil, err
	}
	s.notify(id)
	return s.Get(ctx, id)
}
func (s *Service) Activate(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.transaction(ctx, true, func(tx *sql.Tx) error {
		if _, err := s.session(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE ops_chat_state SET active_session=? WHERE id=1", id)
		return databaseError(err)
	})
}
func (s *Service) duplicate(ctx context.Context, session, request, hash string) (*Run, error) {
	var id, chat, payload string
	err := s.db.QueryRowContext(ctx, "SELECT id,session_id,payload_hash FROM ops_chat_runs WHERE request_id=?", request).Scan(&id, &chat, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrStorage
	}
	if chat != session || payload != hash {
		return nil, ErrConflict
	}
	r, err := s.run(ctx, s.db, session, id)
	if err != nil {
		return nil, err
	}
	return &r.View, nil
}
func (s *Service) Submit(ctx context.Context, id string, in TurnInput) (*Run, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validID(in.ClientRequestID) || !utf8.ValidString(in.Text) || len(in.Text) > 8<<10 {
		return nil, ErrInvalid
	}
	if (in.Text == "") == (in.ToolID == "") {
		return nil, ErrInvalid
	}
	if in.ToolID != "" {
		a, err := parseArgs(in.ToolID, in.Args)
		if err != nil {
			return nil, err
		}
		in.Args, _ = json.Marshal(a)
	} else if len(in.Args) > 0 {
		return nil, ErrInvalid
	}
	hash := digest(in)
	if old, err := s.duplicate(ctx, id, in.ClientRequestID, hash); old != nil || err != nil {
		return old, err
	}
	if in.ToolID == "" && (s.model == nil || !validProvider(s.model.Info())) {
		return nil, ErrUnavailable
	}
	if in.ToolID != "" && s.executor == nil {
		return nil, ErrUnavailable
	}
	chat, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if chat.Revision != in.Revision {
		return nil, ErrConflict
	}
	if in.ToolID != "" && len(chat.ServerIDs) == 0 {
		return nil, ErrInvalid
	}
	snapshots := []target.ConnectionSnapshot{}
	for _, server := range chat.ServerIDs {
		if s.executor == nil {
			return nil, ErrUnavailable
		}
		snap, e := s.executor.Capture(ctx, server)
		if e != nil {
			return nil, ErrConflict
		}
		if snap.Server.ID != server {
			return nil, ErrInvalid
		}
		snapshots = append(snapshots, snap)
	}
	if err = s.registerSecrets(); err != nil {
		return nil, err
	}
	if in.ToolID != "" {
		args, e := parseArgs(in.ToolID, in.Args)
		if e != nil || s.secretArgs(args) {
			return nil, ErrInvalid
		}
	}
	in.Text = s.scrub(in.Text, 8<<10)
	return s.enqueue(ctx, id, in.ClientRequestID, hash, runBody{Turn: in, Targets: snapshots}, in.Revision)
}
func (s *Service) enqueue(ctx context.Context, id, request, hash string, body runBody, revision int64) (*Run, error) {
	for _, snap := range body.Targets {
		encoded, e := json.Marshal(snap)
		if e != nil || len(encoded) > 8<<10 {
			return nil, ErrQuota
		}
	}
	runID := uuid.NewString()
	now := time.Now().UTC()
	r := runRecord{View: Run{ID: runID, SessionID: id, ClientRequestID: request, Status: Queued, CreatedAt: now}, Body: body, Hash: hash}
	var old *Run
	err := s.transaction(ctx, true, func(tx *sql.Tx) error {
		var existing, chat, payload string
		e := tx.QueryRowContext(ctx, "SELECT id,session_id,payload_hash FROM ops_chat_runs WHERE request_id=?", request).Scan(&existing, &chat, &payload)
		if e == nil {
			if chat != id || payload != hash {
				return ErrConflict
			}
			rr, e := s.run(ctx, tx, id, existing)
			if e != nil {
				return e
			}
			old = &rr.View
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return ErrStorage
		}
		sr, e := s.session(ctx, tx, id)
		if e != nil {
			return e
		}
		if sr.View.Revision != revision || sr.View.ActiveRunID != "" {
			return ErrConflict
		}
		if !activeBudget(sr) {
			return ErrQuota
		}
		if e = s.validateRetryTx(ctx, tx, id, body, sr); e != nil {
			return e
		}
		b, e := s.seal(body)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)`, runID, id, request, hash, Queued, b, now.UnixMilli())
		if e != nil {
			return ErrStorage
		}
		if _, e = tx.ExecContext(ctx, "UPDATE ops_chat_sessions SET active_run=? WHERE id=? AND active_run=''", runID, id); e != nil {
			return ErrStorage
		}
		if body.Analysis == nil {
			if !sr.Body.Named && sr.Count == 0 && body.Turn.Text != "" {
				t := strings.SplitN(body.Turn.Text, "\n", 2)[0]
				runes := []rune(t)
				if len(runes) > 40 {
					runes = runes[:40]
				}
				sr.Body.Title = string(runes)
				if e = s.saveSession(ctx, tx, sr); e != nil {
					return e
				}
			}
			text := body.Turn.Text
			allowed := true
			kind := "user"
			if body.Turn.ToolID != "" {
				text = body.Turn.ToolID
				allowed = false
				kind = "tool_request"
			}
			if len(body.RetryActions) > 0 {
				text = "retry_failed"
				allowed = false
				kind = "tool_request"
			}
			if e = s.append(ctx, tx, Entry{SessionID: id, RunID: runID, Kind: kind, Text: text}, true, allowed); e != nil {
				return e
			}
		}
		return s.append(ctx, tx, Entry{SessionID: id, RunID: runID, Kind: "run_status", Status: Queued}, false, false)
	})
	if err != nil {
		return nil, err
	}
	if old != nil {
		return old, nil
	}
	s.notify(id)
	s.launch(id, runID)
	return &r.View, nil
}
func (s *Service) Cancel(ctx context.Context, id, runID string) (*Run, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	err := s.transaction(ctx, true, func(tx *sql.Tx) error {
		r, e := s.run(ctx, tx, id, runID)
		if e != nil {
			return e
		}
		if terminal(r.View.Status) {
			return nil
		}
		r.View.CancelRequested = true
		if e = s.saveRun(ctx, tx, r); e != nil {
			return e
		}
		if r.View.Status == AwaitingConfirmation || r.View.Status == AwaitingAnalysisConsent {
			calls, e := s.calls(ctx, tx, id, runID)
			if e != nil {
				return e
			}
			for _, c := range calls {
				if !terminal(c.Body.View.Status) {
					c.Body.View.Status = Interrupted
					if e = s.saveCall(ctx, tx, c); e != nil {
						return e
					}
				}
			}
			return s.finishTx(ctx, tx, r, Interrupted)
		}
		return s.append(ctx, tx, Entry{SessionID: id, RunID: runID, Kind: "cancel_requested"}, false, false)
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if j := s.jobs[runID]; j != nil {
		j.cancel()
	}
	s.mu.Unlock()
	s.notify(id)
	r, err := s.run(ctx, s.db, id, runID)
	if err != nil {
		return nil, err
	}
	return &r.View, nil
}
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	// Explicit Cancel is required before deleting an active session.
	sr, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if sr.ActiveRunID != "" {
		return ErrConflict
	}
	s.mu.Lock()
	for _, j := range s.jobs {
		if j.session != id {
			continue
		}
		select {
		case <-j.done:
		default:
			s.mu.Unlock()
			return ErrConflict
		}
	}
	s.mu.Unlock()
	err = s.transaction(ctx, true, func(tx *sql.Tx) error {
		r, e := s.session(ctx, tx, id)
		if e != nil {
			return e
		}
		if r.View.ActiveRunID != "" {
			return ErrConflict
		}
		if _, e = tx.ExecContext(ctx, "UPDATE ops_chat_state SET active_session='' WHERE id=1 AND active_session=?", id); e != nil {
			return ErrStorage
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM ops_chat_sessions WHERE id=?", id)
		return databaseError(e)
	})
	if err == nil {
		s.notify(id)
	}
	return err
}
