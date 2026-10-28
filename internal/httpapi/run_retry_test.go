package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestRunRetryAuthenticatedProtocolAndSafeViews(t *testing.T) {
	st := testStoreAuth(t)
	ctx := context.Background()
	authn := auth.NewService(st.DB, nil)
	if e := authn.Bootstrap("operator", "testpass"); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('retry-cred','test','git_token','',X'00','m','old','old')`, `INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('retry-project','test','https://example.invalid/p','main','retry-cred','old','old')`} {
		if _, e := st.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := st.DB.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('retry-root','retry-project','failed',?)", now); e != nil {
		t.Fatal(e)
	}
	var dispatch atomic.Int32
	retry := runretry.New(st.DB, vault.New(st.DB, testMasterKey()), runretry.WithChildCreator(func(ctx context.Context, q configstate.DBTX, in runretry.ChildInput) error {
		_, e := q.ExecContext(ctx, "INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,?,'queued',?)", in.ID, in.ProjectID, in.CreatedAt.Format(time.RFC3339))
		return e
	}), runretry.WithDispatch(func(context.Context, string) error { dispatch.Add(1); return nil }))
	snap := runretry.Snapshot{SchemaVersion: 1, ProjectID: "retry-project", RequestedRef: "main", CommitSHA: strings.Repeat("a", 40), Trigger: runretry.Trigger{Type: "manual", Actor: "frozen-actor"}, Settings: map[string]any{"private": "HTTP_SNAPSHOT_SECRET"}, Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "s", Name: "Build", Jobs: []pipeline.Job{{ID: "a", Name: "A", Type: "script", Config: map[string]any{"private": "HTTP_CONFIG_SECRET"}}, {ID: "b", Name: "B", Type: "script", Needs: []string{"a"}}}}}}}
	if e := retry.SaveManifest(ctx, "retry-root", snap); e != nil {
		t.Fatal(e)
	}
	execution, e := retry.Prepare(ctx, "retry-root")
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := runretry.ExecutionFrom(execution)
	a, _ := ex.Node("s", "a")
	b, _ := ex.Node("s", "b")
	if e = ex.Finish(ctx, a, runretry.Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "none", Outputs: runretry.Inputs{Env: map[string]string{"SECRET": "HTTP_DYNAMIC_SECRET"}}}); e != nil {
		t.Fatal(e)
	}
	if e = ex.Finish(ctx, b, runretry.Outcome{Status: "failed", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "none"}); e != nil {
		t.Fatal(e)
	}
	rec := audit.New(st.DB, mask.NewMasker(), nil)
	srv := httptest.NewServer(New(testWebFS(), authn, WithRuns(run.New(st.DB), nil), WithRunRetry(retry), WithAudit(rec)))
	defer srv.Close()
	body := func(in any) string { raw, _ := json.Marshal(in); return string(raw) }
	request := func(c *http.Client, method, path, csrf, data string) (int, string) {
		t.Helper()
		response := doJSON(t, c, method, srv.URL+path, csrf, data)
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return response.StatusCode, string(raw)
	}
	pth := "/api/runs/retry-root"
	code, _ := request(http.DefaultClient, "GET", pth+"/retry-context", "", "")
	if code != 401 {
		t.Fatal(code)
	}
	client := newTestClient(t)
	// Authenticate through HTTP rather than injecting a context principal.
	code, raw := request(client, "POST", "/api/auth/login", "", `{"username":"operator","password":"testpass"}`)
	if code != 200 {
		t.Fatal(code, raw)
	}
	serverURL, _ := url.Parse(srv.URL)
	csrf := ""
	for _, c := range client.Jar.Cookies(serverURL) {
		if c.Name == cookieCsrf {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("missing real csrf")
	}
	code, raw = request(client, "GET", pth+"/retry-plan?nodeId="+b.Key, "", "")
	var plan runretry.Plan
	if code != 200 || json.Unmarshal([]byte(raw), &plan) != nil || !plan.CanSubmit {
		t.Fatal(code, raw)
	}
	input := runretry.SubmitInput{RequestID: "http-retry-one", BaseAttemptID: plan.BaseAttemptID, BaseRevision: plan.BaseRevision, PlanFingerprint: plan.PlanFingerprint, TargetNodeIDs: []string{b.Key}}
	t.Run("large_readonly_plan_transports_and_envelope_limits", func(t *testing.T) {
		const largeID = "retry-large"
		if _, err := st.DB.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,'retry-project','failed',?)", largeID, now); err != nil {
			t.Fatal(err)
		}
		large := snap
		jobs := make([]pipeline.Job, 1000)
		for i := range jobs {
			jobs[i] = pipeline.Job{ID: fmt.Sprintf("job-%d", i), Name: fmt.Sprintf("Job %d", i), Type: "script"}
		}
		large.Spec = pipeline.Spec{Stages: []pipeline.Stage{{ID: "large", Name: "Large frozen stage", Jobs: jobs}}}
		if err := retry.SaveManifest(ctx, largeID, large); err != nil {
			t.Fatal(err)
		}
		prepared, err := retry.Prepare(ctx, largeID)
		if err != nil {
			t.Fatal(err)
		}
		execution, _ := runretry.ExecutionFrom(prepared)
		keys := make([]string, 0, len(jobs))
		query := url.Values{}
		for _, job := range jobs {
			node, ok := execution.Node("large", job.ID)
			if !ok {
				t.Fatal(job.ID)
			}
			if err = execution.Finish(ctx, node, runretry.Outcome{Status: "failed", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "none"}); err != nil {
				t.Fatal(err)
			}
			keys = append(keys, node.Key)
			query.Add("nodeId", node.Key)
		}
		payload := body(map[string]any{"targetNodeIds": keys, "rebuildNodeIds": []string{}})
		if len(payload) <= 65536 {
			t.Fatal("capacity fixture too small", len(payload))
		}
		counts := func() map[string]int {
			values := map[string]int{}
			for _, table := range []string{"run_retry_attempts", "run_retry_requests", "run_retry_dispatch"} {
				var n int
				if err := st.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
					t.Fatal(err)
				}
				values[table] = n
			}
			return values
		}
		before := counts()
		endpoint := "/api/runs/" + largeID + "/retry-plan"
		code, _ := request(http.DefaultClient, "POST", endpoint, "", payload)
		if code != 401 {
			t.Fatal(code)
		}
		code, _ = request(client, "POST", endpoint, "", payload)
		if code != 403 {
			t.Fatal(code)
		}
		code, raw := request(client, "GET", endpoint+"?"+query.Encode(), "", "")
		var getPlan runretry.Plan
		if code != 200 || json.Unmarshal([]byte(raw), &getPlan) != nil || !getPlan.CanSubmit {
			t.Fatal(code, raw)
		}
		code, raw = request(client, "POST", endpoint, csrf, payload)
		var postPlan runretry.Plan
		if code != 200 || json.Unmarshal([]byte(raw), &postPlan) != nil || !postPlan.CanSubmit || postPlan.PlanFingerprint != getPlan.PlanFingerprint || len(postPlan.TargetNodeIDs) != 1000 {
			t.Fatal("GET/POST plan mismatch", code, raw)
		}
		for _, invalid := range []string{strings.TrimSuffix(payload, "}") + `,"scope":"forged"}`, payload + `{}`, strings.Repeat(" ", maxRunRetryBody) + `{}`} {
			code, _ = request(client, "POST", endpoint, csrf, invalid)
			if code != 400 {
				t.Fatal("invalid plan envelope", code)
			}
		}
		code, _ = request(client, "POST", pth+"/retry", csrf, strings.Repeat(" ", maxRunRetryBody)+`{}`)
		if code != 400 {
			t.Fatal("oversize submit", code)
		}
		after := counts()
		for table, n := range before {
			if after[table] != n {
				t.Fatal("plan wrote state", table, n, after[table])
			}
		}
		var submittedAudit int
		if err := st.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='run.retry'").Scan(&submittedAudit); err != nil || submittedAudit != 0 || dispatch.Load() != 0 {
			t.Fatal("plan dispatched or audited submission", submittedAudit, dispatch.Load(), err)
		}
	})
	code, _ = request(client, "POST", pth+"/retry", "", body(input))
	if code != 403 {
		t.Fatal(code)
	}
	forged := strings.TrimSuffix(body(input), "}") + `,"initiator":"forged","scope":"forged"}`
	code, _ = request(client, "POST", pth+"/retry", csrf, forged)
	if code != 400 {
		t.Fatal("client identity accepted", code)
	}
	for _, suffix := range []string{"/retry-context", "/retry-plan?nodeId=" + b.Key, "/attempts?page=1&pageSize=20"} {
		code, raw = request(client, "GET", pth+suffix, "", "")
		if code != 200 {
			t.Fatal(code, raw)
		}
		for _, secret := range []string{"HTTP_SNAPSHOT_SECRET", "HTTP_CONFIG_SECRET", "HTTP_DYNAMIC_SECRET", "ciphertext", "\"outputs\"", "\"resources\"", "\"settings\""} {
			if strings.Contains(raw, secret) {
				t.Fatalf("public view leaks %q: %s", secret, raw)
			}
		}
	}
	code, raw = request(client, "POST", pth+"/retry", csrf, body(input))
	var accepted runretry.Request
	if code != 202 || json.Unmarshal([]byte(raw), &accepted) != nil || accepted.AttemptRunID == "" || accepted.Initiator != "operator" {
		t.Fatal(code, raw)
	}
	code, raw = request(client, "POST", pth+"/retry", csrf, body(input))
	var duplicate runretry.Request
	json.Unmarshal([]byte(raw), &duplicate)
	if code != 202 || duplicate.AttemptRunID != accepted.AttemptRunID || dispatch.Load() != 1 {
		t.Fatal("duplicate dispatch", code, raw, dispatch.Load())
	}
	input.RequestID = "stale-new-request"
	code, _ = request(client, "POST", pth+"/retry", csrf, body(input))
	if code != 409 {
		t.Fatal("stale head", code)
	}
	code, raw = request(client, "GET", pth+"/retry-requests/http-retry-one", "", "")
	if code != 200 || !strings.Contains(raw, accepted.AttemptRunID) {
		t.Fatal(code, raw)
	}
	// New authenticated session, same stable principal: the original request is
	// still queryable; no client-supplied session identity is required.
	code, raw = request(client, "POST", "/api/auth/login", "", `{"username":"operator","password":"testpass"}`)
	if code != 200 {
		t.Fatal(code, raw)
	}
	for _, cookie := range client.Jar.Cookies(serverURL) {
		if cookie.Name == cookieCsrf {
			csrf = cookie.Value
		}
	}
	code, raw = request(client, "GET", pth+"/retry-requests/http-retry-one", "", "")
	if code != 200 || !strings.Contains(raw, accepted.AttemptRunID) {
		t.Fatal(code, raw)
	}
	var scope, initiator string
	if e = st.DB.QueryRow("SELECT caller_scope,initiator FROM run_retry_requests WHERE request_id='http-retry-one'").Scan(&scope, &initiator); e != nil || scope != "admin" || initiator != "operator" {
		t.Fatal(scope, initiator, e)
	}
	code, _ = request(client, "POST", pth+"/retry-reconcile", "", `{}`)
	if code != 403 {
		t.Fatal(code)
	}
	code, _ = request(client, "POST", pth+"/retry-reconcile", csrf, `{"drained":true}`)
	if code != 400 {
		t.Fatal("browser proof accepted", code)
	}
	code, raw = request(client, "POST", pth+"/retry-reconcile", csrf, `{}`)
	if code != 200 {
		t.Fatal(code, raw)
	}
	code, _ = request(client, "GET", pth+"/attempts?pageSize=101", "", "")
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = request(client, "GET", "/api/runs/invisible/retry-context", "", "")
	if code != 404 {
		t.Fatal(code)
	}
	var detail string
	if e = st.DB.QueryRow("SELECT detail_json FROM audit_log WHERE action='run.retry' LIMIT 1").Scan(&detail); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(detail, "SECRET") || strings.Contains(detail, "scope") || strings.Contains(detail, "cookie") {
		t.Fatal("unsafe audit", detail)
	}
	// Retiring the source row must not erase an accepted request retained with
	// a surviving child. Use another real login to rule out session-token scope.
	if _, e = st.DB.Exec("DELETE FROM pipeline_runs WHERE id='retry-root'"); e != nil {
		t.Fatal(e)
	}
	code, raw = request(client, "POST", "/api/auth/login", "", `{"username":"operator","password":"testpass"}`)
	if code != 200 {
		t.Fatal(code, raw)
	}
	code, _ = request(client, "GET", pth, "", "")
	if code != 404 {
		t.Fatal("ordinary source detail should be retired", code)
	}
	code, raw = request(client, "GET", pth+"/retry-requests/http-retry-one", "", "")
	if code != 200 || !strings.Contains(raw, accepted.AttemptRunID) {
		t.Fatal("accepted request hidden by source retention", code, raw)
	}
	code, _ = request(http.DefaultClient, "GET", pth+"/retry-requests/http-retry-one", "", "")
	if code != 401 {
		t.Fatal("retained request exposed anonymously", code)
	}
	code, _ = request(client, "GET", pth+"/retry-requests/truly-unknown", "", "")
	if code != 404 {
		t.Fatal("unknown request", code)
	}
}
