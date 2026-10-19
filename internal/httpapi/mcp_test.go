package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/mask"
	pmcp "github.com/huangchengsir/pipewright/internal/mcp"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpFixture struct {
	db                                *sql.DB
	tokens                            *auth.MCPTokenStore
	runs                              run.Service
	project, other, readonly, trigger string
	readonlyItem                      *auth.MCPToken
	handler                           http.Handler
	audit                             audit.Recorder
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	db := storetest.OpenDB(t)
	stamp := time.Now().UTC().Format(time.RFC3339)
	cred := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO credentials (id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES (?,'fixture','git_token','',X'00','m',?,?)`, cred, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	f := &mcpFixture{db: db, tokens: auth.NewMCPTokenStore(db), runs: run.New(db), project: uuid.NewString(), other: uuid.NewString(), audit: audit.New(db, nil, nil)}
	for _, id := range []string{f.project, f.other} {
		if _, err := db.Exec(`INSERT INTO projects (id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,'untrusted project','https://user:SECRET@example.invalid/repo','main',?,?,?)`, id, cred, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.readonly, f.readonlyItem, err = f.tokens.Create(context.Background(), auth.MCPTokenInput{Name: "read", ProjectIDs: []string{f.project}})
	if err != nil {
		t.Fatal(err)
	}
	f.trigger, _, err = f.tokens.Create(context.Background(), auth.MCPTokenInput{Name: "trigger", ProjectIDs: []string{f.project}, CanTrigger: true})
	if err != nil {
		t.Fatal(err)
	}
	f.handler, err = NewMCPHandler(f.tokens, pmcp.Dependencies{Projects: project.New(db, nil, nil), Pipelines: pipeline.New(db), Runs: f.runs, Parameters: run.NewParameterService(db), Audit: f.audit, PollInterval: 5 * time.Millisecond}, MCPHTTPOptions{AllowedHosts: []string{"pipewright.example"}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMCPMountedInMainRouterKeepsAdminAuthenticationSeparate(t *testing.T) {
	f := newMCPFixture(t)
	f.handler = New(testWebFSAuth(), &opsAuth{}, WithMCP(f.handler, f.tokens), WithAudit(f.audit))
	out := mcpOutput(t, f.call(t, f.readonly, "projects_list", map[string]any{}))
	if len(out["projects"].([]any)) != 1 {
		t.Fatal("embedded MCP route not connected")
	}
	for _, path := range []string{"/api/settings/mcp/tokens", "/api/credentials"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+f.readonly)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("MCP token authorized admin route %s", path)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	r.Host = "localhost"
	r.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("admin cookie authenticated MCP")
	}
	body := fmt.Sprintf(`{"name":"main-router","projectIds":[%q],"expiresInDays":1}`, f.project)
	r = httptest.NewRequest(http.MethodPost, "/api/settings/mcp/tokens", strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
	r.Header.Set(headerCsrf, "csrf")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("token issuance status=%d body=%s", w.Code, w.Body.String())
	}
	var issued struct {
		Token string        `json:"token"`
		Item  auth.MCPToken `json:"item"`
	}
	if json.Unmarshal(w.Body.Bytes(), &issued) != nil || issued.Token == "" || issued.Item.CanTrigger {
		t.Fatal("token management integration")
	}
	opsHTTP(t, f.handler, http.MethodDelete, "/api/settings/mcp/tokens/"+issued.Item.ID, "", http.StatusNoContent)
	if _, err := f.tokens.Verify(context.Background(), issued.Token); err == nil {
		t.Fatal("main-router revoke ineffective")
	}
}

func (f *mcpFixture) rpc(token, method string, params any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	r := httptest.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", "2025-06-18")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f *mcpFixture) call(t *testing.T, token, name string, args any) map[string]any {
	t.Helper()
	w := f.rpc(token, "tools/call", map[string]any{"name": name, "arguments": args})
	if w.Code != http.StatusOK {
		t.Fatalf("call %s HTTP %d: %s", name, w.Code, w.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP result missing: %s", w.Body.String())
	}
	return result
}

func mcpOutput(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	if result["isError"] == true {
		t.Fatalf("tool error: %v", result)
	}
	out, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatal("structured output missing", result)
	}
	return out
}

func expectMCPError(t *testing.T, result map[string]any, code string) {
	t.Helper()
	blob, _ := json.Marshal(result)
	if result["isError"] != true || !strings.Contains(string(blob), code) {
		t.Fatalf("wanted %s: %s", code, blob)
	}
}

func TestMCPProtocolPermissionsAndSafeOutputs(t *testing.T) {
	f := newMCPFixture(t)
	w := f.rpc(f.readonly, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if w.Code != 200 || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("not stateless initialize", w.Code, w.Body.String())
	}
	w = f.rpc(f.readonly, "tools/list", map[string]any{})
	var envelope struct {
		Result struct {
			Tools []sdk.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range envelope.Result.Tools {
		names[tool.Name] = true
		if tool.Name == "pipeline_trigger" && (tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint) {
			t.Fatal("trigger annotation")
		}
	}
	if len(names) != 6 {
		t.Fatal("tools", names)
	}
	for _, name := range []string{"projects_list", "pipeline_get", "pipeline_trigger", "run_get", "run_wait", "deployments_get"} {
		if !names[name] {
			t.Fatal("missing tool", name)
		}
	}
	list := mcpOutput(t, f.call(t, f.readonly, "projects_list", map[string]any{}))
	blob, _ := json.Marshal(list)
	if strings.Contains(string(blob), f.other) || strings.Contains(string(blob), "repo_url") || strings.Contains(string(blob), "SECRET") {
		t.Fatal("project scope leak", string(blob))
	}
	pipe := mcpOutput(t, f.call(t, f.readonly, "pipeline_get", map[string]any{"projectId": f.project}))
	if pipe["storedPipelinePresent"] != false || pipe["summarySource"] != "none" {
		t.Fatal("missing stored pipeline was misrepresented")
	}
	var storedCount int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_configs WHERE project_id = ?`, f.project).Scan(&storedCount); err != nil || storedCount != 0 {
		t.Fatal("read-only pipeline query wrote defaults", storedCount, err)
	}
	blob, _ = json.Marshal(pipe)
	for _, forbidden := range []string{"SECRET", "config", "yaml", "credential", "repo_url"} {
		if strings.Contains(string(blob), forbidden) {
			t.Fatal("pipeline disclosure", string(blob))
		}
	}
	expectMCPError(t, f.call(t, f.readonly, "pipeline_get", map[string]any{"projectId": f.other}), "forbidden")
	expectMCPError(t, f.call(t, f.readonly, "pipeline_trigger", map[string]any{"projectId": f.project, "requestId": "readonly"}), "forbidden")
	expectMCPError(t, f.call(t, f.trigger, "pipeline_trigger", map[string]any{"projectId": f.other, "requestId": "other"}), "forbidden")
	for _, args := range []map[string]any{{"projectId": f.project, "requestId": "bad", "branch": "https://user:password@evil"}, {"projectId": f.project, "requestId": "bad", "commit": "bad"}, {"projectId": f.project, "requestId": "bad", "params": map[string]string{"BAD-KEY": "SECRET"}}, {"projectId": f.project, "requestId": "bad", "params": map[string]string{"GOOD": strings.Repeat("x", 1025)}}} {
		expectMCPError(t, f.call(t, f.trigger, "pipeline_trigger", args), "invalid_argument")
	}
	var count int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count)
	if count != 0 {
		t.Fatal("denied operation created run")
	}
	r, err := f.runs.Create(context.Background(), f.other, run.Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"run_get", "run_wait", "deployments_get"} {
		expectMCPError(t, f.call(t, f.readonly, tool, map[string]any{"runId": r.ID}), "forbidden")
	}
	expectMCPError(t, f.call(t, f.readonly, "projects_list", map[string]any{"pageSize": 33}), "invalid_argument")
	expectMCPError(t, f.call(t, f.readonly, "projects_list", map[string]any{"unknown": "SECRET"}), "invalid_argument")
}

