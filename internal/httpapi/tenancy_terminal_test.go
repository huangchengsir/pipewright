package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/terminalhttp"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTenantTerminalHTTPRevokesBufferedOutputAndInput(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	credential, e := v.Create(vault.CreateInput{Name: "test SSH", Type: vault.TypeSSHKey, Secret: "fixture-password"})
	if e != nil {
		t.Fatal(e)
	}
	dialer := &permissionContainerDialer{}
	targets := target.New(db, v, dialer)
	server, e := targets.Create(ctx, target.CreateInput{Name: "dedicated fixture", Host: "127.0.0.1", User: "fixture", CredentialID: credential.ID})
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "administrator-terminal-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "administrator-terminal-password")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = targets.(target.ExecutionAssignmentService).AssignExecution(tenancy.WithActor(ctx, admin.Actor, team), server.ID, "SHA256:"+strings.Repeat("A", 43)); e != nil {
		t.Fatal(e)
	}
	grants := []authz.Grant{}
	for _, action := range []string{"server.inspect", "server.use", "credential.metadata", "credential.use", "server.terminal", "container.inspect"} {
		grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Terminal operator", Pages: []string{"page.servers", "page.vault", "page.containers"}, Grants: grants, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "operator", DisplayName: "Operator", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: server.ID}, {Kind: "credential", ID: credential.ID}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	employee, e := accounts.Login(ctx, "operator", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	employee, e = accounts.ChangePassword(ctx, employee.Token, initial.Password, "operator-terminal-password")
	if e != nil {
		t.Fatal(e)
	}
	model := &diagStubAI{diag: &ai.Diagnosis{Status: "ready", Hypothesis: "s3cr3t"}}
	manager := terminalhttp.New()
	t.Cleanup(manager.Close)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithVault(v), WithServers(targets), WithTerminalHTTP(manager), WithAISettings(model))
	request := func(method, path, body, selectedTeam string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", selectedTeam)
		r.Header.Set(headerCsrf, employee.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: employee.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: employee.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, principal := range []struct {
		actor tenancy.Actor
		want  bool
	}{{admin.Actor, true}, {employee.Actor, false}} {
		decisions, e := repo.Decisions(ctx, principal.actor, team, []tenancy.DecisionQuery{{Action: "server.assign", ResourceID: server.ID}, {Action: "server.assign", ResourceID: "missing"}, {Action: "server.assign", ResourceID: "instance"}})
		if e != nil || len(decisions.Allowed) != 3 || decisions.Allowed[0] != principal.want || decisions.Allowed[1] || decisions.Allowed[2] {
			t.Fatal("real assignment button decision", decisions, e)
		}
	}
	assignmentPath := "/api/servers/" + server.ID + "/execution-assignment"
	for _, method := range []string{"GET", "PUT"} {
		response := request(method, assignmentPath, `{"hostKey":""}`, team)
		if response.Code != 403 {
			t.Fatal("employee assigned node", method, response.Code)
		}
	}
	savedEmployee := employee
	employee = admin
	response := request("GET", assignmentPath, "", team)
	if response.Code != 200 || strings.Contains(response.Body.String(), "connectionFingerprint") {
		t.Fatal("assignment metadata", response.Code, response.Body.String())
	}
	response = request("PUT", assignmentPath, `{"hostKey":"SHA256:`+strings.Repeat("A", 43)+`","teamId":"forged"}`, team)
	if response.Code != 400 {
		t.Fatal("assignment unexpected field", response.Code)
	}
	csrfRequest := httptest.NewRequest("PUT", assignmentPath, strings.NewReader(`{"hostKey":""}`))
	csrfRequest.AddCookie(&http.Cookie{Name: cookieSession, Value: admin.Token})
	csrfRequest.Header.Set("X-Pipewright-Team", team)
	csrfResponse := httptest.NewRecorder()
	router.ServeHTTP(csrfResponse, csrfRequest)
	if csrfResponse.Code != 403 {
		t.Fatal("assignment missing csrf", csrfResponse.Code)
	}
	response = request("PUT", assignmentPath, `{"hostKey":""}`, team)
	if response.Code != 200 {
		t.Fatal("admin revoke", response.Code)
	}
	response = request("PUT", assignmentPath, `{"hostKey":"SHA256:`+strings.Repeat("A", 43)+`"}`, team)
	if response.Code != 200 {
		t.Fatal("admin assign", response.Code, response.Body.String())
	}
	if dialer.calls.Load() != 0 {
		t.Fatal("assignment dialed unverified host")
	}
	var assignmentAudit int
	if e = db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE target_id=? AND action IN ('execution_node_assign','execution_node_revoke')`, server.ID).Scan(&assignmentAudit); e != nil || assignmentAudit != 3 {
		t.Fatal("assignment audit count", assignmentAudit, e)
	}
	employee = savedEmployee
	base := "/api/servers/" + server.ID + "/terminal/sessions"
	r := request("POST", base, `{"requestId":"terminal-fixture-001","shell":"sh"}`, team)
	if r.Code != 200 {
		t.Fatal("open", r.Code, r.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if e = json.Unmarshal(r.Body.Bytes(), &result); e != nil || result.ID == "" {
		t.Fatal(e)
	}
	path := base + "/" + result.ID
	r = request("POST", path+"/input", `{"seq":1,"data":"c2FmZSBvdXRwdXQ="}`, team)
	if r.Code != 204 {
		t.Fatal("input", r.Code, r.Body.String())
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "other")
	if e != nil {
		t.Fatal(e)
	}
	r = request("GET", path+"?after=0", "", other.ID)
	if r.Code != 403 {
		t.Fatal("cross team", r.Code, r.Body.String())
	}
	// Container metadata is independently scoped; sensitive runtime values need
	// explicit reveal permission even when command execution is allowed.
	inspectPath := "/api/servers/" + server.ID + "/containers/app/inspect"
	r = request("GET", inspectPath, "", team)
	if r.Code != 200 || strings.Contains(r.Body.String(), "s3cr3t") || strings.Contains(r.Body.String(), "ops@example.com") {
		t.Fatal("container secret projection", r.Code, r.Body.String())
	}
	before := dialer.calls.Load()
	r = request("POST", "/api/servers/"+server.ID+"/service/action", `{"type":"docker","target":"app","action":"restart"}`, team)
	if r.Code != 403 || dialer.calls.Load() != before {
		t.Fatal("container inspect implied operation", r.Code)
	}
	t.Run("each-container-action-requires-operation-scope", func(t *testing.T) {
		outside, err := targets.Create(tenancy.WithActor(ctx, admin.Actor, team), target.CreateInput{
			Name: "unselected fixture", Host: "127.0.0.1", User: "fixture", CredentialID: credential.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range []string{"/containers", "/containers/stats", "/images", "/volumes", "/networks", "/networks/fixture/containers", "/stacks", "/stacks/fixture", "/system/df"} {
			t.Run("inspect"+endpoint, func(t *testing.T) {
				before := dialer.calls.Load()
				w := request(http.MethodGet, "/api/servers/"+server.ID+endpoint, "", team)
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), server.ID) || dialer.calls.Load() <= before {
					t.Fatalf("selected server read returned %d, body=%s", w.Code, w.Body.String())
				}
				before = dialer.calls.Load()
				for _, scope := range []struct{ id, team string }{{outside.ID, team}, {server.ID, other.ID}} {
					w := request(http.MethodGet, "/api/servers/"+scope.id+endpoint, "", scope.team)
					if (w.Code != http.StatusForbidden && w.Code != http.StatusNotFound) || strings.Contains(w.Body.String(), scope.id) || dialer.calls.Load() != before {
						t.Fatalf("out-of-scope server read returned %d or reached mock SSH", w.Code)
					}
				}
			})
		}
		t.Run("selected-server-metrics-and-aggregates", func(t *testing.T) {
			for _, route := range []string{"/api/servers/metrics", "/api/servers/containers"} {
				w := request("GET", route, "", team)
				var body struct {
					Items []struct {
						ServerID string `json:"serverId"`
					} `json:"items"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) != 1 || body.Items[0].ServerID != server.ID || strings.Contains(w.Body.String(), outside.ID) {
					t.Fatalf("unscoped or missing aggregate %s: %d %s", route, w.Code, w.Body.String())
				}
			}
			w := request("GET", "/api/servers/"+server.ID+"/metrics", "", team)
			if w.Code != 200 || !strings.Contains(w.Body.String(), server.ID) {
				t.Fatalf("selected metrics: %d %s", w.Code, w.Body.String())
			}
			before := dialer.calls.Load()
			for _, id := range []string{outside.ID, "missing"} {
				w = request("GET", "/api/servers/"+id+"/metrics", "", team)
				if (w.Code != 403 && w.Code != 404) || strings.Contains(w.Body.String(), id) || dialer.calls.Load() != before {
					t.Fatalf("foreign metrics touched SSH: %d %s", w.Code, w.Body.String())
				}
			}
		})
		t.Run("scoped-log-stream", func(t *testing.T) {
			stream := func(id, selected string) *httptest.ResponseRecorder {
				return request("GET", "/api/servers/"+id+"/logs/stream?source=file&target=/var/log/fixture.log", "", selected)
			}
			w := stream(server.ID, team)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "local-selected-log-line") || !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("selected stream: %d %s", w.Code, w.Body.String())
			}
			before := dialer.calls.Load()
			for _, caseData := range []struct{ id, team string }{{outside.ID, team}, {server.ID, other.ID}} {
				w = stream(caseData.id, caseData.team)
				if (w.Code != 403 && w.Code != 404) || strings.Contains(w.Body.String(), "local-selected-log-line") || dialer.calls.Load() != before {
					t.Fatalf("out-of-scope stream reached SSH: %d %s", w.Code, w.Body.String())
				}
			}
		})

		actions := []struct{ endpoint, body string }{
			{"/containers", "{\"image\":\"fixture:local\",\"name\":\"fixture\"}"},
			{"/images/pull", "{\"image\":\"fixture:local\"}"},
			{"/images/remove", "{\"image\":\"fixture:local\"}"},
			{"/volumes/create", "{\"name\":\"fixture\"}"},
			{"/volumes/remove", "{\"name\":\"fixture\"}"},
			{"/networks/create", "{\"name\":\"fixture\"}"},
			{"/networks/remove", "{\"name\":\"fixture\"}"},
			{"/networks/fixture/connect", "{\"container\":\"fixture\"}"},
			{"/networks/fixture/disconnect", "{\"container\":\"fixture\"}"},
			{"/system/prune", "{\"scope\":\"images\"}"},
			{"/stacks/action", `{"name":"fixture","action":"restart"}`},
			{"/stacks/deploy", `{"name":"fixture","compose":"services: {}"}`},
			{"/stacks/save", `{"name":"fixture","configFile":"/opt/fixture/docker-compose.yml","compose":"services: {}"}`},
		}
		for _, scenario := range []struct {
			name    string
			operate bool
		}{
			{"inspect-only", false}, {"granted", true}, {"revoked", false},
		} {
			next := append([]authz.Grant{}, grants...)
			if scenario.operate {
				next = append(next, authz.Grant{Action: "container.operate", Scope: authz.Scope{Kind: authz.ScopeTeam}})
			}
			role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{
				Name: "Terminal operator", ExpectedRevision: role.Revision, Pages: role.Pages,
				Grants: next, RequiresSelectedBinding: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range actions {
				t.Run(scenario.name+op.endpoint, func(t *testing.T) {
					path := func(id string) string { return "/api/servers/" + id + op.endpoint }
					assertDenied := func(id, selectedTeam string) {
						t.Helper()
						before := dialer.calls.Load()
						w := request(http.MethodPost, path(id), op.body, selectedTeam)
						if (w.Code != http.StatusForbidden && w.Code != http.StatusNotFound) || dialer.calls.Load() != before {
							t.Fatalf("out-of-scope container action: got %d, mock calls %d→%d", w.Code, before, dialer.calls.Load())
						}
					}
					assertDenied(outside.ID, team)
					assertDenied(server.ID, other.ID)
					if !scenario.operate {
						assertDenied(server.ID, team)
						return
					}
					before := dialer.calls.Load()
					w := request(http.MethodPost, path(server.ID), op.body, team)
					if w.Code != http.StatusOK || dialer.calls.Load() <= before {
						t.Fatalf("selected operation did not reach mock SSH: got %d, body=%s", w.Code, w.Body.String())
					}
				})
			}
		}
	})
	revealGrants := append(append([]authz.Grant{}, grants...), authz.Grant{Action: "container.reveal", Scope: authz.Scope{Kind: authz.ScopeTeam}})
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Terminal operator", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: revealGrants, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	r = request("GET", inspectPath, "", team)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "s3cr3t") {
		t.Fatal("explicit reveal unavailable", r.Code, r.Body.String())
	}
	diagnoseGrants := append(append([]authz.Grant{}, grants...), authz.Grant{Action: "container.diagnose", Scope: authz.Scope{Kind: authz.ScopeTeam}})
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Terminal operator", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: diagnoseGrants, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	logPath := "/api/servers/" + server.ID + "/logs?source=docker&target=app"
	r = request("GET", logPath, "", team)
	if r.Code != 200 || strings.Contains(r.Body.String(), "s3cr3t") || !strings.Contains(r.Body.String(), "MASKED") {
		t.Fatal("log redaction", r.Code, r.Body.String())
	}
	diagnosePath := "/api/servers/" + server.ID + "/containers/app/diagnose"
	r = request("POST", diagnosePath, `{}`, team)
	if r.Code != 200 || strings.Contains(model.gotLog, "s3cr3t") || !strings.Contains(model.gotLog, "MASKED") || strings.Contains(r.Body.String(), "s3cr3t") {
		t.Fatal("diagnosis redaction", r.Code, r.Body.String(), model.gotLog)
	}
	// Configuration registration failure must not forward even a log fragment.
	model.gotLog = ""
	dialer.invalidInspect = true
	r = request("POST", diagnosePath, `{}`, team)
	if r.Code != 200 || model.gotLog != "" || !strings.Contains(r.Body.String(), "unavailable") {
		t.Fatal("unsafe model fallback", r.Code, r.Body.String())
	}
	dialer.invalidInspect = false
	// Revocation while remote collection is in progress must stop the model call.
	dialer.onLogs = func() {
		var changeErr error
		role, changeErr = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Terminal operator", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: grants, RequiresSelectedBinding: true})
		if changeErr != nil {
			t.Fatal(changeErr)
		}
	}
	r = request("POST", diagnosePath, `{}`, team)
	if r.Code != 403 || model.gotLog != "" {
		t.Fatal("revoked model call", r.Code, model.gotLog)
	}
	dialer.onLogs = nil
	t.Run("terminal-actions-before-transport", func(t *testing.T) {
		host := "/api/servers/" + server.ID + "/terminal"
		container := "/api/servers/" + server.ID + "/containers/app/terminal"
		workingRole := tenancy.RoleInput{Name: "Terminal operator", Pages: role.Pages, RequiresSelectedBinding: true}
		update := func(t *testing.T, newGrants []authz.Grant) {
			t.Helper()
			workingRole.ExpectedRevision, workingRole.Grants = role.Revision, newGrants
			var err error
			role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, workingRole)
			if err != nil {
				t.Fatal(err)
			}
		}
		// Existing host terminal scope does not authorize a container terminal.
		before := dialer.calls.Load()
		for _, suffix := range []string{"", "/ws-v2"} {
			w := request("GET", container+suffix+"?shell=sh", "", team)
			if w.Code != 403 {
				t.Fatalf("container websocket without its action: %d %s", w.Code, w.Body.String())
			}
		}
		w := request("POST", container+"/sessions", `{"requestId":"unauthorized-container","shell":"sh"}`, team)
		if w.Code != 403 || dialer.calls.Load() != before {
			t.Fatalf("container TLS/HTTP command started without its action: %d", w.Code)
		}
		containerGrants := append(append([]authz.Grant{}, grants...), authz.Grant{Action: "container.terminal", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		update(t, containerGrants)
		w = request("POST", container+"/sessions", `{"requestId":"container-fixture-dialog","shell":"sh"}`, team)
		var session struct {
			ID string `json:"id"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.ID == "" {
			t.Fatalf("selected container terminal: %d %s", w.Code, w.Body.String())
		}
		cpath := container + "/sessions/" + session.ID
		w = request("POST", cpath+"/input", `{"seq":1,"data":"Zml4dHVyZQ=="}`, team)
		if w.Code != 204 {
			t.Fatalf("selected container input: %d %s", w.Code, w.Body.String())
		}
		w = request("GET", cpath+"?after=0", "", team)
		if w.Code != 200 {
			t.Fatalf("selected container poll: %d %s", w.Code, w.Body.String())
		}
		w = request("GET", host+"/sessions/"+session.ID+"?after=0", "", team)
		if w.Code != 404 {
			t.Fatalf("container session visible as host session: %d %s", w.Code, w.Body.String())
		}
		update(t, grants[:4])
		for _, prefix := range []string{host, container} {
			for _, suffix := range []string{"", "/ws-v2"} {
				w = request("GET", prefix+suffix+"?shell=sh", "", team)
				if w.Code != 403 {
					t.Fatalf("revoked websocket admission at %s: %d %s", prefix+suffix, w.Code, w.Body.String())
				}
			}
		}
		for _, tc := range []struct{ method, path, body string }{
			{"GET", cpath + "?after=0", ""},
			{"POST", cpath + "/input", `{"seq":2,"data":"Zml4dHVyZQ=="}`},
		} {
			w = request(tc.method, tc.path, tc.body, team)
			if w.Code != 403 || strings.Contains(w.Body.String(), "Zml4dHVyZQ==") {
				t.Fatalf("revoked container operation: %d", w.Code)
			}
		}
		if w = request("DELETE", cpath, "", team); w.Code != 204 {
			t.Fatalf("fixed cleanup unavailable for own denied session: %d %s", w.Code, w.Body.String())
		}
		update(t, grants)
	})

	// Revocation does not only block another connection: already buffered output,
	// the next input and the next resize must all be denied for this live session.
	_, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Terminal operator", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: grants[:4], RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range []struct{ method, path, body string }{{"GET", path + "?after=0", ""}, {"POST", path + "/input", `{"seq":2,"data":"ZGVuaWVk"}`}, {"POST", path + "/input", `{"seq":2,"cols":80,"rows":24}`}} {
		r = request(item.method, item.path, item.body, team)
		if r.Code != 403 || strings.Contains(r.Body.String(), "c2FmZSBvdXRwdXQ=") {
			t.Fatal(item.method, r.Code, r.Body.String())
		}
	}
	r = request("DELETE", path, "", team)
	if r.Code != 204 {
		t.Fatal("fixed close after revocation", r.Code, r.Body.String())
	}
}

type permissionContainerDialer struct {
	fakeInteractiveDialer
	calls          atomic.Int64
	invalidInspect bool
	onLogs         func()
}

func (d *permissionContainerDialer) RunStream(_ context.Context, _ string, _ target.SSHConfig, _ []string) (io.ReadCloser, error) {
	d.calls.Add(1)
	return io.NopCloser(strings.NewReader("local-selected-log-line\n")), nil
}

func (d *permissionContainerDialer) Run(_ context.Context, _ string, _ target.SSHConfig, cmd []string) (*target.ExecResult, error) {
	d.calls.Add(1)
	if len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "inspect" {
		if d.invalidInspect {
			return &target.ExecResult{Stdout: "invalid"}, nil
		}
		return &target.ExecResult{Stdout: sampleInspectJSON}, nil
	}
	if len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "logs" {
		if d.onLogs != nil {
			d.onLogs()
		}
		return &target.ExecResult{Stdout: "runtime s3cr3t ops@example.com"}, nil
	}
	return &target.ExecResult{}, nil
}
