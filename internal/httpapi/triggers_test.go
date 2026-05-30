package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupTriggerServer 构造带 auth + vault + project + trigger 的测试 server,并返回一个项目 id。
func setupTriggerServer(t *testing.T) (*httptest.Server, *http.Client, string, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	tsvc := trigger.New(st.DB, v)
	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithTriggers(tsvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_proj")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"shop","repoUrl":"https://gitee.com/acme/shop.git","credentialId":"`+credID+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	projID, _ := p["id"].(string)
	if projID == "" {
		t.Fatalf("create project failed: %s", raw)
	}
	return srv, client, csrf, projID
}

func TestTriggerGetLazyDefaultMasked(t *testing.T) {
	srv, client, csrf, projID := setupTriggerServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/trigger", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)

	url, _ := dto["webhookUrl"].(string)
	if !strings.HasPrefix(url, "/api/webhooks/") {
		t.Fatalf("webhookUrl = %q, want /api/webhooks/<token>", url)
	}
	masked, _ := dto["webhookSecretMasked"].(string)
	if !strings.Contains(masked, "••••") {
		t.Fatalf("密钥应掩码: %q", masked)
	}
	if _, ok := dto["webhookSecret"]; ok {
		t.Fatal("GET 不应回显完整密钥字段")
	}
	if dto["unmatchedPolicy"] != "record" {
		t.Fatalf("默认策略应为 record, got %v", dto["unmatchedPolicy"])
	}
	ev, _ := dto["events"].(map[string]any)
	if ev["push"] != false || ev["tag"] != false || ev["pullRequest"] != false {
		t.Fatalf("默认 events 应全 false: %v", ev)
	}
	if bm, ok := dto["branchMappings"].([]any); !ok || len(bm) != 0 {
		t.Fatalf("默认映射应为 []: %v", dto["branchMappings"])
	}
}

func TestTriggerSaveAndReset(t *testing.T) {
	srv, client, csrf, projID := setupTriggerServer(t)
	base := srv.URL + "/api/projects/" + projID + "/trigger"

	// 保存 events + 映射 + 策略。
	body := `{"events":{"push":true,"tag":false,"pullRequest":true},
	          "branchMappings":[{"branchPattern":"main","environment":"生产","targetServerIds":[]},
	                            {"branchPattern":"release/*","environment":"预发"}],
	          "unmatchedPolicy":"ignore"}`
	resp := doJSON(t, client, http.MethodPut, base, csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if dto["unmatchedPolicy"] != "ignore" {
		t.Fatalf("policy = %v, want ignore", dto["unmatchedPolicy"])
	}
	bm, _ := dto["branchMappings"].([]any)
	if len(bm) != 2 {
		t.Fatalf("映射数 = %d, want 2", len(bm))
	}
	if !strings.Contains(string(raw), "••••") {
		t.Fatal("PUT 响应密钥应掩码")
	}

	// 重置密钥:回显完整明文一次 + 掩码。
	rresp := doJSON(t, client, http.MethodPost, base+"/secret/reset", csrf, "")
	defer rresp.Body.Close()
	if rresp.StatusCode != http.StatusOK {
		t.Fatalf("reset status = %d, want 200", rresp.StatusCode)
	}
	rraw, _ := io.ReadAll(rresp.Body)
	var reset map[string]any
	_ = json.Unmarshal(rraw, &reset)
	secret, _ := reset["webhookSecret"].(string)
	if !strings.HasPrefix(secret, "whsec_") || strings.Contains(secret, "••••") {
		t.Fatalf("reset 应回显完整明文: %q", secret)
	}
	if !strings.Contains(reset["webhookSecretMasked"].(string), "••••") {
		t.Fatal("reset 应同时给掩码")
	}

	// 复位后再 GET:不应回显完整明文。
	gresp := doJSON(t, client, http.MethodGet, base, csrf, "")
	defer gresp.Body.Close()
	graw, _ := io.ReadAll(gresp.Body)
	if strings.Contains(string(graw), secret) {
		t.Fatalf("GET 不应含重置后的完整明文: %s", graw)
	}
}

func TestTriggerSaveInvalidBranchPattern422(t *testing.T) {
	srv, client, csrf, projID := setupTriggerServer(t)
	base := srv.URL + "/api/projects/" + projID + "/trigger"
	resp := doJSON(t, client, http.MethodPut, base, csrf,
		`{"events":{},"branchMappings":[{"branchPattern":"","environment":"e"}],"unmatchedPolicy":"record"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var e map[string]map[string]string
	_ = json.Unmarshal(raw, &e)
	if e["error"]["code"] == "" {
		t.Fatalf("应返回 {error:{code,message}}: %s", raw)
	}
}

func TestTriggerRequiresAuth(t *testing.T) {
	srv, _, _, projID := setupTriggerServer(t)
	// 不带会话 cookie 的裸客户端。
	resp, err := http.Get(srv.URL + "/api/projects/" + projID + "/trigger")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无会话 GET status = %d, want 401", resp.StatusCode)
	}
}

func TestTriggerWriteRequiresCSRF(t *testing.T) {
	srv, client, _, projID := setupTriggerServer(t)
	base := srv.URL + "/api/projects/" + projID + "/trigger"
	// 带会话但不带 CSRF header 的 PUT。
	req, _ := http.NewRequest(http.MethodPut, base, strings.NewReader(`{"unmatchedPolicy":"record"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 CSRF PUT status = %d, want 403", resp.StatusCode)
	}
}
