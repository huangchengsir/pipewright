package dbmigration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
)

// Runtime coordinates a single instance. All methods are serialized; only the
// exclusive Control owner may construct one. No HTTP handler receives State or
// ActiveConfig directly because both contain encrypted-at-rest connection secrets.
type Runtime struct {
	info      DatabaseInfo
	stop      chan struct{}
	closeOnce sync.Once
	draining  bool
	mu        sync.Mutex
	Control   *Control
	Current   ActiveConfig
	Source    *store.Store
	key       *[32]byte
	Restart   func() error
	Recovery  bool
}

func NewRuntime(c *Control, current ActiveConfig, source *store.Store, key *[32]byte, recovery bool, restart func() error) *Runtime {
	r := &Runtime{stop: make(chan struct{}), Control: c, Current: current, Source: source, key: key, Recovery: recovery, Restart: restart}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.info, _ = r.queryInfo(ctx)
	return r
}

func OpenExisting(connection Connection) (*store.Store, error) {
	return OpenExistingContext(context.Background(), connection)
}

func OpenExistingContext(ctx context.Context, connection Connection) (*store.Store, error) {
	if connection.Driver == "sqlite" {
		info, e := os.Stat(connection.DSN)
		if e != nil || !info.Mode().IsRegular() {
			return nil, ErrIdentity
		}
	}
	s, e := store.ConnectContext(ctx, store.OpenConfig{Driver: connection.Driver, DSN: connection.DSN})
	if e != nil {
		if errors.Is(e, store.ErrDriverUnavailable) {
			return nil, ErrUnsupported
		}
		return nil, ErrConnection
	}
	return s, nil
}

// Preflight persists a task bound to the exact configuration and actual empty
// target. It never creates a target file, identity table, schema or business row.
func (r *Runtime) Preflight(ctx context.Context, target Connection, managed bool) (*State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Recovery || r.Source == nil || r.key == nil {
		return nil, ErrInvalidState
	}
	if target.Driver == r.Current.Connection.Driver {
		return nil, ErrUnsupported
	}
	if err := checkFence(ctx, r.Source, ""); err != nil {
		return nil, err
	}
	if err := VerifySecrets(ctx, r.Source, r.key); err != nil {
		return nil, err
	}
	if err := VerifyLocalResources(ctx, r.Source, r.key, r.Current.Paths); err != nil {
		return nil, err
	}
	actual, e := Describe(ctx, r.Source, r.Current.Connection.DSN)
	if e != nil || !exactEndpoint(actual, r.Current.Endpoint) {
		return nil, ErrIdentity
	}
	var endpoint Endpoint
	permissions := "initialization_required"
	if target.Driver == "sqlite" {
		absolute, e := filepath.Abs(target.DSN)
		if e != nil {
			return nil, ErrIdentity
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(absolute))
		if e != nil {
			return nil, ErrIdentity
		}
		target.DSN = filepath.Join(parent, filepath.Base(absolute))
		_, e = os.Lstat(target.DSN)
		if !errors.Is(e, os.ErrNotExist) {
			return nil, ErrTargetNotEmpty
		}
		endpoint = Endpoint{Dialect: "sqlite", FilePath: target.DSN}
	} else {
		db, e := OpenExistingContext(ctx, target)
		if e != nil {
			return nil, e
		}
		defer db.Close()
		permissions, e = targetPermissions(ctx, db)
		if e != nil {
			return nil, e
		}
		if e = CheckEmpty(ctx, db); e != nil {
			return nil, e
		}
		endpoint, e = Describe(ctx, db, "")
		if e != nil {
			return nil, e
		}
	}
	profile := ActiveConfig{Connection: target, Endpoint: endpoint, Paths: r.Current.Paths, Managed: managed}
	return r.Control.plan(r.Current, profile, permissions)
}

// Start persists the maintenance intent before requesting the admission fence.
// Any uncertain fence/exec outcome keeps the next startup in recovery mode.
func (r *Runtime) Start(ctx context.Context, task string, revision uint64) (*State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Recovery || r.Source == nil || r.Restart == nil {
		return nil, ErrInvalidState
	}
	if e := checkFence(ctx, r.Source, task); e != nil {
		return nil, e
	}
	s, e := r.Control.State()
	if e != nil {
		return nil, e
	}
	if s == nil || s.TaskID != task || s.Revision != revision {
		return nil, ErrInvalidState
	}
	if s.Phase == Prepared {
		s, e = r.Control.BeginQuiesce(task, revision)
		if e != nil {
			return nil, e
		}
		if e = maintenance.Drain(ctx, r.Source.DB, task); e != nil {
			return s, e
		}
	} else if s.Phase != Quiescing {
		return nil, ErrInvalidState
	} else {
		var fence string
		if r.Source.DB.QueryRowContext(ctx, "SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence) != nil {
			return nil, ErrInvalidState
		}
		if fence == "" {
			if e = maintenance.Drain(ctx, r.Source.DB, task); e != nil {
				return s, e
			}
		}
	}
	if !r.draining {
		r.draining = true
		go r.awaitDrain(task)
	}

	return s, nil
}
func checkFence(ctx context.Context, db *store.Store, allowed string) error {
	var task string
	if db.DB.QueryRowContext(ctx, "SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&task) != nil {
		return ErrInvalidState
	}
	if task != "" && task != allowed {
		return maintenance.ErrBlocked
	}
	var updates int
	if db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM selfupdate_tasks WHERE active_key IS NOT NULL").Scan(&updates) != nil || updates != 0 {
		return maintenance.ErrBlocked
	}
	return nil
}