func TestMCPTriggerConcurrentConflictParametersAndAudit(t *testing.T) {
	f := newMCPFixture(t)
	params := run.NewParameterService(f.db)
	if _, err := params.Save(context.Background(), f.project, []run.ParamDef{{Key: "MODE", Type: run.ParamTypeChoice, Options: []string{"safe", "SECRET"}, Default: "safe", Required: true}}); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"projectId": f.project, "requestId": "concurrent", "params": map[string]string{"MODE": "SECRET"}}
	var wg sync.WaitGroup
	results := make(chan map[string]any, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- f.call(t, f.trigger, "pipeline_trigger", args) }()
	}
	wg.Wait()
	close(results)
	id := ""
	for result := range results {
		out := mcpOutput(t, result)
		got := out["runId"].(string)
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("duplicate IDs", id, got)
		}
		if out["dispatchState"] != "unknown" && out["dispatchState"] != "rejected" {
			t.Fatal("absent pool enqueue claimed accepted")
		}
		blob, _ := json.Marshal(result)
		if strings.Contains(string(blob), "SECRET") {
			t.Fatal("parameter output disclosure")
		}
	}
	var count int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count)
	if count != 1 {
		t.Fatal("run count", count)
	}
	r, err := f.runs.Get(context.Background(), id)
	if err != nil || r.Trigger.Params["MODE"] != "SECRET" {
		t.Fatal("Resolve bypassed", r, err)
	}
	expectMCPError(t, f.call(t, f.trigger, "pipeline_trigger", map[string]any{"projectId": f.project, "requestId": "concurrent", "params": map[string]string{"MODE": "safe"}}), "idempotency_conflict")
	expectMCPError(t, f.call(t, f.trigger, "pipeline_trigger", map[string]any{"projectId": f.project, "requestId": "invalid-param", "params": map[string]string{"MODE": "invalid SECRET"}}), "invalid_argument")
	entries, err := f.audit.List(context.Background(), audit.ListFilter{Action: "mcp_pipeline.trigger"})
	if err != nil || len(entries.Entries) == 0 {
		t.Fatal("missing trigger audit", err)
	}
	blob, _ := json.Marshal(entries)
	if strings.Contains(string(blob), "SECRET") || strings.Contains(string(blob), f.trigger) || strings.Contains(string(blob), "MODE") {
		t.Fatal("audit disclosure", string(blob))
	}
}

