package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// stubProber 让 HTTP 层测试可控仓库探测结果,不触网。
type stubProber struct {
	branch string
	err    error
}

func (s stubProber) Probe(_ context.Context, _ string, _ string) (string, error) {
	return s.branch, s.err
}

// setupProjectServer 构造带 auth + vault + project 的测试 server。
func setupProjectServer(t *testing.T, pr project.RemoteProber) (*httptest.Server, *http.Client, string, vault.Vault) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, pr)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithProjects(psvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf, v
}

// newGitCred 经 API 建一个 git_token 凭据,返回 id。
func newGitCred(t *testing.T, client *http.Client, srvURL, csrf, secret string) string {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, srvURL+"/api/credentials", csrf,
		`{"name":"ci","type":"git_token","secret":"`+secret+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	id, _ := c["id"].(string)
	if id == "" {
		t.Fatalf("create credential failed: %s", raw)
	}
	return id
}

func TestProjectCreateAndList(t *testing.T) {
	srv, client, csrf, _ := setupProjectServer(t, stubProber{branch: "main"})
	credID := newGitCred(t, client, srv.URL, csrf, "ghp_LEAKMARKER_secret")

	body := `{"name":"shop","repoUrl":"https://gitee.com/acme/shop.git","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "LEAKMARKER") {
		t.Fatalf("create 响应泄漏凭据明文: %s", raw)
	}
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	if p["defaultBranch"] != "main" {
		t.Fatalf("defaultBranch = %v, want main(探测)", p["defaultBranch"])
	}
	if p["credentialName"] != "ci" {
		t.Fatalf("credentialName = %v, want ci", p["credentialName"])
	}
	if p["lastRunStatus"] != nil {
		t.Fatalf("lastRunStatus 本期应为 null, got %v", p["lastRunStatus"])
	}
	ts, ok := p["targetServers"].([]any)
	if !ok || len(ts) != 0 {
		t.Fatalf("targetServers 本期应为 [], got %v", p["targetServers"])
	}

	// 列表。
	listResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects", "", "")
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	listRaw, _ := io.ReadAll(listResp.Body)
	if strings.Contains(string(listRaw), "LEAKMARKER") {
		t.Fatalf("list 泄漏凭据明文: %s", listRaw)
	}
	var list []map[string]any
	_ = json.Unmarshal(listRaw, &list)
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
}

func TestProjectCreateMissingCredential422(t *testing.T) {
	srv, client, csrf, _ := setupProjectServer(t, stubProber{branch: "main"})
	body := `{"name":"x","repoUrl":"https://gitee.com/a/b.git","credentialId":"no-such"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	assertErrCode(t, resp, "credential_error")
}

func TestTestCloneCredentialError422(t *testing.T) {
	srv, client, csrf, _ := setupProjectServer(t, stubProber{err: project.ErrCredentialError})
	credID := newGitCred(t, client, srv.URL, csrf, "badtoken_LEAK")
	body := `{"repoUrl":"https://gitee.com/a/b.git","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/test-clone", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "LEAK") {
		t.Fatalf("错误体泄漏凭据明文: %s", raw)
	}
	var body2 struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body2)
	if body2.Error.Code != "credential_error" {
		t.Fatalf("code = %q, want credential_error", body2.Error.Code)
	}
}

func TestTestCloneRepoUnreachable422(t *testing.T) {
	srv, client, csrf, _ := setupProjectServer(t, stubProber{err: project.ErrRepoUnreachable})
	credID := newGitCred(t, client, srv.URL, csrf, "tok")
	body := `{"repoUrl":"https://bad.invalid/a/b.git","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/test-clone", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	assertErrCode(t, resp, "repo_unreachable")
}

func TestTestCloneSuccess(t *testing.T) {
	srv, client, csrf, _ := setupProjectServer(t, stubProber{branch: "develop"})
	credID := newGitCred(t, client, srv.URL, csrf, "tok")
	body := `{"repoUrl":"https://gitee.com/a/b.git","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/test-clone", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["ok"] != true || out["defaultBranch"] != "develop" {
		t.Fatalf("test-clone success body = %v", out)
	}
}

func TestProjectVaultUnconfigured422(t *testing.T) {
	// 项目服务持有未配置的 vault → 创建/test-clone 应 422 vault_unconfigured。
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, nil) // 未配置
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithProjects(psvc)))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	body := `{"repoUrl":"https://gitee.com/a/b.git","credentialId":"any"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/test-clone", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	assertErrCode(t, resp, "vault_unconfigured")
}

func TestProjectPatchAndDelete(t *testing.T) {
	srv, client, csrf, _ := setupProjectServer(t, stubProber{branch: "main"})
	credID := newGitCred(t, client, srv.URL, csrf, "tok")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"old","repoUrl":"https://g/a.git","credentialId":"`+credID+`"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id: %s", raw)
	}

	patchResp := doJSON(t, client, http.MethodPatch, srv.URL+"/api/projects/"+id, csrf, `{"name":"renamed"}`)
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
	}
	pr, _ := io.ReadAll(patchResp.Body)
	var patched map[string]any
	_ = json.Unmarshal(pr, &patched)
	if patched["name"] != "renamed" {
		t.Fatalf("name not updated: %s", pr)
	}

	delResp := doJSON(t, client, http.MethodDelete, srv.URL+"/api/projects/"+id, csrf, "")
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}
	delAgain := doJSON(t, client, http.MethodDelete, srv.URL+"/api/projects/"+id, csrf, "")
	defer delAgain.Body.Close()
	if delAgain.StatusCode != http.StatusNotFound {
		t.Fatalf("delete-again status = %d, want 404", delAgain.StatusCode)
	}
	assertErrCode(t, delAgain, "project_not_found")
}

func TestProjectRequiresCSRF(t *testing.T) {
	srv, client, _, _ := setupProjectServer(t, stubProber{branch: "main"})
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", "",
		`{"name":"x","repoUrl":"https://g/a.git","credentialId":"c"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF)", resp.StatusCode)
	}
}

func TestProjectRequiresAuth(t *testing.T) {
	srv, _, _, _ := setupProjectServer(t, stubProber{branch: "main"})
	resp, err := http.Get(srv.URL + "/api/projects")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
