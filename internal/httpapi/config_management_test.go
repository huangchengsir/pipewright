package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configdraft"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/mask"
	pmcp "github.com/huangchengsir/pipewright/internal/mcp"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type configSessionAuth struct {
	auth.Authenticator
	store *auth.SessionStore
}

func (a configSessionAuth) Verify(token string) (*auth.Session, error) { return a.store.Get(token) }

func configHTTPFixture(t *testing.T) (*mcpFixture, *configdraft.Service, http.Handler, *auth.Session, string) {
	t.Helper()
	f := newMCPFixture(t)
	ctx := context.Background()
	if err := configstate.EnsureProject(ctx, f.db, f.db, f.project); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	raw := `{"stages":[{"id":"source","name":"Source","kind":"source","jobs":[{"id":"git","name":"Git","type":"git_source","config":{}}]},{"id":"test","name":"Tests","kind":"custom","needs":["source"],"jobs":[{"id":"script","name":"Test","type":"script","config":{"image":"alpine:3","commands":"echo private-script","timeoutSeconds":60}}]}]}`
	if _, err := f.db.Exec(`INSERT INTO pipeline_configs(project_id,spec_json,spec_yaml,status,created_at,updated_at,saved_at) VALUES (?,?,'','saved',?,?,?)`, f.project, raw, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO pipeline_settings(project_id,build_json,environments_json,steps_json,created_at,updated_at) VALUES (?,'{"model":"dockerfile","artifactType":"image","vars":[],"cache":{"enabled":false}}','[]','[]',?,?)`, f.project, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	token, _, err := f.tokens.Create(ctx, auth.MCPTokenInput{Name: "config", ProjectIDs: []string{f.project}, ConfigGrants: []auth.MCPConfigGrant{{ProjectID: f.project, Read: true, Propose: true, Apply: true, Sections: configdraft.Sections, Operations: configdraft.OperationTypes}}})
	if err != nil {
		t.Fatal(err)
	}
	svc := configdraft.New(f.db, &configdraft.Options{Runtime: configdraft.RuntimeInfo{RunnerMode: "dag", ExecutorKind: "local_docker", Available: true}, ApplyEnabled: true, PrepareSecrets: func(context.Context) (func(string) string, error) { return func(v string) string { return v }, nil }, AuthorizeAdmin: ConfigAdminAuthorizer(f.db), RevalidateApprover: ConfigApproverVerifier(f.db)})
	f.handler, err = NewMCPHandler(f.tokens, pmcp.Dependencies{Configs: svc, Masker: mask.NewMasker()}, MCPHTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ss := auth.NewSessionStore(f.db)
	sess, err := ss.Create()
	if err != nil {
		t.Fatal(err)
	}
	admin := New(testWebFSAuth(), configSessionAuth{store: ss}, WithProjects(project.New(f.db, nil, nil)), WithConfigManagement(f.db, svc, true))
	return f, svc, admin, sess, token
}

func configAdminRequest(handler http.Handler, sess *auth.Session, method, path, body, revision string, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if sess != nil {
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: sess.Token})
		if csrf {
			r.Header.Set(headerCsrf, sess.CSRFToken)
		}
	}
	if revision != "" {
		r.Header.Set("If-Match", revision)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestConfigurationMCPBrowserApprovalApplyAndReplay(t *testing.T) {
	f, _, admin, sess, token := configHTTPFixture(t)
	var tools struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(f.rpc(token, "tools/list", map[string]any{}).Body.Bytes(), &tools); err != nil || len(tools.Result.Tools) != 14 {
		t.Fatalf("14 tools required: %v / %d", err, len(tools.Result.Tools))
	}
	expectMCPError(t, f.call(t, f.readonly, "project_config_get", map[string]any{"projectId": f.project, "section": "project", "beginSnapshot": true}), "permission_denied")
	snap := mcpOutput(t, f.call(t, token, "project_config_get", map[string]any{"projectId": f.project, "section": "project", "beginSnapshot": true}))
	draft := mcpOutput(t, f.call(t, token, "config_draft_create", map[string]any{"projectId": f.project, "baseRevision": snap["baseRevision"], "policyVersion": snap["policyVersion"], "requestId": "integration-draft-1", "operations": []any{map[string]any{"type": "project.set_name", "value": "Reviewed project"}}}))
	if strings.Contains(fmt.Sprint(draft), "private-script") {
		t.Fatal("raw script exposed")
	}
	url := "/api/mcp/config-drafts/" + draft["draftId"].(string)
	if w := configAdminRequest(admin, nil, "GET", url+"/review", "", "", false); w.Code != 401 {
		t.Fatal("anonymous review", w.Code)
	}
	if w := configAdminRequest(admin, sess, "GET", url+"/review", "", "", false); w.Code != 200 {
		t.Fatalf("review %d %s", w.Code, w.Body)
	}
	body, _ := json.Marshal(map[string]any{"baseRevision": draft["baseRevision"], "policyVersion": draft["policyVersion"], "reviewDigest": draft["reviewDigest"]})
	if w := configAdminRequest(admin, sess, "POST", url+"/approve", string(body), "", false); w.Code != 403 {
		t.Fatal("CSRF approval bypass", w.Code)
	}
	w := configAdminRequest(admin, sess, "POST", url+"/approve", string(body), "", true)
	if w.Code != 200 {
		t.Fatalf("approve %d %s", w.Code, w.Body)
	}
	var approval configdraft.Approval
	if err := json.Unmarshal(w.Body.Bytes(), &approval); err != nil || approval.ID == "" {
		t.Fatal("approval missing", err)
	}
	args := map[string]any{"draftId": draft["draftId"], "approvalId": approval.ID, "requestId": "integration-apply-1"}
	first := mcpOutput(t, f.call(t, token, "config_apply", args))
	if first["state"] != "applied" {
		t.Fatalf("apply %v", first)
	}
	second := mcpOutput(t, f.call(t, token, "config_apply", args))
	if second["operationId"] != first["operationId"] {
		t.Fatal("replay created operation")
	}
	lookup := mcpOutput(t, f.call(t, token, "config_operation_get", map[string]any{"projectId": f.project, "requestId": "integration-apply-1"}))
	if lookup["state"] != "applied" {
		t.Fatal(lookup)
	}
	var name string
	var rev int
	_ = f.db.QueryRow(`SELECT name FROM projects WHERE id=?`, f.project).Scan(&name)
	_ = f.db.QueryRow(`SELECT revision FROM project_config_versions WHERE project_id=?`, f.project).Scan(&rev)
	if name != "Reviewed project" || rev != 2 {
		t.Fatalf("not exactly once: %q %d", name, rev)
	}
	var approver string
	_ = f.db.QueryRow(`SELECT approver FROM mcp_config_approvals WHERE id=?`, approval.ID).Scan(&approver)
	if approver != auth.SessionID(sess.Token) || approver == sess.Token {
		t.Fatal("session secret persisted")
	}
}

func TestConfigurationOldHTTPWritesRejectStaleMissingAndActiveReaders(t *testing.T) {
	f, _, admin, sess, _ := configHTTPFixture(t)
	path := "/api/projects/" + f.project
	for _, tc := range []struct {
		rev  string
		want int
	}{{"", 428}, {"cfg-99", 409}, {"garbage", 400}} {
		w := configAdminRequest(admin, sess, "PATCH", path, `{"name":"unsafe"}`, tc.rev, true)
		if w.Code != tc.want {
			t.Fatalf("%q status %d body %s", tc.rev, w.Code, w.Body)
		}
	}
	w := configAdminRequest(admin, sess, "PATCH", path, `{"name":"safe"}`, "cfg-1", true)
	if w.Code != 200 || w.Header().Get("X-Config-Revision") != "cfg-2" {
		t.Fatalf("CAS save %d %v %s", w.Code, w.Header(), w.Body)
	}
	var saved map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved["configRevision"] != "cfg-2" {
		t.Fatalf("body/header versions differ: %v", saved)
	}
	ctx := context.Background()
	err := configstate.WithWriteTx(ctx, f.db, func(q configstate.DBTX) error {
		_, err := configstate.AcquireActivityTx(ctx, q, f.db, configstate.Activity{ID: "held-reader", ProjectID: f.project, OwnerEpoch: "test-owner", Kind: "test", Phase: "executing"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	w = configAdminRequest(admin, sess, "PATCH", path, `{"name":"blocked"}`, "cfg-2", true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "active_runs") {
		t.Fatalf("reader guard %d %s", w.Code, w.Body)
	}
}

func TestConfigRevisionReadDoesNotAttachNewVersionToOldPayload(t *testing.T) {
	f, _, _, _, _ := configHTTPFixture(t)
	count := 0
	h := configRevisionMiddleware(f.db, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			_ = configstate.MutateProject(r.Context(), f.db, f.project, func(ctx context.Context, q configstate.DBTX) error {
				_, err := q.ExecContext(ctx, `UPDATE projects SET name='fresh' WHERE id=?`, f.project)
				return err
			})
		}
		writeJSON(w, 200, map[string]int{"read": count})
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/projects/"+f.project+"/pipeline", nil))
	if count != 2 || w.Header().Get("X-Config-Revision") != "cfg-2" || !strings.Contains(w.Body.String(), `"read":2`) {
		t.Fatalf("stale body rebaselined: count=%d %v %s", count, w.Header(), w.Body)
	}
}

func TestConfigPartialSavePreflightFailureKeepsCommittedRevision(t *testing.T) {
	f, _, _, _, _ := configHTTPFixture(t)
	h := configRevisionMiddleware(f.db, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := configstate.MutateProject(r.Context(), f.db, f.project, func(ctx context.Context, q configstate.DBTX) error {
			_, err := q.ExecContext(ctx, `UPDATE projects SET name='first block saved' WHERE id=?`, f.project)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// A later block rejects input before it enters a mutation transaction.
		writeError(w, 422, "invalid_build", "Invalid build settings")
	}))
	r := httptest.NewRequest("POST", "/api/projects/"+f.project+"/pipeline/ai-apply", nil)
	r.Header.Set("If-Match", "cfg-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 422 || w.Header().Get("X-Config-Partial-Save") != "true" || w.Header().Get("X-Config-Revision") != "cfg-2" {
		t.Fatalf("partial save hidden: %d %v %s", w.Code, w.Header(), w.Body)
	}
	var name string
	_ = f.db.QueryRow(`SELECT name FROM projects WHERE id=?`, f.project).Scan(&name)
	if name != "first block saved" {
		t.Fatal("test did not commit its first block")
	}
}

func TestConfigurationApproverSessionRevocationStopsApply(t *testing.T) {
	f, svc, admin, sess, token := configHTTPFixture(t)
	tok, err := f.tokens.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := svc.Snapshot(context.Background(), tok.ID, configdraft.SnapshotRequest{ProjectID: f.project, Section: "project", BeginSnapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	value := "must not save"
	d, err := svc.Create(context.Background(), tok.ID, configdraft.CreateRequest{ProjectID: f.project, BaseRevision: snap.BaseRevision, PolicyVersion: snap.PolicyVersion, RequestID: "revoke-draft", Operations: []configdraft.Operation{{Type: "project.set_name", Value: &value}}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(configdraft.ApprovalRequest{BaseRevision: d.BaseRevision, PolicyVersion: d.PolicyVersion, ReviewDigest: d.ReviewDigest})
	w := configAdminRequest(admin, sess, "POST", "/api/mcp/config-drafts/"+d.DraftID+"/approve", string(body), "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var a configdraft.Approval
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if _, err := f.db.Exec(`DELETE FROM sessions WHERE token=?`, sess.Token); err != nil {
		t.Fatal(err)
	}
	result := mcpOutput(t, f.call(t, token, "config_apply", map[string]any{"draftId": d.DraftID, "approvalId": a.ID, "requestId": "revoke-apply"}))
	if result["state"] != "rejected" || result["reason"] != "approval_invalid" {
		t.Fatal(result)
	}
	var rev int
	_ = f.db.QueryRow(`SELECT revision FROM project_config_versions WHERE project_id=?`, f.project).Scan(&rev)
	if rev != 1 {
		t.Fatal("revoked approver changed configuration")
	}
}

func TestConfigurationInvalidRawUTF8NeverReachesDecoder(t *testing.T) {
	f, _, _, _, token := configHTTPFixture(t)
	raw := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"config_draft_create","arguments":{"projectId":%q,"value":"`, f.project) + string([]byte{0xff}) + `"}}}`
	r := httptest.NewRequest("POST", "http://localhost/mcp", strings.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("invalid UTF8 accepted: %d %s", w.Code, w.Body)
	}
	var count int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM mcp_config_drafts`).Scan(&count)
	if count != 0 {
		t.Fatal("invalid bytes created draft")
	}
}

func TestConfigurationMaintenanceRequiresSessionCSRFAndRealExitEvidence(t *testing.T) {
	f, _, admin, sess, _ := configHTTPFixture(t)
	path := "/api/mcp/config-maintenance"
	if w := configAdminRequest(admin, nil, "GET", path, "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	ctx := context.Background()
	err := configstate.WithWriteTx(ctx, f.db, func(q configstate.DBTX) error {
		_, err := configstate.AcquireActivityTx(ctx, q, f.db, configstate.Activity{ID: "legacy-held", ProjectID: f.project, OwnerEpoch: "legacy-unreconciled", Kind: "run", Phase: "cleanup_unknown"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	w := configAdminRequest(admin, sess, "GET", path, "", "", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var state configstate.MaintenanceSnapshot
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Items) != 1 || state.Items[0].Recoverable || state.Items[0].Reason != "evidence_unavailable" {
		t.Fatal("legacy hold offered forced recovery", w.Body)
	}
	body, _ := json.Marshal(configstate.MaintenanceRecoverRequest{ActivityID: "legacy-held", ExpectedDigest: state.Items[0].Digest, EvidenceID: "client-confirmation"})
	if w := configAdminRequest(admin, sess, "POST", path+"/recover", string(body), "", false); w.Code != 403 {
		t.Fatal("maintenance CSRF bypass", w.Code)
	}
	w = configAdminRequest(admin, sess, "POST", path+"/recover", string(body), "", true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "evidence_unavailable") {
		t.Fatal("client assertion released hold", w.Code, w.Body)
	}
	var active int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE activity_id='legacy-held' AND released_at IS NULL`).Scan(&active)
	if active != 1 {
		t.Fatal("unknown hold released")
	}
}