func TestMCPReplayIgnoresChangedParameterDefinitions(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()
	params := run.NewParameterService(f.db)
	if _, err := params.Save(ctx, f.project, []run.ParamDef{{Key: "MODE", Type: run.ParamTypeChoice, Options: []string{"old"}, Default: "old", Required: true}}); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"projectId": f.project, "requestId": "parameter-replay", "params": map[string]string{"MODE": "old"}}
	first := mcpOutput(t, f.call(t, f.trigger, "pipeline_trigger", args))
	if _, err := params.Save(ctx, f.project, []run.ParamDef{{Key: "MODE", Type: run.ParamTypeChoice, Options: []string{"new"}, Default: "new", Required: true}}); err != nil {
		t.Fatal(err)
	}
	replay := mcpOutput(t, f.call(t, f.trigger, "pipeline_trigger", args))
	if replay["runId"] != first["runId"] || replay["dispatchState"] != "rejected" || replay["status"] != run.StatusFailed {
		t.Fatal("parameter change broke replay", replay)
	}
	for _, tool := range []string{"run_get", "run_wait"} {
		out := mcpOutput(t, f.call(t, f.readonly, tool, map[string]any{"runId": first["runId"]}))
		if out["dispatchState"] != "rejected" || out["status"] != run.StatusFailed {
			t.Fatal("status query lost rejection", tool, out)
		}
	}
	expectMCPError(t, f.call(t, f.trigger, "pipeline_trigger", map[string]any{"projectId": f.project, "requestId": "new-invalid", "params": map[string]string{"MODE": "old"}}), "invalid_argument")
}

func TestMCPRejectsLegacyBatchesBeforeDispatch(t *testing.T) {
	f := newMCPFixture(t)
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pipeline_trigger","arguments":{"projectId":"` + f.project + `","requestId":"batch"}}}`
	for _, protocol := range []string{"2025-03-26", "2025-06-18"} {
		r := httptest.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(" \n["+call+"]"))
		r.Header.Set("Authorization", "Bearer "+f.trigger)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", protocol)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "mcp_batch_denied") {
			t.Fatal("legacy batch reached SDK", protocol, w.Code)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("batch created work", count, err)
	}
}

