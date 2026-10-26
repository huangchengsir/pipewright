package selfupdate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/version"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var tagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)
var requestPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)

func New(db *sql.DB, opts Options) (*Service, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	if opts.Repository == "" {
		opts.Repository = strings.TrimSpace(os.Getenv("PIPEWRIGHT_RELEASE_REPO"))
		if opts.Repository == "" {
			opts.Repository = "huangchengsir/pipewright"
		}
	}
	if opts.BaseURL != "" && !loopbackBase(opts.BaseURL) {
		return nil, ErrInvalid
	}
	if !repoPattern.MatchString(opts.Repository) {
		return nil, ErrInvalid
	}
	if opts.Executable == "" {
		var e error
		opts.Executable, e = os.Executable()
		if e != nil {
			return nil, e
		}
	}
	exe, e := filepath.EvalSymlinks(opts.Executable)
	if e != nil {
		return nil, e
	}
	exe, e = filepath.Abs(exe)
	if e != nil {
		return nil, e
	}
	opts.Executable = exe
	if opts.Directory == "" {
		opts.Directory = filepath.Join(filepath.Dir(exe), ".pipewright-updates")
	}
	if opts.Platform == "" {
		opts.Platform = runtime.GOOS + "_" + runtime.GOARCH
	}
	if opts.Current == nil {
		opts.Current = version.Get
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	if opts.TotalTimeout == 0 {
		opts.TotalTimeout = 10 * time.Minute
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = time.Minute
	}
	if opts.Install == nil {
		opts.Install = func(a, b string) error {
			if err := os.Rename(a, b); err != nil {
				return err
			}
			restoreExecutableContext(b)
			return nil
		}
	}
	if e = os.MkdirAll(opts.Directory, 0700); e != nil {
		return nil, e
	}
	st, e := os.Lstat(opts.Directory)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnavailable
	}
	root, e := os.OpenRoot(opts.Directory)
	if e != nil {
		return nil, e
	}
	f, e := root.OpenFile("engine.lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		root.Close()
		return nil, e
	}
	if e = lockFile(f); e != nil {
		f.Close()
		root.Close()
		return nil, ErrUnavailable
	}
	host, _ := os.Hostname()
	h := sha256.Sum256([]byte(host + "\x00" + exe))
	s := &Service{db: db, opts: opts, instance: hex.EncodeToString(h[:]), root: root, lock: f, running: map[string]bool{}}
	if e = s.reconcile(context.Background()); e != nil {
		unlockFile(f)
		f.Close()
		root.Close()
		return nil, e
	}
	return s, nil
}
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		s.ctx, s.cancel = context.WithCancel(ctx)
	}
}
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	unlockFile(s.lock)
	_ = s.lock.Close()
	_ = s.root.Close()
}
func (s *Service) Submit(ctx context.Context, in Request) (*Task, error) {
	if !requestPattern.MatchString(in.RequestID) || !tagPattern.MatchString(in.Target) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx == nil {
		return nil, ErrUnavailable
	}
	if old, e := s.load(ctx, "request_id=?", in.RequestID); e == nil {
		if old.Target != in.Target {
			return nil, ErrConflict
		}
		// Startup reconciliation turns a previous process's accepted task into
		// interrupted. An accepted row here belongs to this live service and has
		// not crossed the installation boundary; explicit retry can start it.
		if old.State == "accepted" && !s.running[old.ID] {
			s.launch(*old)
		}
		return old, nil
	} else if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	if _, e := s.load(ctx, "active_key IS NOT NULL"); e == nil {
		return nil, ErrConflict
	} else if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	current := s.opts.Current()
	if !tagPattern.MatchString(current.Version) || version.CompareVersions(in.Target, current.Version) <= 0 {
		return nil, ErrInvalid
	}
	baseSHA, e := fileDigest(s.opts.Executable)
	if e != nil {
		return nil, ErrUnavailable
	}
	now := time.Now().UTC()
	t := &Task{BaseSHA256: baseSHA, ID: uuid.NewString(), RequestID: in.RequestID, Target: in.Target, From: current.Version, Platform: s.opts.Platform, Repository: s.opts.Repository, Asset: "pipewright_" + strings.TrimPrefix(in.Target, "v") + "_" + s.opts.Platform + ".tar.gz", State: "accepted", CreatedAt: now, UpdatedAt: now}
	raw, e := encoded(t)
	if e != nil {
		return nil, e
	}
	_, e = s.db.ExecContext(ctx, "INSERT INTO selfupdate_tasks(id,instance_id,request_id,target_tag,status,active_key,record_json,created_at) VALUES(?,?,?,?,?,?,?,?)", t.ID, s.instance, t.RequestID, t.Target, t.State, s.instance, raw, now.UnixMilli())
	if e != nil {
		// The write may have committed despite a lost result. Only authoritative
		// matching rows can establish acceptance or a definitive conflict.
		check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if old, x := s.load(check, "request_id=?", in.RequestID); x == nil {
			if old.Target != in.Target {
				return nil, ErrConflict
			}
			if old.State == "accepted" && !s.running[old.ID] {
				s.launch(*old)
			}
			return old, nil
		} else if !errors.Is(x, ErrNotFound) {
			return nil, ErrUnavailable
		}
		if active, x := s.load(check, "active_key IS NOT NULL"); x == nil && active.RequestID != in.RequestID {
			return nil, ErrConflict
		}
		return nil, ErrUnavailable
	}
	s.launch(*t)
	return t, nil
}
func (s *Service) Resume(ctx context.Context, id, requestID string) (*Task, error) {
	if !requestPattern.MatchString(requestID) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx == nil {
		return nil, ErrUnavailable
	}
	t, e := s.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if t.LastResumeID == requestID {
		if t.State == "accepted" && !s.running[t.ID] {
			s.launch(*t)
		}
		return t, nil
	}
	if t.Repository != s.opts.Repository || t.Platform != s.opts.Platform {
		return nil, ErrConflict
	}
	if !resumable(t.State) || s.running[id] {
		return nil, ErrConflict
	}
	t.LastResumeID = requestID
	t.State = "accepted"
	t.Reason = ""
	if e = s.save(ctx, t); e != nil {
		return nil, e
	}
	s.launch(*t)
	return t, nil
}
func (s *Service) launch(t Task) {
	s.running[t.ID] = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.running, t.ID); s.mu.Unlock() }()
		s.work(t)
	}()
}
func (s *Service) transition(t *Task, state, reason string) error {
	t.State = state
	t.Reason = reason
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.save(c, t)
}
func (s *Service) reconcile(ctx context.Context) error {
	t, e := s.load(ctx, "active_key IS NOT NULL")
	if errors.Is(e, ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	var fence string
	if e = s.db.QueryRowContext(ctx, "SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence); e != nil {
		return e
	}
	if fence == t.ID || t.State == "installing" || t.State == "restarting" || t.State == "unknown" || t.State == "restart_required" {
		got, x := fileDigest(s.opts.Executable)
		info := s.opts.Current()
		if x == nil && t.BinarySHA256 != "" && got == t.BinarySHA256 && strings.TrimPrefix(info.Version, "v") == strings.TrimPrefix(t.Target, "v") && strings.ReplaceAll(info.Platform, "/", "_") == t.Platform {
			return s.finish(ctx, t, "completed", "")
		}
		return s.transition(t, "unknown", "installation_not_confirmed")
	}
	if terminal(t.State) {
		return nil
	}
	if t.State == "ready_blocked" {
		return nil
	}
	return s.transition(t, "interrupted", "explicit_resume_required")
}
func fileDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, MaxBinary+1)); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
