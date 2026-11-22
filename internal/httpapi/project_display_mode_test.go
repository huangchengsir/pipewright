package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/store"
)

func TestProjectDisplayModeRequiresLoginAndCSRFAndPersists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "preferences.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(testWebFSAuth(), svc, WithProjectDisplayMode(st.DB)))
	defer server.Close()
	url := server.URL + "/api/account/project-mode"
	client := newTestClient(t)
	assertMode := func(method, csrf, body string, status int, mode string) {
		t.Helper()
		resp := doJSON(t, client, method, url, csrf, body)
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("%s: got %d, want %d", method, resp.StatusCode, status)
		}
		if mode != "" {
			var out projectDisplayMode
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Mode != mode {
				t.Fatalf("%s: got %q (%v), want %q", method, out.Mode, err, mode)
			}
		}
	}
	assertMode(http.MethodGet, "", "", 401, "")
	csrf := loginWithClient(t, client, server.URL)
	assertMode(http.MethodGet, "", "", 200, "single")
	assertMode(http.MethodPut, "", `{"mode":"multi"}`, 403, "")
	assertMode(http.MethodPut, csrf, `{"mode":"multi","accountId":"someone-else"}`, 400, "")
	assertMode(http.MethodPut, csrf, `{"mode":"invalid"}`, 400, "")
	assertMode(http.MethodPut, csrf, `{"mode":"multi"}`, 200, "multi")
	assertMode(http.MethodPut, csrf, `{"mode":"multi"}`, 200, "multi")
	server.Close()
	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	svc = auth.NewService(st.DB, nil)
	server = httptest.NewServer(New(testWebFSAuth(), svc, WithProjectDisplayMode(st.DB)))
	defer server.Close()
	url = server.URL + "/api/account/project-mode"
	client = newTestClient(t)
	csrf = loginWithClient(t, client, server.URL)
	assertMode(http.MethodGet, "", "", 200, "multi")
	assertMode(http.MethodPut, csrf, `{"mode":"single"}`, 200, "single")
	assertMode(http.MethodGet, "", "", 200, "single")
}
