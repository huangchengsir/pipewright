package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configdraft"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
)

func WithConfigManagement(db *sql.DB, drafts *configdraft.Service, requireMatch bool) Option {
	return func(o *options) { o.configDB = db; o.configDrafts = drafts; o.configRequireMatch = requireMatch }
}

// ConfigAdminAuthorizer rechecks the browser session in the same transaction
// as approval. Only its public hash is stored as approver identity.
func ConfigAdminAuthorizer(db *sql.DB) func(context.Context, configstate.DBTX, string) error {
	return func(ctx context.Context, q configstate.DBTX, id string) error {
		sess, ok := sessionFromContext(ctx)
		if !ok || sess == nil || id != auth.SessionID(sess.Token) {
			return configdraft.ErrForbidden
		}
		query := `SELECT expires_at FROM sessions WHERE token=?`
		if store.DialectOf(db) == store.MySQL {
			query += " FOR UPDATE"
		}
		var expiry string
		if q.QueryRowContext(ctx, query, sess.Token).Scan(&expiry) != nil {
			return configdraft.ErrForbidden
		}
		exp, err := time.Parse(time.RFC3339Nano, expiry)
		if err != nil || !time.Now().Before(exp) {
			return configdraft.ErrForbidden
		}
		return nil
	}
}

// ConfigApproverVerifier checks that the session which issued an approval has
// not been revoked. The MCP caller never receives or supplies its session token.
func ConfigApproverVerifier(db *sql.DB) func(context.Context, configstate.DBTX, string) error {
	return func(ctx context.Context, q configstate.DBTX, id string) error {
		if len(id) != 16 {
			return configdraft.ErrForbidden
		}
		query := `SELECT token,expires_at FROM sessions ORDER BY token LIMIT 1025`
		if store.DialectOf(db) == store.MySQL {
			query += " FOR UPDATE"
		}
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		matched, count := false, 0
		for rows.Next() {
			count++
			var token, expiry string
			if err := rows.Scan(&token, &expiry); err != nil {
				return err
			}
			exp, err := time.Parse(time.RFC3339Nano, expiry)
			if err == nil && auth.SessionID(token) == id && time.Now().Before(exp) {
				matched = true
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !matched || count > 1024 {
			return configdraft.ErrForbidden
		}
		return nil
	}
}

func mountConfigManagement(r chi.Router, db *sql.DB, svc *configdraft.Service) {
	r.Get("/mcp/config-operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeConfigError(w, configdraft.ErrUnavailable)
			return
		}
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeConfigError(w, configdraft.ErrForbidden)
			return
		}
		out, err := svc.AdminOperation(r.Context(), auth.SessionID(sess.Token), chi.URLParam(r, "id"))
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
	r.Post("/mcp/config-operations/{id}/fence", func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeConfigError(w, configdraft.ErrUnavailable)
			return
		}
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeConfigError(w, configdraft.ErrForbidden)
			return
		}
		var body struct{}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if dec.Decode(&body) != nil || dec.Decode(new(any)) != io.EOF {
			writeConfigError(w, configdraft.ErrInvalid)
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.FencePending(r.Context(), auth.SessionID(sess.Token), id); err != nil {
			writeConfigError(w, err)
			return
		}
		out, err := svc.AdminOperation(r.Context(), auth.SessionID(sess.Token), id)
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
	maintenance := configstate.NewMaintenance(db, &configstate.MaintenanceOptions{AuthorizeAdmin: ConfigAdminAuthorizer(db)})
	r.Get("/mcp/config-maintenance", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeConfigError(w, configstate.ErrMaintenanceForbidden)
			return
		}
		out, err := maintenance.List(r.Context(), auth.SessionID(sess.Token))
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
	r.Post("/mcp/config-maintenance/recover", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeConfigError(w, configstate.ErrMaintenanceForbidden)
			return
		}
		var in configstate.MaintenanceRecoverRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
			writeConfigError(w, configdraft.ErrInvalid)
			return
		}
		out, err := maintenance.Recover(r.Context(), auth.SessionID(sess.Token), in)
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
	r.Get("/projects/{id}/config-revision", func(w http.ResponseWriter, r *http.Request) {
		v, err := configstate.ReadRevision(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]string{"revision": configRevision(v)})
	})
	r.Get("/mcp/config-drafts/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeConfigError(w, configdraft.ErrUnavailable)
			return
		}
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeConfigError(w, configdraft.ErrForbidden)
			return
		}
		out, err := svc.Review(r.Context(), auth.SessionID(sess.Token), chi.URLParam(r, "id"))
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
	r.Post("/mcp/config-drafts/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeConfigError(w, configdraft.ErrUnavailable)
			return
		}
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeConfigError(w, configdraft.ErrForbidden)
			return
		}
		var in configdraft.ApprovalRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
			writeConfigError(w, configdraft.ErrInvalid)
			return
		}
		out, err := svc.Approve(r.Context(), auth.SessionID(sess.Token), chi.URLParam(r, "id"), in)
		if err != nil {
			writeConfigError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
	})
}

