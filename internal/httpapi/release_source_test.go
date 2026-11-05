package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseSourceSettingsAuthenticationStrictInputAndAudit(t *testing.T) {
	st := testStoreAuth(t)
	cat := releasesource.NewCatalog(releasesource.Config{GitHubRepository: "test/upstream", GiteeRepository: "test/mirror"})
	settings := releasesource.NewSettings(st.DB, cat)
	authn := auth.NewService(st.DB, nil)
	if e := authn.Bootstrap("admin", "testpass"); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(New(testWebFS(), authn, WithReleaseSources(cat, settings), WithAudit(audit.New(st.DB, nil, nil))))
	defer srv.Close()
	path := srv.URL + "/api/version/source-settings"
	response, e := http.Get(path)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	response = doJSON(t, client, "PUT", path, "", `{"source":"gitee","expectedRevision":1}`)
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	response, e = client.Get(path)
	if e != nil {
		t.Fatal(e)
	}
	var view releasesource.SettingsView
	json.NewDecoder(response.Body).Decode(&view)
	response.Body.Close()
	if response.StatusCode != 200 || view.Source != releasesource.GitHub || view.Revision != 1 {
		t.Fatal(response.StatusCode, view)
	}
	for _, body := range []string{`{"source":"gitee","expectedRevision":1,"url":"https://bad.invalid"}`, `{"source":"gitee","expectedRevision":1} {}`, `{"source":"other","expectedRevision":1}`, `{"source":"gitee","expectedRevision":0}`, `{"source":"gitee","expectedRevision":1}` + strings.Repeat(" ", 5000)} {
		response = doJSON(t, client, "PUT", path, csrf, body)
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal(response.StatusCode, string(raw))
		}
	}
	response = doJSON(t, client, "PUT", path, csrf, `{"source":"gitee","expectedRevision":1}`)
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(raw))
	}
	response = doJSON(t, client, "PUT", path, csrf, `{"source":"github","expectedRevision":1}`)
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal(response.StatusCode)
	}
	var n int
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='release_source_settings_update'").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	response, e = client.Get(srv.URL + "/api/version/check?source=invalid")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
	got, e := settings.Get(context.Background())
	if e != nil || got.Source != releasesource.Gitee || got.Revision != 2 {
		t.Fatal(got, e)
	}
}
func TestReleaseSourceSettingsWritesRespectUpgradeMaintenance(t *testing.T) {
	updater, fence := reviewAdmission(t)
	if e := fence("upgrading"); e != nil {
		t.Fatal(e)
	}
	called := false
	h := updateAdmissionMiddleware(updater)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/version/source-settings", strings.NewReader(`{"source":"gitee","expectedRevision":1}`)))
	if called || rec.Code != 503 {
		t.Fatal(called, rec.Code)
	}
}
