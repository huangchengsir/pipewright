package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/dbmigration"
	"github.com/huangchengsir/pipewright/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDatabaseRecoveryAuthenticationDoesNotWriteSource(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	s, e := store.Open(sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	service := auth.NewService(s.DB, nil)
	if e = service.Bootstrap("admin", "recovery-password"); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"sessions", "admin_user"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, e = s.DB.Exec("CREATE TRIGGER deny_" + table + op + " BEFORE " + op + " ON " + table + " BEGIN SELECT RAISE(FAIL,'readonly source'); END"); e != nil {
				t.Fatal(e)
			}
		}
	}
	key := [32]byte{42}
	c, e := dbmigration.AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	endpoint, e := dbmigration.Describe(context.Background(), s, sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	a := dbmigration.ActiveConfig{Connection: dbmigration.Connection{Driver: "sqlite", DSN: sourcePath}, Endpoint: endpoint}
	b := dbmigration.ActiveConfig{Connection: dbmigration.Connection{Driver: "postgres", DSN: "postgres://private-password@invalid/db"}, Endpoint: dbmigration.Endpoint{Dialect: "postgres", Server: "invalid", Database: "db", Schema: "empty"}}
	state, e := c.Plan(a, b)
	if e != nil {
		t.Fatal(e)
	}
	runtime := dbmigration.NewRuntime(c, a, s, &key, true, nil)
	defer runtime.Close()
	handler := NewDatabaseRecovery(fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}}, runtime)
	request := func(method, path, body, origin string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set(headerCsrf, csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if bytes.Contains(w.Body.Bytes(), []byte("private-password")) {
			t.Fatal("DSN in response")
		}
		return w
	}
	if w := request("GET", "/api/database-recovery/session", "", "", nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	login := `{"username":"admin","password":"recovery-password"}`
	if w := request("POST", "/api/database-recovery/login", login, "https://foreign.test", nil, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/database-recovery/login", login, "http://example.test", nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	var result struct {
		CSRF string `json:"csrf"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if w = request("GET", "/api/database-recovery/session", "", "", cookies[0], ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	body, _ := json.Marshal(map[string]any{"taskId": state.TaskID, "revision": state.Revision})
	if w = request("POST", "/api/database-recovery/continue", string(body), "http://example.test", cookies[0], ""); w.Code != 403 {
		t.Fatal("CSRF accepted", w.Code)
	}
	if w = request("POST", "/api/database-recovery/continue", string(body), "http://example.test", cookies[0], result.CSRF); w.Code != 409 {
		t.Fatal("invalid state accepted", w.Code)
	}
	if w = request("POST", "/api/auth/login", login, "http://example.test", nil, ""); w.Code != 503 {
		t.Fatal("ordinary auth mounted", w.Code)
	}
	var n int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n); e != nil || n != 0 {
		t.Fatal("source sessions changed", n, e)
	}
}