func TestConfigurationPendingOperationHasAuthenticatedManualFence(t *testing.T) {
	f, svc, admin, sess, token := configHTTPFixture(t)
	ctx := context.Background()
	tok, err := f.tokens.Verify(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := svc.Snapshot(ctx, tok.ID, configdraft.SnapshotRequest{ProjectID: f.project, Section: "project", BeginSnapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	value := "candidate"
	d, err := svc.Create(ctx, tok.ID, configdraft.CreateRequest{ProjectID: f.project, BaseRevision: snap.BaseRevision, PolicyVersion: snap.PolicyVersion, RequestID: uuid.NewString(), Operations: []configdraft.Operation{{Type: "project.set_name", Value: &value}}})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO mcp_config_operations(id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,state,reason,before_revision,result_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'old-owner','pending','',1,'{}',0,0)`, id, tok.ID, f.project, uuid.NewString(), "fixture-fingerprint", d.DraftID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/mcp/config-operations/" + id
	if w := configAdminRequest(admin, nil, "GET", path, "", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := configAdminRequest(admin, sess, "GET", path, "", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"pending"`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := configAdminRequest(admin, sess, "POST", path+"/fence", `{}`, "", false); w.Code != 403 {
		t.Fatal("fence bypassed CSRF", w.Code)
	}
	w = configAdminRequest(admin, sess, "POST", path+"/fence", `{}`, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"failed"`) || !strings.Contains(w.Body.String(), "interrupted_not_applied") {
		t.Fatal("fence did not return primary result", w.Code, w.Body)
	}
	var revision int
	_ = f.db.QueryRow(`SELECT revision FROM project_config_versions WHERE project_id=?`, f.project).Scan(&revision)
	if revision != 1 {
		t.Fatal("recovery modified configuration")
	}
}

func TestConfigurationOfficialSDKDiscoversScopedToolsAndHonorsRevocation(t *testing.T) {
	f, _, _, _, token := configHTTPFixture(t)
	srv := httptest.NewServer(f.handler)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "configuration-acceptance", Version: "1"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerTransport{http.DefaultTransport, token}}}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 14 {
		t.Fatal("SDK configuration discovery", listed, err)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "project_config_get", Arguments: map[string]any{"projectId": f.project, "section": "project", "beginSnapshot": true}})
	if err != nil || result.IsError {
		t.Fatal("SDK configuration snapshot", result, err)
	}
	blob, _ := json.Marshal(result)
	if strings.Contains(string(blob), "private-script") {
		t.Fatal("SDK configuration result exposed script")
	}
	tok, err := f.tokens.Verify(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tokens.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "capabilities_get", Arguments: map[string]any{}}); err == nil {
		t.Fatal("SDK retained revoked credentials")
	}
}

func TestConfigurationFirstRunInitializesActualServicesBeforeAdmission(t *testing.T) {
	f := newMCPFixture(t)
	pipes := pipeline.New(f.db)
	settings := pipeline.NewSettingsService(f.db, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	svc := run.New(f.db, run.WithConfigInitializer(func(ctx context.Context, id string) error {
		for _, service := range []any{pipes, settings} {
			initializer, ok := service.(interface {
				EnsureStoredDefault(context.Context, string) error
			})
			if !ok {
				t.Fatal("missing real initializer")
			}
			if err := initializer.EnsureStoredDefault(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}))
	r, err := svc.Create(ctx, f.project, run.Trigger{})
	if err != nil || r.Status != run.StatusQueued {
		t.Fatal("first run admission failed", r, err)
	}
	for _, table := range []string{"pipeline_configs", "pipeline_settings"} {
		var count int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE project_id=?`, f.project).Scan(&count); err != nil || count != 1 {
			t.Fatal("default not atomically initialized", table, count, err)
		}
	}
	var rev int
	_ = f.db.QueryRow(`SELECT revision FROM project_config_versions WHERE project_id=?`, f.project).Scan(&rev)
	if rev != 3 {
		t.Fatal("default initialization revisions", rev)
	}
	name := "blocked while queued"
	projects := project.New(f.db, nil, nil)
	if _, err := projects.Update(ctx, f.project, project.UpdateInput{Name: &name}); !errors.Is(err, configstate.ErrBusy) {
		t.Fatal("new admission did not protect configuration", err)
	}
	if _, err := svc.Cancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Update(ctx, f.project, project.UpdateInput{Name: &name}); err != nil {
		t.Fatal("queued cancellation retained hold", err)
	}
}
