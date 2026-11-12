package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/dbmigration"
)

func TestDatabaseConnectionProbeAuthCSRFAndNoMutation(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if e := svc.Bootstrap("admin", "testpass"); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	key := [32]byte{54}
	control, e := dbmigration.AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	defer control.Close()
	runtime := dbmigration.NewRuntime(control, dbmigration.ActiveConfig{Connection: dbmigration.Connection{Driver: "sqlite"}}, st, &key, false, nil)
	defer runtime.Close()
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithDatabaseMigration(runtime)))
	defer srv.Close()
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	target := filepath.Join(dir, "new.db")
	payload, _ := json.Marshal(map[string]any{"driver": "sqlite", "file": target})
	post := func(client *http.Client, csrf string, payload []byte) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/api/settings/database/test-connection", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set(headerCsrf, csrf)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if status, _ := post(http.DefaultClient, "", payload); status != 401 {
		t.Fatalf("anonymous %d", status)
	}
	if status, _ := post(client, "", payload); status != 403 {
		t.Fatalf("CSRF %d", status)
	}
	before, _ := control.State()
	if status, body := post(client, csrf, payload); status != 200 || strings.TrimSpace(body) != `{"kind":"path"}` {
		t.Fatalf("result %d %s", status, body)
	}
	after, _ := control.State()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("task changed")
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("target file created")
	}
	for _, body := range []string{`{"driver":"sqlite","file":"x","managed":true}`, `{"driver":"mysql","host":"bad host","username":"secret-marker","password":"secret-marker"}`} {
		status, result := post(client, csrf, []byte(body))
		if status < 400 || strings.Contains(result, "secret-marker") {
			t.Fatalf("unsafe response %d %s", status, result)
		}
	}
}
func TestDatabaseConnectionProbeSkipsWriteAdmission(t *testing.T) {
	calls := 0
	h := workAdmissionMiddleware(func(_ context.Context, _ string) (func(), error) { calls++; return func() {}, nil })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/settings/database/test-connection", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if calls != 0 || w.Code != 204 {
		t.Fatal("read-only probe entered write admission")
	}
}
