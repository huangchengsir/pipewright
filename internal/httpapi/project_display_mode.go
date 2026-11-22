package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
)

const legacyProjectModeAccount = "instance-owner"

type projectDisplayMode struct {
	Mode string `json:"mode"`
}

// WithProjectDisplayMode wires the authenticated account preference endpoints.
func WithProjectDisplayMode(db *sql.DB) Option {
	return func(o *options) { o.projectDisplayModeDB = db }
}

func mountProjectDisplayMode(router chi.Router, db *sql.DB, aud audit.Recorder) {
	if db == nil {
		return
	}
	handler := projectDisplayModeHandler(db, aud)
	router.Get("/account/project-mode", handler)
	router.Put("/account/project-mode", handler)
}

func projectDisplayModeHandler(db *sql.DB, aud audit.Recorder) http.HandlerFunc {
	upsert := store.InsertSQL(store.DialectOf(db), "project_display_modes",
		[]string{"account_key", "display_mode"}, []string{"?", "?"},
		[]string{"account_key"}, []string{"display_mode"})
	return func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		session, ok := sessionFromContext(r.Context())
		if !ok || session.MultiTenant && session.UserID == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		account := legacyProjectModeAccount
		if session.MultiTenant {
			account = "user:" + session.UserID
		}
		out := projectDisplayMode{Mode: "single"}
		if r.Method == http.MethodGet {
			err := db.QueryRowContext(r.Context(), "SELECT display_mode FROM project_display_modes WHERE account_key=?", account).Scan(&out.Mode)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				accessError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		const maxBytes = 128
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
		var in projectDisplayMode
		if err != nil || strictObjectJSON(raw, &in, maxBytes) != nil || in.Mode != "single" && in.Mode != "multi" {
			writeError(w, http.StatusBadRequest, "invalid_project_display_mode", "请选择单项目或多项目")
			return
		}
		ctx := r.Context()
		err = configstate.WithWriteTx(ctx, db, func(q configstate.DBTX) error {
			if err := configstate.CheckMaintenanceAdmission(ctx, q); err != nil {
				return err
			}
			_, err := q.ExecContext(ctx, upsert, account, in.Mode)
			return err
		})
		if err != nil {
			accessError(w, err)
			return
		}
		if aud != nil {
			_ = aud.Record(context.WithoutCancel(ctx), audit.Entry{
				Actor: account, Action: "account.project_mode.save", TargetType: "account", TargetID: account,
				Detail: map[string]any{"mode": in.Mode},
			})
		}
		writeJSON(w, http.StatusOK, in)
	}
}
