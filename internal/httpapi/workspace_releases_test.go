package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductReleaseUnavailableFailsClosed(t *testing.T) {
	h := NewProductReleaseHandlers(nil)
	for name, handler := range map[string]http.HandlerFunc{"confirm": h.Confirm, "get": h.Get, "advance": h.Advance, "reconcile": h.Reconcile} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
			if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status %d headers %v", w.Code, w.Header())
			}
		})
	}
}
func TestProductReleaseStrictRequest(t *testing.T) {
	for _, body := range []string{`{"unexpected":true}`, `{} {}`, `{"requestId":"` + strings.Repeat("a", 256<<10) + `"}`, `{`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var in struct {
			RequestID string `json:"requestId"`
		}
		if decodeProductRelease(w, r, &in) || w.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid request; status %d", w.Code)
		}
	}
}
