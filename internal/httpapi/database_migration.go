package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/dbmigration"
	"github.com/huangchengsir/pipewright/internal/maintenance"
)

const recoveryCookie = "pipewright_database_recovery"

func WithDatabaseMigration(r *dbmigration.Runtime) Option {
	return func(o *options) { o.databaseMigration = r }
}

type databaseMigrationView struct {
	TargetPermissions   string                    `json:"targetPermissions,omitempty"`
	Info                *dbmigration.DatabaseInfo `json:"info,omitempty"`
	CompletedTables     int                       `json:"completedTables"`
	TotalTables         int                       `json:"totalTables"`
	Checks              []string                  `json:"checks,omitempty"`
	Blockers            []maintenance.Blocker     `json:"blockers"`
	BlockersUnavailable bool                      `json:"blockersUnavailable"`
	TaskID              string                    `json:"taskId,omitempty"`
	Revision            uint64                    `json:"revision"`
	Phase               dbmigration.Phase         `json:"phase,omitempty"`
	Current             string                    `json:"current"`
	Target              string                    `json:"target,omitempty"`
	Managed             bool                      `json:"managed"`
	Recovery            bool                      `json:"recovery"`
	Report              *dbmigration.CopyReport   `json:"report,omitempty"`
}

func migrationView(runtime *dbmigration.Runtime, s *dbmigration.State) databaseMigrationView {
	v := databaseMigrationView{Current: runtime.Current.Connection.Driver, Managed: runtime.Current.Managed, Recovery: runtime.Recovery}
	if s != nil {
		v.TargetPermissions = s.TargetPermissions
		v.TaskID = s.TaskID
		v.Revision = s.Revision
		v.Phase = s.Phase
		if s.Phase != dbmigration.Completed {
			v.Current = s.Source.Connection.Driver
		}
		v.CompletedTables = len(s.Progress)
		v.TotalTables = len(dbmigration.Manifest())
		if s.Phase == dbmigration.Prepared {
			v.Checks = []string{"connection", "emptyTarget", "identity", "secrets", "artifacts"}
		}
		v.Target = s.Target.Connection.Driver
		v.Managed = s.Target.Managed
		v.Report = s.Report
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var e error
	if info, err := runtime.Info(ctx); err == nil {
		v.Info = &info
	}
	v.Blockers, e = runtime.Blockers(ctx)
	v.BlockersUnavailable = e != nil
	return v
}
func migrationError(w http.ResponseWriter, e error) {
	code := "database_migration_failed"
	switch {
	case errors.Is(e, dbmigration.ErrRecoveryAuth):
		writeError(w, 401, "database_recovery_auth", "Recovery authentication required")
		return
	case errors.Is(e, maintenance.ErrBlocked):
		code = "database_work_active"
	case errors.Is(e, dbmigration.ErrTargetNotEmpty):
		code = "database_target_not_empty"
	case errors.Is(e, dbmigration.ErrIdentity):
		code = "database_identity_mismatch"
	case errors.Is(e, dbmigration.ErrResources):
		code = "database_resources_invalid"
	case errors.Is(e, dbmigration.ErrCommitUnknown), errors.Is(e, dbmigration.ErrPersistenceUnknown):
		code = "database_outcome_unknown"
	case errors.Is(e, dbmigration.ErrMismatch):
		code = "database_verification_failed"
	case errors.Is(e, dbmigration.ErrUnsupported):
		code = "database_capability_unavailable"
	case errors.Is(e, dbmigration.ErrConnection):
		code = "database_connection_failed"
	case errors.Is(e, dbmigration.ErrPermissions):
		code = "database_permissions_insufficient"
	case errors.Is(e, dbmigration.ErrInvalidConnection):
		code = "database_connection_invalid"
	case errors.Is(e, dbmigration.ErrInvalidState):
		code = "database_state_changed"
	}
	// Driver errors may contain a DSN, query parameters or secrets. Never echo them.
	writeError(w, 409, code, "Database migration requires attention; inspect the task and continue explicitly")
}
func decodeMigration(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, "invalid_request", "Invalid request")
		return false
	}
	return true
}
func mountDatabaseMigration(ar chi.Router, runtime *dbmigration.Runtime) {
	if runtime == nil {
		return
	}
	ar.Get("/settings/database", func(w http.ResponseWriter, r *http.Request) {
		s, e := runtime.Control.State()
		if e != nil {
			migrationError(w, e)
			return
		}
		writeJSON(w, 200, migrationView(runtime, s))
	})
	ar.Post("/settings/database/preflight", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			dbmigration.ConnectionFields
			Managed bool `json:"managed"`
		}
		if !decodeMigration(w, r, &in) {
			return
		}
		connection, e := dbmigration.BuildConnection(in.ConnectionFields)
		if e != nil {
			migrationError(w, e)
			return
		}
		s, e := runtime.Preflight(r.Context(), connection, in.Managed)
		in.Password = ""
		in.DSN = ""

		if e != nil {
			migrationError(w, e)
			return
		}
		writeJSON(w, 200, migrationView(runtime, s))
	})
	ar.Post("/settings/database/start", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TaskID   string `json:"taskId"`
			Revision uint64 `json:"revision"`
		}
		if !decodeMigration(w, r, &in) {
			return
		}
		token, csrf, e := runtime.Control.IssueRecoverySession(in.TaskID, time.Now())
		if e != nil {
			migrationError(w, e)
			return
		}
		s, e := runtime.Start(r.Context(), in.TaskID, in.Revision)
		if e != nil {
			migrationError(w, e)
			return
		}
		setRecoveryCookie(w, r, token)
		writeJSON(w, 202, map[string]any{"state": migrationView(runtime, s), "csrf": csrf})
	})
	ar.Post("/settings/database/abort", migrationAction(runtime, runtime.Abort))
}

