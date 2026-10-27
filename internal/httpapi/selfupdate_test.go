package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/selfupdate"
	"github.com/huangchengsir/pipewright/internal/version"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfUpdateAuthenticatedTaskProtocol(t *testing.T) {
	st := testStoreAuth(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-current")
	if e := os.WriteFile(exe, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer source.Close()
	up, e := selfupdate.New(st.DB, selfupdate.Options{Repository: "huangchengsir/pipewright", Executable: exe, Directory: filepath.Join(dir, "work"), BaseURL: source.URL, Client: source.Client(), Current: func() version.Info { return version.Info{Version: "1.0.0", Platform: "linux/amd64"} }, Capabilities: func() (version.DeploymentMode, bool) { return version.ModeBinary, true }})
	if e != nil {
		t.Fatal(e)
	}
	up.Start(context.Background())
	defer up.Close()
	authn := auth.NewService(st.DB, nil)
	if e = authn.Bootstrap("admin", "testpass"); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(New(testWebFS(), authn, WithSelfUpdate(up)))
	defer srv.Close()
	body := `{"requestId":"protocol-request","target":"2.0.0"}`
	response, e := http.Post(srv.URL+"/api/version/update-tasks", "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	response = doJSON(t, client, "POST", srv.URL+"/api/version/update-tasks", "", body)
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	var n int
	st.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&n)
	if n != 0 {
		t.Fatal("unauthorized work")
	}
	response = doJSON(t, client, "POST", srv.URL+"/api/version/update-tasks", csrf, body)
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var accepted updateResult
	if response.StatusCode != 202 || json.Unmarshal(raw, &accepted) != nil || accepted.TaskID == "" {
		t.Fatal(response.StatusCode, string(raw))
	}
	response = doJSON(t, client, "POST", srv.URL+"/api/version/update-tasks", csrf, body)
	raw, _ = io.ReadAll(response.Body)
	response.Body.Close()
	var duplicate updateResult
	json.Unmarshal(raw, &duplicate)
	if response.StatusCode != 202 || duplicate.TaskID != accepted.TaskID {
		t.Fatal(response.StatusCode, string(raw))
	}
	response, e = client.Get(srv.URL + "/api/version/update-tasks/current")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(raw), accepted.TaskID) {
		t.Fatal(response.StatusCode, string(raw))
	}
	response = doJSON(t, client, "POST", srv.URL+"/api/version/update-tasks", csrf, `{"requestId":"protocol-request","target":"3.0.0"}`)
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal(response.StatusCode)
	}
	response = doJSON(t, client, "POST", srv.URL+"/api/version/update-tasks", csrf, `{"requestId":"other","target":"2.0.0","url":"https://invalid"}`)
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
	st.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