func TestMCPRegistersLocalSecretsBeforeProjection(t *testing.T) {
	f := newMCPFixture(t)
	v := vault.New(f.db, &[32]byte{1})
	secret := "fixture-mask-secret-123"
	sealed, err := v.SealSecret([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE credentials SET ciphertext = ?`, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE projects SET name = ?, default_branch = ? WHERE id = ?`, "project "+secret, "release/"+secret, f.project); err != nil {
		t.Fatal(err)
	}
	m := mask.NewMasker()
	f.handler, err = NewMCPHandler(f.tokens, pmcp.Dependencies{
		Projects: project.New(f.db, nil, nil), Runs: f.runs, Masker: m,
		RegisterSecrets: func(ctx context.Context) error { return vault.RegisterMaskSecrets(ctx, v, m) },
	}, MCPHTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	list := f.call(t, f.readonly, "projects_list", map[string]any{})
	rn, err := f.runs.Create(context.Background(), f.project, run.Trigger{Branch: "release/" + secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.AddArtifact(context.Background(), run.Artifact{RunID: rn.ID, Type: run.ArtifactImage, Reference: "registry.example/" + secret + "/service:v1"}); err != nil {
		t.Fatal(err)
	}
	for _, output := range []map[string]any{list, f.call(t, f.readonly, "run_get", map[string]any{"runId": rn.ID}), f.call(t, f.readonly, "deployments_get", map[string]any{"runId": rn.ID})} {
		body, _ := json.Marshal(output)
		if output["isError"] == true || strings.Contains(string(body), secret) {
			t.Fatal("local secret projection failed")
		}
	}
	if !strings.Contains(fmt.Sprint(list), "[MASKED]") {
		t.Fatal("secret registrar was not used")
	}
}

func TestMCPHTTPOriginHostBodyAndAuthLimits(t *testing.T) {
	f := newMCPFixture(t)
	for _, tc := range []struct {
		host, origin, token string
		want                int
	}{
		{"localhost", "", f.readonly, 200}, {"LOCALHOST:18090", "https://localhost:18090", f.readonly, 200}, {"pipewright.example:443", "https://pipewright.example", f.readonly, 200},
		{"localhost.evil", "", f.readonly, 403}, {"evil.test", "", f.readonly, 403}, {"localhost", "https://localhost.evil", f.readonly, 403}, {"localhost", "null", f.readonly, 403},
		{"localhost", "https://user:pass@localhost", f.readonly, 403}, {"localhost", "https://localhost/", f.readonly, 403}, {"localhost", "https://localhost?", f.readonly, 403},
		{"localhost", "https://localhost#frag", f.readonly, 403}, {"localhost", "https://localhost:0", f.readonly, 403}, {"localhost:65536", "", f.readonly, 403},
		{"localhost", "https://localhost#", f.readonly, 403},
		{"localhost@evil", "", f.readonly, 403}, {"localhost.", "", f.readonly, 403}, {"localhost", "https://localhost, https://evil", f.readonly, 403},
		{"localhost", "", "", 401}, {"localhost", "", strings.Repeat("0", 64), 401},
	} {
		t.Run(fmt.Sprintf("%s_%s_%d", tc.host, tc.origin, tc.want), func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://localhost/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Location") != "" {
				t.Fatal("CORS or redirect")
			}
		})
	}
	for _, body := range []string{strings.Repeat("x", MCPMaxRequestBytes+1), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pipeline_trigger","arguments":{"projectId":"` + f.project + `","requestId":"oversize"}}}` + strings.Repeat(" ", MCPMaxRequestBytes)} {
		r := httptest.NewRequest("POST", "http://localhost/mcp", strings.NewReader(body))
		r.ContentLength = -1
		r.Header.Set("Authorization", "Bearer "+f.trigger)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatal("oversize", w.Code)
		}
	}
	var count int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count)
	if count != 0 {
		t.Fatal("oversize triggered side effect")
	}
	for _, method := range []string{"GET", "DELETE", "OPTIONS"} {
		r := httptest.NewRequest(method, "http://localhost/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+f.readonly)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 405 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("method", method, w.Code)
		}
	}
	for _, host := range []string{"*.example", "https://example", "example:443", "user@example", "localhost."} {
		if _, err := NewMCPHandler(f.tokens, pmcp.Dependencies{}, MCPHTTPOptions{AllowedHosts: []string{host}}); err == nil {
			t.Fatal("bad configuration accepted", host)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+f.readonly)
	r.Header["Origin"] = []string{"https://localhost", "https://localhost"}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate Origins")
	}
	r = httptest.NewRequest("POST", "http://localhost/mcp", nil)
	r.AddCookie(&http.Cookie{Name: cookieSession, Value: f.readonly})
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("cookie authenticated MCP")
	}
}

