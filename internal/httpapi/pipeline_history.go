package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func withPipelineHistoryAuthor(source string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if session, ok := sessionFromContext(ctx); ok && session != nil {
			if session.MultiTenant {
				actor := session.Username
				if actor == "" {
					actor = session.UserID
				}
				ctx = pipeline.WithHistoryAuthor(ctx, actor, source)
			} else {
				ctx = pipeline.WithLegacyHistoryAuthor(ctx, source)
			}
		}
		handler(w, r.WithContext(ctx))
	}
}

// ConfigHistoryApprover resolves the human identity from the approval subject
// already validated by MCP apply. Session hashes never enter history responses.
func ConfigHistoryApprover(db *sql.DB) func(context.Context, configstate.DBTX, string) (string, error) {
	return func(ctx context.Context, q configstate.DBTX, subject string) (string, error) {
		on, err := tenancy.Enabled(configstate.WithTxContext(ctx, db, q), db)
		if err != nil {
			return "", err
		}
		var actor string
		if !on {
			err = q.QueryRowContext(ctx, "SELECT username FROM admin_user WHERE id=1").Scan(&actor)
			return actor, err
		}
		identity, err := tenancy.ValidateStoredSession(ctx, q, subject)
		if err != nil {
			return "", err
		}
		err = q.QueryRowContext(ctx, "SELECT username FROM access_users WHERE id=?", identity.UserID).Scan(&actor)
		return actor, err
	}
}

func pipelineHistoryReader(w http.ResponseWriter, svc pipeline.Service) pipeline.HistoryReader {
	w.Header().Set("Cache-Control", "no-store")
	reader, ok := svc.(pipeline.HistoryReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "pipeline_history_unavailable", "Pipeline history is unavailable")
		return nil
	}
	return reader
}

func validPipelineVersion(w http.ResponseWriter, version string) bool {
	id, err := uuid.Parse(version)
	if err != nil || id.String() != version {
		writeError(w, http.StatusNotFound, "pipeline_history_not_found", "Pipeline history version not found")
		return false
	}
	return true
}

func writePipelineHistoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrHistoryNotFound):
		writeError(w, http.StatusNotFound, "pipeline_history_not_found", "Pipeline history version not found")
	case errors.Is(err, pipeline.ErrHistoryDraftUnavailable):
		writeError(w, http.StatusConflict, "pipeline_history_draft_unavailable", "This version cannot be opened as a draft")
	default:
		writePipelineError(w, err)
	}
}

func makePipelineHistoryListHandler(svc pipeline.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reader := pipelineHistoryReader(w, svc)
		if reader == nil {
			return
		}
		before := int64(0)
		limit := 20
		if v := r.URL.Query().Get("before"); v != "" {
			var err error
			before, err = strconv.ParseInt(v, 10, 64)
			if err != nil || before < 1 {
				writeError(w, http.StatusBadRequest, "bad_request", "Invalid history cursor")
				return
			}
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			var err error
			limit, err = strconv.Atoi(v)
			if err != nil || limit < 1 || limit > 100 {
				writeError(w, http.StatusBadRequest, "bad_request", "Invalid history page size")
				return
			}
		}
		page, err := reader.ListHistory(r.Context(), chi.URLParam(r, "id"), before, limit)
		if err != nil {
			writePipelineHistoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

func makePipelineHistoryDetailHandler(svc pipeline.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reader := pipelineHistoryReader(w, svc)
		version := chi.URLParam(r, "versionId")
		if reader == nil || !validPipelineVersion(w, version) {
			return
		}
		detail, err := reader.GetHistory(r.Context(), chi.URLParam(r, "id"), version)
		if err != nil {
			writePipelineHistoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func makePipelineHistoryDraftHandler(svc pipeline.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reader := pipelineHistoryReader(w, svc)
		version := chi.URLParam(r, "versionId")
		if reader == nil || !validPipelineVersion(w, version) {
			return
		}
		draft, revision, err := reader.HistoryDraft(r.Context(), chi.URLParam(r, "id"), version)
		if err != nil {
			writePipelineHistoryError(w, err)
			return
		}
		w.Header().Set("X-Config-Revision", configRevision(revision))
		writeJSON(w, http.StatusOK, toPipelineDTO(draft))
	}
}