type migrationActionFunc func(context.Context, string, uint64) (*dbmigration.State, error)

func migrationAction(runtime *dbmigration.Runtime, action migrationActionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TaskID   string `json:"taskId"`
			Revision uint64 `json:"revision"`
		}
		if !decodeMigration(w, r, &in) {
			return
		}
		s, e := action(r.Context(), in.TaskID, in.Revision)
		if e != nil {
			migrationError(w, e)
			return
		}
		writeJSON(w, 200, migrationView(runtime, s))
	}
}
func setRecoveryCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: recoveryCookie, Value: token, Path: "/api/database-recovery", HttpOnly: true, Secure: cookieSecure(r, nil), SameSite: http.SameSiteStrictMode, MaxAge: int(dbmigration.RecoverySessionLifetime.Seconds())})
}
func recoveryOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") != "" && r.Header.Get("Origin") == requestOrigin(r)
}
func recoveryProtection(runtime *dbmigration.Runtime, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cookie, e := r.Cookie(recoveryCookie)
		if e != nil {
			migrationError(w, dbmigration.ErrRecoveryAuth)
			return
		}
		csrf, e := runtime.Control.RecoverySession(cookie.Value, time.Now())
		if e != nil {
			migrationError(w, e)
			return
		}
		if r.Method != "GET" && (!recoveryOrigin(r) || subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get(headerCsrf))) != 1) {
			writeError(w, 403, "csrf_invalid", "Invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func mountDatabaseRecovery(router chi.Router, runtime *dbmigration.Runtime) {
	router.Get("/api/database-recovery/mode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]bool{"recovery": runtime != nil && runtime.Recovery})
	})
	if runtime == nil {
		return
	}
	readonlyAuth := auth.NewService(runtime.Source.DB, nil)
	router.Post("/api/database-recovery/login", func(w http.ResponseWriter, r *http.Request) {
		if !recoveryOrigin(r) {
			writeError(w, 403, "origin_invalid", "Same-origin request required")
			return
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeMigration(w, r, &in) {
			return
		}
		e := readonlyAuth.VerifyRecoveryPassword(r.Context(), in.Username, in.Password)
		in.Password = ""
		if e != nil {
			var locked *auth.ErrLockedOut
			if errors.As(e, &locked) {
				writeError(w, 429, "locked_out", "Try again later")
			} else {
				migrationError(w, dbmigration.ErrRecoveryAuth)
			}
			return
		}
		s, e := runtime.Control.State()
		if e != nil || s == nil {
			migrationError(w, dbmigration.ErrRecoveryAuth)
			return
		}
		token, csrf, e := runtime.Control.IssueRecoverySession(s.TaskID, time.Now())
		if e != nil {
			migrationError(w, e)
			return
		}
		setRecoveryCookie(w, r, token)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]string{"csrf": csrf})
	})
	router.Group(func(ar chi.Router) {
		ar.Use(func(next http.Handler) http.Handler { return recoveryProtection(runtime, next) })
		ar.Get("/api/database-recovery/session", func(w http.ResponseWriter, r *http.Request) {
			cookie, _ := r.Cookie(recoveryCookie)
			csrf, e := runtime.Control.RecoverySession(cookie.Value, time.Now())
			if e != nil {
				migrationError(w, e)
				return
			}
			s, e := runtime.Control.State()
			if e != nil {
				migrationError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"csrf": csrf, "state": migrationView(runtime, s)})
		})
		ar.Post("/api/database-recovery/continue", migrationAction(runtime, runtime.ContinueCopy))
		ar.Post("/api/database-recovery/activate", migrationAction(runtime, runtime.Stage))
		ar.Post("/api/database-recovery/reconcile", migrationAction(runtime, runtime.Reconcile))
		ar.Post("/api/database-recovery/abort", migrationAction(runtime, runtime.Abort))
	})
}

// NewDatabaseRecovery mounts only local recovery/authentication and embedded SPA
// assets. It never instantiates normal auth session handlers or business services.
func NewDatabaseRecovery(web fs.FS, runtime *dbmigration.Runtime) http.Handler {
	router := chi.NewRouter()
	router.Use(localeMiddleware)
	mountDatabaseRecovery(router, runtime)
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 503, "database_maintenance", "Database maintenance")
	})
	router.Handle("/api/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 503, "database_maintenance", "Database maintenance")
	}))
	router.Handle("/*", spaHandler(web))
	return router
}
