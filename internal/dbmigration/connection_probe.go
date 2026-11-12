package dbmigration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

var ErrProbeTimeout = errors.New("database connection probe timed out")
var ErrProbePath = errors.New("database target path is invalid")

type ConnectionProbeResult struct {
	Kind string `json:"kind"`
}

// ProbeConnection never creates a migration task, target file or database object.
// Its one deadline includes waiting for the runtime, authentication and queries.
func (r *Runtime) ProbeConnection(parent context.Context, target Connection) (result ConnectionProbeResult, err error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	defer func() {
		deadline, bounded := ctx.Deadline()
		if ctx.Err() != nil || (bounded && !time.Now().Before(deadline)) {
			result = ConnectionProbeResult{}
			err = ErrProbeTimeout
		}
	}()
	if !probeLock(ctx, &r.mu) {
		return result, ErrProbeTimeout
	}
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return result, ErrProbeTimeout
	}
	if r.Recovery || r.Source == nil || r.Control == nil || r.key == nil {
		return result, ErrInvalidState
	}
	if !probeLock(ctx, &r.Control.mu) {
		return result, ErrProbeTimeout
	}
	state, e := r.Control.state()
	r.Control.mu.Unlock()
	if e != nil {
		return result, ErrInvalidState
	}
	if state != nil && state.Phase != Prepared && state.Phase != Aborted && state.Phase != Completed {
		return result, ErrInvalidState
	}
	if e = r.probeFence(ctx); e != nil {
		return result, e
	}
	if target.Driver == "sqlite" {
		if strings.TrimSpace(target.DSN) == "" || strings.HasPrefix(target.DSN, "file:") || target.DSN == ":memory:" {
			return result, ErrProbePath
		}
		path, e := filepath.Abs(target.DSN)
		if e != nil {
			return result, ErrProbePath
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(path))
		if e != nil {
			return result, ErrProbePath
		}
		info, e := os.Stat(parent)
		if e != nil || !info.IsDir() {
			return result, ErrProbePath
		}
		if _, e = os.Lstat(filepath.Join(parent, filepath.Base(path))); !os.IsNotExist(e) {
			return result, ErrProbePath
		}
		return ConnectionProbeResult{Kind: "path"}, nil
	}
	switch target.Driver {
	case "mysql", "postgres", "sqlserver", "dm":
	default:
		return result, ErrUnsupported
	}
	targetDB, e := store.ConnectProbeContext(ctx, store.OpenConfig{Driver: target.Driver, DSN: target.DSN})
	if e != nil {
		if errors.Is(e, store.ErrDriverUnavailable) {
			return result, ErrUnsupported
		}
		return result, ErrConnection
	}
	defer targetDB.Close()
	query := "SELECT 1"
	if target.Driver == "dm" {
		query += " FROM DUAL"
	}
	var one int
	if e = targetDB.DB.QueryRowContext(ctx, query).Scan(&one); e != nil || one != 1 {
		return result, ErrConnection
	}
	return ConnectionProbeResult{Kind: "connection"}, nil
}

// Control and runtime waits consume the same deadline as the actual connection.
func probeLock(ctx context.Context, mu *sync.Mutex) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !mu.TryLock() {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
	if ctx.Err() != nil {
		mu.Unlock()
		return false
	}
	return true
}

// A probe must not inherit an ordinary pooled driver's cancellation drain or
// close a shared production connection to enforce its deadline. These engines
// use a short-lived, bounded read-only connection for the source fence too.
func (r *Runtime) probeFence(ctx context.Context) error {
	source := r.Source
	switch r.Current.Connection.Driver {
	case "sqlserver", "dm":
		isolated, err := store.ConnectProbeContext(ctx, store.OpenConfig{Driver: r.Current.Connection.Driver, DSN: r.Current.Connection.DSN})
		if err != nil {
			if errors.Is(err, store.ErrDriverUnavailable) {
				return ErrUnsupported
			}
			return ErrConnection
		}
		defer isolated.Close()
		source = isolated
	}
	return checkFence(ctx, source, "")
}