func TestMCPWaitRevocationApprovalTimeoutAndCancellation(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()
	rn, err := f.runs.Create(ctx, f.project, run.Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE pipeline_runs SET status='waiting_approval' WHERE id=?`, rn.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out := mcpOutput(t, f.call(t, f.readonly, "run_wait", map[string]any{"runId": rn.ID, "waitSeconds": 30}))
	if out["waitingForHumanApproval"] != true || time.Since(start) > time.Second {
		t.Fatal("approval blocked wait")
	}
	if _, err := f.db.Exec(`UPDATE pipeline_runs SET status='running' WHERE id=?`, rn.ID); err != nil {
		t.Fatal(err)
	}
	out = mcpOutput(t, f.call(t, f.readonly, "run_wait", map[string]any{"runId": rn.ID, "waitSeconds": 1}))
	if out["waitTimedOut"] != true || out["status"] != "running" {
		t.Fatal("timeout changed run", out)
	}
	expectMCPError(t, f.call(t, f.readonly, "run_wait", map[string]any{"runId": rn.ID, "waitSeconds": 31}), "invalid_argument")
	done := make(chan map[string]any, 1)
	go func() { done <- f.call(t, f.readonly, "run_wait", map[string]any{"runId": rn.ID, "waitSeconds": 30}) }()
	time.Sleep(30 * time.Millisecond)
	if err := f.tokens.Revoke(ctx, f.readonlyItem.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		expectMCPError(t, result, "unauthorized")
	case <-time.After(time.Second):
		t.Fatal("long wait retained revoked permission")
	}
	if w := f.rpc(f.readonly, "tools/list", map[string]any{}); w.Code != 401 {
		t.Fatal("connected token still valid")
	}
	current, _ := f.runs.Get(ctx, rn.ID)
	if current.Status != "running" {
		t.Fatal("revoke canceled run")
	}
	// Cancel the HTTP request, not the underlying execution.
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "run_wait", "arguments": map[string]any{"runId": rn.ID, "waitSeconds": 30}}})
	r := httptest.NewRequest("POST", "http://localhost/mcp", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+f.trigger)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	cancelCtx, cancel := context.WithCancel(r.Context())
	exited := make(chan struct{})
	go func() { defer close(exited); f.handler.ServeHTTP(httptest.NewRecorder(), r.WithContext(cancelCtx)) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("HTTP cancel ignored")
	}
	current, _ = f.runs.Get(ctx, rn.ID)
	if current.Status != "running" {
		t.Fatal("wait cancellation canceled execution")
	}
}

func TestMCPPartialDeploymentRecordsAndVersionSafety(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()
	rn, err := f.runs.Create(ctx, f.project, run.Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	out := mcpOutput(t, f.call(t, f.readonly, "deployments_get", map[string]any{"runId": rn.ID}))
	if out["hasDeploymentRecords"] != false || out["liveVerified"] != false {
		t.Fatal("no deployment claimed success")
	}
	if err := f.runs.SaveDeployTargets(ctx, rn.ID, []run.DeployTarget{{ServerID: "success", ServerName: "one", Status: run.TargetSuccess, StartedAt: time.Now()}, {ServerID: "failed", ServerName: "two", Status: run.TargetFailed, Message: "password=SECRET failure log", StartedAt: time.Now()}, {ServerID: "rollback", ServerName: "three", Status: run.TargetRolledBack, StartedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if err := f.runs.SetDeployTerminal(ctx, rn.ID, run.StatusPartialFailed); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, ref := range []string{"registry.example/app:1", "https://user:SECRET@registry.example/app:1", "registry.example/app@bad"} {
		if _, err := f.runs.AddArtifact(ctx, run.Artifact{RunID: rn.ID, Type: run.ArtifactImage, Name: "artifact", Reference: ref, Metadata: map[string]any{"digest": digest, "digestRepository": "registry.example/app", "pushed": true}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.runs.SetFailureLog(ctx, rn.ID, "SECRET failure log"); err != nil {
		t.Fatal(err)
	}
	out = mcpOutput(t, f.call(t, f.readonly, "deployments_get", map[string]any{"runId": rn.ID}))
	blob, _ := json.Marshal(out)
	if strings.Contains(string(blob), "SECRET") || strings.Contains(string(blob), "password") || strings.Contains(string(blob), "failure log") {
		t.Fatal("deployment disclosure", string(blob))
	}
	targets := out["targets"].([]any)
	if len(targets) != 3 || out["liveVerified"] != false {
		t.Fatal(out)
	}
	statuses := map[string]bool{}
	for _, v := range targets {
		target := v.(map[string]any)
		statuses[target["status"].(string)] = true
		if target["liveVerified"] != false {
			t.Fatal("live claim")
		}
	}
	if !statuses["success"] || !statuses["failed"] || !statuses["rolled_back"] {
		t.Fatal("partial failure lost")
	}
	versions := out["artifactCandidates"].([]any)
	valid, redacted := 0, 0
	for _, version := range versions {
		v := version.(map[string]any)
		if v["reference"] == "registry.example/app:1" && v["digest"] == digest {
			valid++
		} else if v["reference"] == nil && v["digest"] == nil {
			redacted++
		} else {
			t.Fatal("unsafe version reference", v)
		}
	}
	if valid != 1 || redacted != 2 {
		t.Fatal("version candidates", versions)
	}
	out = mcpOutput(t, f.call(t, f.readonly, "run_get", map[string]any{"runId": rn.ID}))
	blob, _ = json.Marshal(out)
	if strings.Contains(string(blob), "SECRET") || out["status"] != "partial_failed" {
		t.Fatal("run output", string(blob))
	}
}

type mcpBearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}

func TestMCPOfficialSDKClient(t *testing.T) {
	f := newMCPFixture(t)
	srv := httptest.NewServer(f.handler)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerTransport{http.DefaultTransport, f.readonly}}}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal("official SDK connect", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 6 {
		t.Fatal("official SDK discovery", listed, err)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "projects_list", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatal("official SDK call", result, err)
	}
	if err := f.tokens.Revoke(ctx, f.readonlyItem.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "projects_list", Arguments: map[string]any{}}); err == nil {
		t.Fatal("SDK cached revoked token")
	}
}

func TestMCPTokenAdminContractCookieCSRFAndAudit(t *testing.T) {
	f := newMCPFixture(t)
	authn := auth.NewService(f.db, nil)
	session, err := auth.NewSessionStore(f.db).Create()
	if err != nil {
		t.Fatal(err)
	}
	handlers := NewMCPTokenHandlers(f.tokens, f.audit)
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler { return requireAuth(authn, next) })
		r.Get("/api/settings/mcp/tokens", handlers.List)
		r.Post("/api/settings/mcp/tokens", handlers.Create)
		r.Delete("/api/settings/mcp/tokens/{id}", handlers.Revoke)
	})
	do := func(method, path, body string, cookie, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+f.trigger)
		r.Header.Set("Content-Type", "application/json")
		if cookie {
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: session.Token})
		}
		if csrf {
			r.Header.Set(headerCsrf, session.CSRFToken)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/api/settings/mcp/tokens", "", false, false); w.Code != 401 {
		t.Fatal("MCP token used for admin", w.Code)
	}
	body := `{"name":"issued","projectIds":["` + f.project + `"]}`
	if w := do("POST", "/api/settings/mcp/tokens", body, true, false); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	w := do("POST", "/api/settings/mcp/tokens", body, true, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var issued struct {
		Token string        `json:"token"`
		Item  auth.MCPToken `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.Item.CanTrigger || issued.Item.RevokedAt != nil {
		t.Fatal("issuance contract", issued)
	}
	w = do("GET", "/api/settings/mcp/tokens", "", true, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), issued.Token) || !strings.Contains(w.Body.String(), `"tokens":[`) {
		t.Fatal("list contract", w.Body.String())
	}
	w = do("DELETE", "/api/settings/mcp/tokens/"+issued.Item.ID, "", true, true)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal("revoke contract", w.Code, w.Body.String())
	}
	if _, err := f.tokens.Verify(context.Background(), issued.Token); err == nil {
		t.Fatal("revoke not persisted")
	}
	for _, body := range []string{`{"name":"bad","projectIds":[],"expiresInDays":7}`, `{"name":"bad","projectIds":["` + f.project + `"],"expiresInDays":0}`, `{"name":"bad","projectIds":["` + f.project + `"],"expiresInDays":31}`} {
		if w := do("POST", "/api/settings/mcp/tokens", body, true, true); w.Code != 422 {
			t.Fatal("admin validation", w.Code, w.Body.String())
		}
	}
	if w := do("POST", "/api/settings/mcp/tokens", body+` {}`, true, true); w.Code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	for _, action := range []string{"mcp_token.create", "mcp_token.revoke"} {
		result, err := f.audit.List(context.Background(), audit.ListFilter{Action: action})
		if err != nil || len(result.Entries) != 1 {
			t.Fatal("audit action", action, err)
		}
		blob, _ := json.Marshal(result)
		if strings.Contains(string(blob), issued.Token) {
			t.Fatal("token audit secret")
		}
	}
	// Body cap applies before issuing a token, even with a valid JSON prefix.
	if w := do("POST", "/api/settings/mcp/tokens", body+strings.Repeat(" ", 16384), true, true); w.Code != 413 {
		t.Fatal("admin body cap", w.Code)
	}
}