func targetLocationMatches(expected, actual Endpoint) bool {
	if expected.Dialect == "sqlite" && expected.FileID == "" {
		return actual.Dialect == "sqlite" && expected.FilePath == actual.FilePath
	}
	return sameLocation(expected, actual)
}

// ContinueCopy is available only in a fresh process, before business bootstrap.
// A target with existing tables is accepted solely by its matching initialization
// receipt. An unknown/partial initialization is never blindly replayed.
func (r *Runtime) ContinueCopy(ctx context.Context, task string, revision uint64) (*State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Recovery || r.Source == nil {
		return nil, ErrInvalidState
	}
	s, e := r.Control.State()
	if e != nil {
		return nil, e
	}
	if s == nil || s.TaskID != task || s.Revision != revision {
		return nil, ErrInvalidState
	}
	if s.Phase == Quiescing {
		s, e = r.Control.Takeover(task, revision)
		if e != nil {
			return nil, e
		}
	}
	if s.Phase != Frozen && s.Phase != Copying {
		return nil, ErrInvalidState
	}
	source, e := Describe(ctx, r.Source, s.Source.Connection.DSN)
	if e != nil || !exactEndpoint(source, s.Source.Endpoint) {
		return nil, ErrIdentity
	}
	if e = checkFence(ctx, r.Source, task); e != nil {
		return nil, e
	}
	if e = maintenance.Inspect(ctx, r.Source.DB); e != nil {
		return nil, e
	}
	if e = VerifySecrets(ctx, r.Source, r.key); e != nil {
		return nil, e
	}
	if e = VerifyLocalResources(ctx, r.Source, r.key, s.Source.Paths); e != nil {
		return nil, e
	}
	if s.Target.Connection.Driver == "sqlite" {
		f, e := os.OpenFile(s.Target.Connection.DSN, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e == nil {
			e = f.Close()
		}
		if e != nil && !errors.Is(e, os.ErrExist) {
			return nil, ErrResources
		}
	}
	db, e := OpenExistingContext(ctx, s.Target.Connection)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	actual, e := Describe(ctx, db, s.Target.Connection.DSN)
	if e != nil || !targetLocationMatches(s.Target.Endpoint, actual) || SameEndpoint(source, actual) {
		return nil, ErrIdentity
	}
	var target *Target
	if s.Phase == Copying || actual.Identity != "" {
		if s.Phase == Copying && !exactEndpoint(actual, s.Target.Endpoint) {
			return nil, ErrIdentity
		}
		target, e = ResumeTarget(ctx, db, task, actual.Identity)
	} else {
		target, e = InitializeTarget(ctx, db, task)
	}
	if e != nil {
		return nil, e
	}
	actual, e = Describe(ctx, db, s.Target.Connection.DSN)
	if e != nil {
		return nil, e
	}
	if s.Phase == Frozen {
		s, e = r.Control.BeginCopy(task, s.Revision, actual)
		if e != nil {
			return nil, e
		}
	}
	report, e := snapshotProgress(ctx, r.Source, target, true, func(proof TableProof) error {
		var progressErr error
		s, progressErr = r.Control.RecordProgress(task, s.Revision, proof)
		return progressErr
	})
	if e != nil {
		return nil, e
	}
	if e = VerifySecrets(ctx, db, r.key); e != nil {
		return nil, e
	}
	if e = VerifyLocalResources(ctx, db, r.key, s.Source.Paths); e != nil {
		return nil, e
	}
	return r.Control.RecordVerified(task, s.Revision, report)
}

func (r *Runtime) Stage(ctx context.Context, task string, revision uint64) (*State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.Recovery {
		return nil, ErrInvalidState
	}
	s, e := r.Control.State()
	if e != nil {
		return nil, e
	}
	if s == nil || s.TaskID != task || s.Revision != revision || (s.Phase != Verified && s.Phase != ActivationIntent) {
		return nil, ErrInvalidState
	}
	db, e := OpenExistingContext(ctx, s.Target.Connection)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	actual, e := Describe(ctx, db, s.Target.Connection.DSN)
	if e != nil || !exactEndpoint(actual, s.Target.Endpoint) {
		return nil, ErrIdentity
	}
	t, e := ResumeTarget(ctx, db, task, actual.Identity)
	if e != nil {
		return nil, e
	}
	report, e := VerifySnapshot(ctx, r.Source, t)
	if e != nil || !reflect.DeepEqual(report, s.Report) {
		return nil, ErrMismatch
	}
	if e = VerifySecrets(ctx, db, r.key); e != nil {
		return nil, e
	}
	if e = VerifyLocalResources(ctx, db, r.key, s.Source.Paths); e != nil {
		return nil, e
	}
	if s.Phase == ActivationIntent {
		s, e = r.Control.ReconcileActivation(task, revision)
	} else {
		s, e = r.Control.StageActivation(task, revision)
	}
	if e != nil {
		return nil, e
	}
	if s.Target.Managed && r.Restart != nil {
		if e = r.Restart(); e != nil {
			return s, e
		}
	}
	return s, nil
}

func (r *Runtime) Abort(ctx context.Context, task string, revision uint64) (*State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, e := r.Control.State()
	if e != nil {
		return nil, e
	}
	if s == nil || s.TaskID != task || s.Revision != revision || s.Phase == Completed || s.Phase == Aborted || r.Source == nil {
		return nil, ErrInvalidState
	}
	actual, e := Describe(ctx, r.Source, s.Source.Connection.DSN)
	if e != nil || !exactEndpoint(actual, s.Source.Endpoint) {
		return nil, ErrIdentity
	}
	if e = checkFence(ctx, r.Source, task); e != nil {
		return nil, e
	}
	if e = r.Control.stageSourceRestore(task, revision); e != nil {
		return nil, e
	}
	if e = maintenance.Unfence(ctx, r.Source.DB, task); e != nil {
		return nil, ErrCommitUnknown
	}
	s, e = r.Control.RestoreSource(task, revision)
	if e != nil {
		return nil, e
	}
	if r.Recovery && r.Restart != nil {
		if e = r.Restart(); e != nil {
			return s, e
		}
	}
	return s, nil
}

func (r *Runtime) Close() { r.closeOnce.Do(func() { close(r.stop) }) }
func (r *Runtime) awaitDrain(task string) {
	defer func() { r.mu.Lock(); r.draining = false; r.mu.Unlock() }()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-timer.C:
		}
		r.mu.Lock()
		state, e := r.Control.State()
		if e != nil || state == nil || state.TaskID != task || state.Phase != Quiescing {
			r.mu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = checkFence(ctx, r.Source, task)
		var fence string
		if e == nil && (r.Source.DB.QueryRowContext(ctx, "SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence) != nil || fence != task) {
			e = ErrInvalidState
		}
		if e == nil {
			e = maintenance.Inspect(ctx, r.Source.DB)
		}
		cancel()
		if e == nil {
			e = r.Restart()
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
	}
}
func (r *Runtime) Blockers(ctx context.Context) ([]maintenance.Blocker, error) {
	if r.Recovery {
		s, e := r.Control.State()
		if e != nil {
			return nil, e
		}
		if s != nil && s.Phase != Quiescing && s.Phase != Frozen && s.Phase != Prepared {
			return []maintenance.Blocker{}, nil
		}
	}
	if r.Source == nil {
		return nil, ErrIdentity
	}
	e := maintenance.Inspect(ctx, r.Source.DB)
	var blocked *maintenance.BlockedError
	if errors.As(e, &blocked) {
		return blocked.Blockers, nil
	}
	if e != nil {
		return nil, ErrInvalidState
	}
	return []maintenance.Blocker{}, nil
}

// Reconcile resumes only an explicitly requested, authenticated handoff. A
// post-restart verification first inspects the transactional activation receipt.
// If the process has not yet loaded the target, only a managed restart is allowed.
func (r *Runtime) Reconcile(ctx context.Context, task string, revision uint64) (*State, error) {
	s, e := r.Control.State()
	if e != nil {
		return nil, e
	}
	if s == nil || s.TaskID != task || s.Revision != revision {
		return nil, ErrInvalidState
	}
	if s.Phase == ActivationIntent {
		return r.Stage(ctx, task, revision)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, e = r.Control.State()
	if e != nil {
		return nil, e
	}
	if !r.Recovery || r.Restart == nil || s == nil || s.TaskID != task || s.Revision != revision || s.Phase != AwaitingRestart {
		return nil, ErrInvalidState
	}
	active, e := r.Control.Active()
	if e != nil {
		return nil, e
	}
	if active == nil || profileDigest(*active) != profileDigest(s.Target) {
		return nil, ErrIdentity
	}
	if s.Owner != r.Control.owner && profileDigest(r.Current) == profileDigest(s.Target) {
		s, e = r.confirmBoot(ctx, r.Current)
		if e != nil {
			return nil, e
		}
	} else if !s.Target.Managed {
		return nil, ErrInvalidState
	}
	if r.Restart == nil {
		return nil, ErrInvalidState
	}
	if e = r.Restart(); e != nil {
		return s, e
	}
	return s, nil
}

// Admit also protects migration when the optional self-updater cannot initialize.
func (r *Runtime) Admit(ctx context.Context, kind string) (func(), error) {
	if r.Recovery || r.Source == nil {
		return nil, maintenance.ErrBlocked
	}
	return maintenance.Enter(ctx, r.Source.DB, kind)
}
