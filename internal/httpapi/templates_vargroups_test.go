package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/library"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupLibraryServer 构造带 auth + vault + project + pipeline + templates + variable-groups 的测试 server。
func setupLibraryServer(t *testing.T) (*httptest.Server, *http.Client, string, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	pipes := pipeline.New(st.DB)
	tplSvc := library.NewTemplateService(st.DB, pipes)
	vgSvc := library.NewVarGroupService(st.DB, v)
	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithPipelines(pipes),
		WithTemplates(tplSvc), WithVariableGroups(vgSvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_library_proj")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"libproj","repoUrl":"https://gitee.com/acme/lib.git","credentialId":"`+credID+`"}`)
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

func TestTemplateCreateApplyToProjectHTTP(t *testing.T) {
	srv, client, csrf, projID := setupLibraryServer(t)

	// 建模板:含一个源阶段 + 一个构建阶段。
	tplBody := `{"name":"go-svc","description":"Go 模板","stages":[
		{"name":"源","kind":"source","jobs":[{"name":"git","type":"git_source"}]},
		{"name":"构建","kind":"build","jobs":[{"name":"compile","type":"build"}]}
	]}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/templates", csrf, tplBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create template status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var tpl map[string]any
	_ = json.Unmarshal(raw, &tpl)
	tplID, _ := tpl["id"].(string)
	if tplID == "" {
		t.Fatalf("template id empty: %s", raw)
	}

	// 应用模板到项目流水线。
	applyResp := doJSON(t, client, http.MethodPost,
		srv.URL+"/api/projects/"+projID+"/pipeline/apply-template", csrf,
		`{"templateId":"`+tplID+`"}`)
	defer applyResp.Body.Close()
	if applyResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(applyResp.Body)
		t.Fatalf("apply status = %d, body=%s", applyResp.StatusCode, raw)
	}
	rawApply, _ := io.ReadAll(applyResp.Body)
	var applied map[string]any
	_ = json.Unmarshal(rawApply, &applied)
	stages, _ := applied["stages"].([]any)
	if len(stages) != 2 {
		t.Fatalf("applied pipeline stages = %d, want 2: %s", len(stages), rawApply)
	}
	if yaml, _ := applied["yaml"].(string); strings.TrimSpace(yaml) == "" {
		t.Fatalf("applied pipeline should render YAML")
	}

	// 读回项目流水线,确认与模板一致(落库)。
	getResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline", csrf, "")
	defer getResp.Body.Close()
	rawGet, _ := io.ReadAll(getResp.Body)
	var got map[string]any
	_ = json.Unmarshal(rawGet, &got)
	if gotStages, _ := got["stages"].([]any); len(gotStages) != 2 {
		t.Fatalf("project pipeline stages = %d, want 2: %s", len(gotStages), rawGet)
	}
}

func TestTemplateApplyMissingTemplateHTTP(t *testing.T) {
	srv, client, csrf, projID := setupLibraryServer(t)
	resp := doJSON(t, client, http.MethodPost,
		srv.URL+"/api/projects/"+projID+"/pipeline/apply-template", csrf,
		`{"templateId":"nope"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, raw)
	}
}

func TestVariableGroupSecretMaskedRoundTripHTTP(t *testing.T) {
	srv, client, csrf, _ := setupLibraryServer(t)

	const leak = "vg_LEAKMARKER_secret_zzz"
	credID := newGitCred(t, client, srv.URL, csrf, leak)

	body := `{"name":"shared-env","vars":[
		{"key":"API_URL","secret":false,"value":"https://api.example.com"},
		{"key":"TOKEN","secret":true,"credentialId":"` + credID + `","value":"should-be-dropped"}
	]}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/variable-groups", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create variable group status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	// 响应绝无明文 secret。
	if strings.Contains(string(raw), leak) || strings.Contains(string(raw), "should-be-dropped") {
		t.Fatalf("variable group response leaked plaintext: %s", raw)
	}
	var g map[string]any
	_ = json.Unmarshal(raw, &g)
	vars, _ := g["vars"].([]any)
	if len(vars) != 2 {
		t.Fatalf("vars = %d, want 2: %s", len(vars), raw)
	}
	for _, vAny := range vars {
		vmap, _ := vAny.(map[string]any)
		if vmap["key"] == "TOKEN" {
			if vmap["secret"] != true {
				t.Fatalf("TOKEN should be secret: %v", vmap)
			}
			if _, hasValue := vmap["value"]; hasValue {
				t.Fatalf("secret var should not carry plaintext value: %v", vmap)
			}
			if vmap["credentialId"] != credID {
				t.Fatalf("secret var credentialId = %v, want %s", vmap["credentialId"], credID)
			}
			if masked, _ := vmap["maskedValue"].(string); masked == "" {
				t.Fatalf("secret var should carry maskedValue: %v", vmap)
			}
		}
	}

	gid, _ := g["id"].(string)
	// 列表与详情同样不泄漏明文。
	listResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/variable-groups", csrf, "")
	defer listResp.Body.Close()
	rawList, _ := io.ReadAll(listResp.Body)
	if strings.Contains(string(rawList), leak) {
		t.Fatalf("variable group list leaked plaintext: %s", rawList)
	}

	// 删除。
	delResp := doJSON(t, client, http.MethodDelete, srv.URL+"/api/variable-groups/"+gid, csrf, "")
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}
}

func TestVariableGroupDuplicateKeysRejectedHTTP(t *testing.T) {
	srv, client, csrf, _ := setupLibraryServer(t)
	body := `{"name":"dup","vars":[{"key":"K","value":"1"},{"key":"K","value":"2"}]}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/variable-groups", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422, body=%s", resp.StatusCode, raw)
	}
}