type mcpBlockingRuns struct {
	run.Service
	started   chan struct{}
	release   chan struct{}
	projectID string
}

func (b mcpBlockingRuns) Get(ctx context.Context, id string) (*run.Run, error) {
	b.started <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return &run.Run{ID: id, ProjectID: b.projectID, Status: run.StatusSuccess}, nil
	}
}

func TestMCPConcurrentRequestCap(t *testing.T) {
	f := newMCPFixture(t)
	blocking := mcpBlockingRuns{Service: f.runs, started: make(chan struct{}, 32), release: make(chan struct{}), projectID: f.project}
	h, err := NewMCPHandler(f.tokens, pmcp.Dependencies{Runs: blocking}, MCPHTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.handler = h
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.rpc(f.readonly, "tools/call", map[string]any{"name": "run_get", "arguments": map[string]string{"runId": "fixture"}})
		}()
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for i := 0; i < 32; i++ {
		select {
		case <-blocking.started:
		case <-timer.C:
			close(blocking.release)
			wg.Wait()
			t.Fatal("requests did not start")
		}
	}
	w := f.rpc(f.readonly, "tools/list", map[string]any{})
	close(blocking.release)
	wg.Wait()
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("unbounded active requests", w.Code, w.Body.String())
	}
}

func TestMCPWaitExpiresAndObservesTerminalState(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()
	rn, err := f.runs.Create(ctx, f.project, run.Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan map[string]any, 1)
	go func() { done <- f.call(t, f.readonly, "run_wait", map[string]any{"runId": rn.ID, "waitSeconds": 30}) }()
	time.Sleep(20 * time.Millisecond)
	if _, err := f.db.Exec(`UPDATE pipeline_runs SET status='success' WHERE id=?`, rn.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if out := mcpOutput(t, result); out["status"] != "success" {
			t.Fatal(out)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal state not observed")
	}
	if _, err := f.db.Exec(`UPDATE pipeline_runs SET status='running' WHERE id=?`, rn.ID); err != nil {
		t.Fatal(err)
	}
	go func() { done <- f.call(t, f.readonly, "run_wait", map[string]any{"runId": rn.ID, "waitSeconds": 30}) }()
	time.Sleep(20 * time.Millisecond)
	if _, err := f.db.Exec(`UPDATE mcp_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), f.readonlyItem.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		expectMCPError(t, result, "unauthorized")
	case <-time.After(time.Second):
		t.Fatal("wait kept expired permission")
	}
}
