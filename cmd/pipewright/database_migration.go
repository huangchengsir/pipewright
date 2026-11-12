package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/dbmigration"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	log "github.com/huangchengsir/pipewright/internal/logging"
	"github.com/huangchengsir/pipewright/internal/version"
)

// Kept open until successful exec or actual process exit, never a runPlatform
// defer. Every startup obtains a new nonce even when syscall.Exec keeps the PID.
var databaseProcessGuard *dbmigration.Control

func databaseBoot(cfg *config.Config, key *[32]byte) (dbmigration.ActiveConfig, *dbmigration.Runtime, error) {
	control, e := dbmigration.AcquireControl(cfg.DataDir, key)
	if e != nil {
		return dbmigration.ActiveConfig{}, nil, e
	}
	databaseProcessGuard = control
	state, e := control.State()
	if e != nil {
		return dbmigration.ActiveConfig{}, nil, e
	}
	active, e := control.Active()
	if e != nil {
		return dbmigration.ActiveConfig{}, nil, e
	}
	paths, e := dbmigration.ResolvePaths(cfg.DataDir, cfg.DBPath, os.Getenv("PIPEWRIGHT_ARTIFACT_DIR"), os.Getenv("PIPEWRIGHT_REPO_CACHE_DIR"), os.Getenv("PIPEWRIGHT_CACHE_DIR"))
	if e != nil {
		return dbmigration.ActiveConfig{}, nil, e
	}
	if active != nil {
		if paths.DataDir != active.Paths.DataDir {
			return dbmigration.ActiveConfig{}, nil, dbmigration.ErrResources
		}
		paths = active.Paths
		if active.Managed && os.Getenv("PIPEWRIGHT_DB_CREDENTIAL_ROTATION") == "1" && !databaseCredentialOverride(cfg, active, state) {
			return dbmigration.ActiveConfig{}, nil, dbmigration.ErrInvalidState
		}
		if active.Managed && !databaseCredentialOverride(cfg, active, state) {
			cfg.DBDriver = active.Connection.Driver
			cfg.DBDSN = active.Connection.DSN
		}
	}
	driver, dsn, e := cfg.StoreConfig()
	if e != nil {
		return dbmigration.ActiveConfig{}, nil, e
	}
	connection := dbmigration.Connection{Driver: driver, DSN: dsn}
	current := dbmigration.ActiveConfig{Connection: connection, Paths: paths}
	if state == nil {
		return current, nil, nil
	}
	expected := state.Source
	if state.Phase == dbmigration.Completed {
		expected = state.Target
	}
	switch state.Phase {
	case dbmigration.Prepared, dbmigration.Aborted, dbmigration.Completed:
		db, e := dbmigration.OpenExisting(connection)
		if e != nil {
			return current, nil, e
		}
		defer db.Close()
		actual, e := dbmigration.Describe(context.Background(), db, dsn)
		if e != nil {
			return current, nil, e
		}
		current = expected
		current.Connection = connection
		current.Endpoint = actual
		current.Paths = paths
		allowed, e := control.CheckBoot(current)
		if e != nil {
			return current, nil, e
		}
		if !allowed {
			return current, nil, dbmigration.ErrIdentity
		}
		return current, nil, nil
	default:
		// Source connections in this branch perform SELECT-only recovery operations;
		// no store.Migrate, auth bootstrap, updater initialization or worker runs.
		source, e := dbmigration.OpenExisting(state.Source.Connection)
		if e != nil {
			return current, nil, e
		}
		actual, e := dbmigration.Describe(context.Background(), source, state.Source.Connection.DSN)
		if e != nil || !reflect.DeepEqual(actual, state.Source.Endpoint) {
			source.Close()
			return current, nil, dbmigration.ErrIdentity
		}
		current = state.Source
		if reflect.DeepEqual(connection, state.Target.Connection) {
			target, e := dbmigration.OpenExisting(connection)
			if e == nil {
				endpoint, err := dbmigration.Describe(context.Background(), target, connection.DSN)
				target.Close()
				if err == nil {
					current = state.Target
					current.Endpoint = endpoint
					current.Paths = paths
				}
			}
		}
		runtime := dbmigration.NewRuntime(control, current, source, key, true, nil)
		if state.Phase == dbmigration.AwaitingRestart && reflect.DeepEqual(current, state.Target) {
			if _, e = runtime.ConfirmBoot(context.Background(), current); e == nil {
				source.Close()
				runtime.Close()
				return current, nil, nil
			}
			log.Printf("[database] activation requires explicit recovery; business startup remains closed")
		}
		return current, runtime, nil
	}
}

// An explicitly requested same-engine deployment profile may rotate credentials
// only before a new migration starts or after the previous one is terminal.
// The subsequent read-only endpoint check must succeed; there is no fallback to
// old credentials or another database when the supplied connection is invalid.
func databaseCredentialOverride(cfg *config.Config, active *dbmigration.ActiveConfig, state *dbmigration.State) bool {
	if os.Getenv("PIPEWRIGHT_DB_CREDENTIAL_ROTATION") != "1" || state == nil || active == nil || cfg.DBDriver == "sqlite" || cfg.DBDSN == "" || cfg.DBDriver != active.Connection.Driver {
		return false
	}
	switch state.Phase {
	case dbmigration.Prepared, dbmigration.Aborted, dbmigration.Completed:
		return true
	default:
		return false
	}
}

func databaseRestart() {
	if e := version.Reexec(); e != nil {
		log.Printf("[database] restart unavailable; restart this instance externally; business startup remains closed")
	}
	// Returning from main exits the old process. Never reopen or write the source
	// after a failed exec, unlike the separate self-update recovery mechanism.
}
func serveDatabaseRecovery(addr string, web fs.FS, runtime *dbmigration.Runtime) (restart func()) {
	requests := make(chan struct{}, 1)
	runtime.Restart = func() error {
		select {
		case requests <- struct{}{}:
			return nil
		default:
			return dbmigration.ErrInvalidState
		}
	}
	defer runtime.Close()
	defer runtime.Source.Close()
	srv := &http.Server{Addr: addr, Handler: httpapi.NewDatabaseRecovery(web, runtime), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case <-requests:
		restart = databaseRestart
	case e := <-errorsCh:
		if !errors.Is(e, http.ErrServerClosed) {
			log.Printf("[database] recovery listener unavailable")
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	return restart
}
