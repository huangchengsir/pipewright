package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/productrelease"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// ProductReleaseHandlers must be mounted inside the existing authenticated,
// CSRF-protected API group. Service authorization remains mandatory as well.
// Writes are explicit: GET never starts or reconciles a release.
type ProductReleaseHandlers struct{ Options, Preview, List, Confirm, Get, Advance, Reconcile http.HandlerFunc }

func WithProductReleases(s *productrelease.Service) Option {
	return func(o *options) {
		o.productReleaseRoutes = func(r chi.Router, aud audit.Recorder) {
			h := NewProductReleaseHandlers(s, aud)
			r.Get("/workspace/products/{productId}/release-options", h.Options)
			r.Post("/workspace/products/{productId}/release-preview", h.Preview)
			r.Get("/workspace/products/{productId}/releases", h.List)
			r.Post("/workspace/products/{productId}/releases", h.Confirm)
			r.Get("/workspace/releases/{releaseId}", h.Get)
			r.Post("/workspace/releases/{releaseId}/advance", h.Advance)
			r.Post("/workspace/releases/{releaseId}/reconcile", h.Reconcile)
		}
	}
}
func NewProductReleaseHandlers(s *productrelease.Service, audits ...audit.Recorder) ProductReleaseHandlers {
	var aud audit.Recorder
	if len(audits) > 0 {
		aud = audits[0]
	}
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !s.Available() {
				h(w, r)
				return
			}
			session, ok := sessionFromContext(r.Context())
			if !ok || session == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in first")
				return
			}
			if !session.MultiTenant {
				r = r.WithContext(productrelease.WithLegacySession(r.Context()))
			}
			h(w, r)
		}
	}
	available := func(w http.ResponseWriter) bool {
		w.Header().Set("Cache-Control", "no-store")
		if !s.Available() {
			writeError(w, http.StatusServiceUnavailable, "product_release_unavailable", "Frozen product release execution is unavailable")
			return false
		}
		return true
	}
	options := func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		page, err := productReleasePage(r)
		if err != nil {
			writeProductReleaseError(w, err)
			return
		}
		value, err := s.Options(r.Context(), chi.URLParam(r, "productId"), page)
		if err != nil {
			writeProductReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
	preview := func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		var in struct {
			Items []productrelease.PreviewItem `json:"items"`
		}
		if !decodeProductRelease(w, r, &in) {
			return
		}
		value, err := s.Preview(r.Context(), chi.URLParam(r, "productId"), in.Items)
		if err != nil {
			writeProductReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	}
	list := func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		page, err := productReleasePage(r)
		if err != nil {
			writeProductReleaseError(w, err)
			return
		}
		result, err := s.List(r.Context(), chi.URLParam(r, "productId"), page)
		if err != nil {
			writeProductReleaseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
	get := func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		b, e := s.Get(r.Context(), chi.URLParam(r, "releaseId"))
		if e != nil {
			writeProductReleaseError(w, e)
			return
		}
		writeJSON(w, http.StatusOK, b)
	}
	confirm := func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		var in struct {
			RequestID string              `json:"requestId"`
			Plan      productrelease.Plan `json:"plan"`
		}
		if !decodeProductRelease(w, r, &in) {
			return
		}
		if in.Plan.ProductID != chi.URLParam(r, "productId") {
			writeProductReleaseError(w, productrelease.ErrInvalid)
			return
		}
		b, e := s.Confirm(r.Context(), in.RequestID, in.Plan)
		if e != nil {
			writeProductReleaseError(w, e)
			return
		}
		workspaceAudit(r, aud, "workspace_release_confirm", "product", b.ProductID, map[string]any{"releaseId": b.ID, "itemCount": len(b.Items)})
		writeJSON(w, http.StatusOK, b)
	}
	action := func(reconcile bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !available(w) {
				return
			}
			var in struct {
				ExpectedRevision int64 `json:"expectedRevision"`
			}
			if !decodeProductRelease(w, r, &in) {
				return
			}
			if in.ExpectedRevision < 1 {
				writeProductReleaseError(w, productrelease.ErrInvalid)
				return
			}
			var b *productrelease.Batch
			var e error
			if reconcile {
				b, e = s.Reconcile(r.Context(), chi.URLParam(r, "releaseId"), in.ExpectedRevision)
			} else {
				b, e = s.Advance(r.Context(), chi.URLParam(r, "releaseId"), in.ExpectedRevision)
			}
			if e != nil {
				writeProductReleaseError(w, e)
				return
			}
			event := "workspace_release_advance"
			if reconcile {
				event = "workspace_release_reconcile"
			}
			workspaceAudit(r, aud, event, "product", b.ProductID, map[string]any{"releaseId": b.ID, "revision": b.Revision})
			writeJSON(w, http.StatusOK, b)
		}
	}
	return ProductReleaseHandlers{Options: wrap(options), Preview: wrap(preview), List: wrap(list), Confirm: wrap(confirm), Get: wrap(get), Advance: wrap(action(false)), Reconcile: wrap(action(true))}
}
func productReleasePage(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("page")
	if raw == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 || page > 1_000_000 {
		return 0, productrelease.ErrInvalid
	}
	return page, nil
}
func decodeProductRelease(w http.ResponseWriter, r *http.Request, out any) bool {
	const limit = 256 << 10
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || strictObjectJSON(raw, out, limit) != nil {
		writeProductReleaseError(w, productrelease.ErrInvalid)
		return false
	}
	return true
}
func writeProductReleaseError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, productrelease.ErrUnavailable):
		writeError(w, 503, "product_release_unavailable", "Frozen product release execution is unavailable")
	case errors.Is(e, productrelease.ErrUnsupported):
		writeError(w, 422, "product_release_unsupported", "This pipeline cannot produce a verified frozen deployment")
	case errors.Is(e, productrelease.ErrInvalid):
		writeError(w, 400, "product_release_invalid", "Invalid product release request")
	case errors.Is(e, productrelease.ErrNotFound):
		writeError(w, 404, "product_release_not_found", "Product release not found")
	case errors.Is(e, productrelease.ErrConflict):
		writeError(w, 409, "product_release_conflict", "Product release changed; reload before continuing")
	case errors.Is(e, productrelease.ErrBlocked):
		writeError(w, 409, "product_release_blocked", "Reconcile the original run before continuing")
	case errors.Is(e, tenancy.ErrForbidden):
		writeError(w, 403, "forbidden", "Access denied")
	default:
		writeError(w, 500, "product_release_failed", "Product release operation could not be completed")
	}
}
