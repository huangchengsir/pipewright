package httpapi

import (
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This checks actual registered route authentication, not domain authorization.
// Domain/actor/action evidence remains in the separate tenancy integration tests.
func TestRegisteredAPIRoutesRequireAuthentication(t *testing.T) {
	f := platformSetup(t)
	h := New(testWebFSAuth(), nil, WithPlatformLogin(f.s), WithConfigManagement(f.store.DB, nil, false), WithTenancyAccounts(tenancy.NewAccounts(f.store.DB)), WithTenancyRepository(tenancy.NewRepository(f.store.DB)))
	routes, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("router does not expose registered routes")
	}
	public := map[string]bool{
		"GET /api/database-recovery/mode": true,
		"POST /api/auth/login":            true, "GET /api/auth/session": true,
		"GET /api/auth/platform/providers": true, "POST /api/auth/platform/{provider}/start": true,
		"GET /api/auth/platform/{provider}/callback": true, "POST /api/webhooks/{token}": true,
	}
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	var inventory []string
	checked := 0
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if !strings.HasPrefix(route, "/api/") {
			return nil
		}
		if public[key] {
			inventory = append(inventory, "PUBLIC "+key)
			return nil
		}
		path := placeholder.ReplaceAllString(route, "inventory-id")
		path = strings.ReplaceAll(path, "*", "inventory-path")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		checked++
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous status=%d want=401", key, w.Code)
		}
		inventory = append(inventory, "AUTH "+key)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 200 {
		t.Fatalf("unexpectedly incomplete route fixture: %d", checked)
	}
	sort.Strings(inventory)
	for _, route := range inventory {
		t.Log(route)
	}
	t.Logf("checked protected registered routes=%d; optional services absent from fixture require separate inventory", checked)
}