func writeConfigError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "service_unavailable"
	switch {
	case errors.Is(err, configstate.ErrMaintenanceForbidden):
		status, code = 403, "permission_denied"
	case errors.Is(err, configstate.ErrEvidenceUnavailable):
		status, code = 409, "evidence_unavailable"
	case errors.Is(err, configstate.ErrPrecondition):
		status, code = 428, "config_revision_required"
	case errors.Is(err, configstate.ErrConflict):
		status, code = 409, "revision_conflict"
	case errors.Is(err, configstate.ErrBusy):
		status, code = 409, "active_runs"
	case errors.Is(err, configstate.ErrNotFound):
		status, code = 404, "not_found"
	default:
		var e configdraft.Error
		if errors.As(err, &e) {
			code = configdraft.Code(err)
			switch e {
			case configdraft.ErrForbidden:
				status = 403
			case configdraft.ErrInvalid:
				status = 400
			case configdraft.ErrUnavailable, configdraft.ErrSecret:
				status = 503
			default:
				status = 409
			}
		}
	}
	writeError(w, status, code, "Configuration request could not be completed")
}

func configRevision(v int64) string { return "cfg-" + strconv.FormatInt(v, 10) }
func parseConfigRevision(s string) (*int64, error) {
	if s == "" {
		return nil, nil
	}
	s = strings.TrimSpace(s)
	if len(s) > 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if !strings.HasPrefix(s, "cfg-") {
		return nil, configdraft.ErrInvalid
	}
	v, err := strconv.ParseInt(s[4:], 10, 64)
	if err != nil || v < 1 {
		return nil, configdraft.ErrInvalid
	}
	return &v, nil
}

// Limit the precondition adapter to actual configuration routes. Run actions,
// streaming endpoints and ordinary project reads retain their own behavior.
func configurationPath(path string) (string, bool) {
	p := strings.Split(strings.TrimPrefix(path, "/api/projects/"), "/")
	if !strings.HasPrefix(path, "/api/projects/") || len(p) == 0 || p[0] == "" {
		return "", false
	}
	suffix := strings.Join(p[1:], "/")
	switch suffix {
	case "", "trigger", "trigger/secret/reset", "cron", "concurrency", "parameters", "chain", "environments", "pipeline", "pipeline/import", "pipeline/settings", "pipeline/ai-apply", "pipeline/apply-template", "runner", "preview-config":
		return p[0], true
	}
	return "", false
}

type configResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newConfigResponse() *configResponse      { return &configResponse{header: make(http.Header)} }
func (w *configResponse) Header() http.Header { return w.header }
func (w *configResponse) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
}
func (w *configResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(b) > 2<<20 {
		return 0, io.ErrShortBuffer
	}
	return w.body.Write(b)
}
func (w *configResponse) flush(to http.ResponseWriter) {
	for k, v := range w.header {
		to.Header()[k] = v
	}
	if w.status == 0 {
		w.status = 200
	}
	to.WriteHeader(w.status)
	_, _ = to.Write(w.body.Bytes())
}

func configRevisionMiddleware(db *sql.DB, require bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := configurationPath(r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			if r.Method == http.MethodGet {
				// A lazy default may be stored by a legacy GET. Retry that safe read
				// once; never attach a newer baseline to an older response body.
				for attempt := 0; attempt < 2; attempt++ {
					before, e := configstate.ReadRevision(r.Context(), db, id)
					if e != nil {
						writeConfigError(w, e)
						return
					}
					buf := newConfigResponse()
					next.ServeHTTP(buf, r)
					after, e := configstate.ReadRevision(r.Context(), db, id)
					if e != nil {
						writeConfigError(w, e)
						return
					}
					if before != after {
						continue
					}
					buf.Header().Set("X-Config-Revision", configRevision(after))
					buf.flush(w)
					return
				}
				writeConfigError(w, configdraft.ErrStale)
				return
			}
			expected, err := parseConfigRevision(r.Header.Get("If-Match"))
			if err != nil {
				writeConfigError(w, err)
				return
			}
			result := &configstate.MutationResult{}
			buf := newConfigResponse()
			next.ServeHTTP(buf, r.WithContext(configstate.WithMutation(r.Context(), expected, require, result)))
			if result.Successful > 0 {
				w.Header().Set("X-Config-Revision", configRevision(result.Revision))
			}
			if result.Err != nil {
				if result.Successful > 0 {
					w.Header().Set("X-Config-Partial-Save", "true")
				}
				writeConfigError(w, result.Err)
				return
			}
			if result.Successful > 0 && buf.status >= 400 {
				w.Header().Set("X-Config-Partial-Save", "true")
			}
			buf.flush(w)
		})
	}
}
