package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTenantGuardHTTPActionsAndHiddenData(t *testing.T) {
	ctx := context.Background()
	model := &guardHTTPModel{}
	f := newGuardHTTPFixture(t, model)
	if err := auth.NewService(f.db, nil).Bootstrap("admin", "guard-http-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(f.db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = f.db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(f.db)
	admin, err := accounts.Login(ctx, "admin", "guard-http-password")
	if err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{f.runID}}
	input := tenancy.RoleInput{Name: "Guard reader", Pages: []string{"page.guard"}, Grants: []authz.Grant{{Action: "guard.read", Scope: scope}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "guardreader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "guardreader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "guard-reader-http-password")
	if err != nil {
		t.Fatal(err)
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	plan, err := f.service.Prepare(adminctx, f.projectID, releaseguard.PrepareInput{Mode: "from_now", AttemptID: f.attemptID})
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.service.Start(adminctx, f.projectID, releaseguard.StartInput{PlanID: plan.ID, PlanHash: plan.Hash, IdempotencyKey: "tenant-http"})
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		page, e := f.service.Evidence(adminctx, f.projectID, report.ID, 0, 100)
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 0 {
			break
		}
		select {
		case <-wait.Done():
			t.Fatal("guard runtime evidence did not become available")
		case <-tick.C:
		}
	}
	if _, err = f.service.Stop(adminctx, f.projectID, report.ID); err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithReleaseGuard(f.service))
	request := func(method, path, body, selectedTeam string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Pipewright-Team", selectedTeam)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		if csrf {
			req.Header.Set(headerCsrf, login.CSRFToken)
			req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	path := "/api/projects/" + f.projectID + "/release-guards/" + report.ID
	for _, suffix := range []string{"", "/evidence?limit=1", "/analysis"} {
		w := request("GET", path+suffix, "", team, true)
		if w.Code != 200 {
			t.Fatal("read", suffix, w.Code, w.Body.String())
		}
	}
	t.Run("events-and-analysis-preview", func(t *testing.T) {
		response := request("GET", path+"/events", "", team, true)
		var page releaseguard.EventPage
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) == 0 {
			t.Fatalf("scoped timeline: %d %s", response.Code, response.Body.String())
		}
		for _, target := range []string{
			"/api/projects/unrelated/release-guards/" + report.ID + "/events",
			"/api/projects/" + f.projectID + "/release-guards/unknown/events",
		} {
			if w := request("GET", target, "", team, true); w.Code != 404 || strings.Contains(w.Body.String(), report.ID) {
				t.Fatalf("events data boundary: %d %s", w.Code, w.Body.String())
			}
		}
		preview := func() *httptest.ResponseRecorder {
			return request("POST", path+"/analysis/preview", `{"includeRuntime":true,"includeLogs":false}`, team, true)
		}
		var rows int
		if w := preview(); w.Code != 404 {
			t.Fatalf("reader cannot make analysis: %d %s", w.Code, w.Body.String())
		}
		if e := f.db.QueryRow("SELECT COUNT(*) FROM release_guard_analyses WHERE guard_id=?", report.ID).Scan(&rows); e != nil || rows != 0 || model.calls.Load() != 0 {
			t.Fatal("denied action was not read-only", e, rows, model.calls.Load())
		}
		writeRole := func(grants []authz.Grant) {
			input.ExpectedRevision = role.Revision
			input.Grants = grants
			saved, e := repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
			if e != nil {
				t.Fatal(e)
			}
			role = saved
		}
		read := append([]authz.Grant{}, input.Grants...)
		writeRole(append(append([]authz.Grant{}, read...), authz.Grant{Action: "guard.analyze", Scope: scope}))
		w := preview()
		var analysis releaseguard.Analysis
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &analysis) != nil || analysis.ID == "" ||
			analysis.GuardID != report.ID || strings.Contains(analysis.Payload, "fixture-private") || model.calls.Load() != 0 {
			t.Fatalf("scoped preview should use local evidence without dispatch: %d %s", w.Code, w.Body.String())
		}
		writeRole(read)
		if w := preview(); w.Code != 404 {
			t.Fatalf("same session retained analysis permission: %d %s", w.Code, w.Body.String())
		}
		if e := f.db.QueryRow("SELECT COUNT(*) FROM release_guard_analyses WHERE guard_id=?", report.ID).Scan(&rows); e != nil || rows != 1 || model.calls.Load() != 0 {
			t.Fatal("expected exactly one local analysis and no external calls", e, rows)
		}
	})
	t.Run("separate-reverification-action", func(t *testing.T) {
		base := "/api/projects/" + f.projectID
		prep := releaseguard.PrepareInput{Mode: "from_now", AttemptID: f.attemptID, ParentGuardID: report.ID, DurationSeconds: 300}
		raw, _ := json.Marshal(prep)
		planRoute := base + "/release-guard-plans"
		if w := request("POST", planRoute, string(raw), team, true); w.Code != 403 {
			t.Fatalf("read-only role prepared plan: %d %s", w.Code, w.Body.String())
		}
		// Prepare a fresh plan so the check is of the action, not plan consumption.
		otherPlan, e := f.service.Prepare(adminctx, f.projectID, prep)
		if e != nil {
			t.Fatal(e)
		}
		start := releaseguard.StartInput{PlanID: otherPlan.ID, PlanHash: otherPlan.Hash, IdempotencyKey: "reverification-denied"}
		rawStart, _ := json.Marshal(start)
		if w := request("POST", base+"/release-guards", string(rawStart), team, true); w.Code != 403 {
			t.Fatalf("read-only role started plan: %d %s", w.Code, w.Body.String())
		}
		var consumed string
		if e = f.db.QueryRow("SELECT consumed_by FROM release_guard_plans WHERE id=?", otherPlan.ID).Scan(&consumed); e != nil || consumed != "" {
			t.Fatal("denied start consumed the plan", e, consumed)
		}
		read := append([]authz.Grant{}, input.Grants...)
		input.ExpectedRevision = role.Revision
		input.Grants = append(append([]authz.Grant{}, read...), authz.Grant{Action: "guard.observe", Scope: scope})
		role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
		if e != nil {
			t.Fatal(e)
		}
		w := request("POST", planRoute, string(raw), team, true)
		var replanned releaseguard.Plan
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &replanned) != nil || replanned.ID == "" || replanned.RunID != f.runID || replanned.ParentGuardID != report.ID {
			t.Fatalf("exact source plan: %d %s", w.Code, w.Body.String())
		}
		// Revoke observation before committing; no report or claim may be created.
		input.ExpectedRevision, input.Grants = role.Revision, read
		role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
		if e != nil {
			t.Fatal(e)
		}
		start.PlanID, start.PlanHash = replanned.ID, replanned.Hash
		rawStart, _ = json.Marshal(start)
		if w = request("POST", base+"/release-guards", string(rawStart), team, true); w.Code != 403 {
			t.Fatalf("revoked role started plan: %d %s", w.Code, w.Body.String())
		}
		var reports int
		if e = f.db.QueryRow("SELECT COUNT(*) FROM release_guards WHERE project_id=?", f.projectID).Scan(&reports); e != nil || reports != 1 {
			t.Fatal("denied revalidation created a report", e, reports)
		}
		t.Run("qualified-employee-can-reverify-exact-source", func(t *testing.T) {
			serverID := report.Plan.Targets[0].ServerID
			var credentialID string
			if e := f.db.QueryRow("SELECT credential_id FROM servers WHERE id=?", serverID).Scan(&credentialID); e != nil {
				t.Fatal(e)
			}
			serverScope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: serverID}}}
			credentialScope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "credential", ID: credentialID}}}
			input.ExpectedRevision = role.Revision
			input.Pages = []string{"page.guard", "page.servers", "page.vault"}
			input.Grants = append(append([]authz.Grant{}, read...),
				authz.Grant{Action: "guard.observe", Scope: scope},
				authz.Grant{Action: "server.inspect", Scope: serverScope},
				authz.Grant{Action: "server.use", Scope: serverScope},
				authz.Grant{Action: "credential.metadata", Scope: credentialScope},
				authz.Grant{Action: "credential.use", Scope: credentialScope},
			)
			var e error
			role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
			if e != nil {
				t.Fatal(e)
			}
			w := request("POST", base+"/release-guards", string(rawStart), team, true)
			var rerun releaseguard.Report
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rerun) != nil || rerun.ID == "" || rerun.RunID != f.runID || rerun.ParentGuardID != report.ID {
				t.Fatalf("authorized employee did not start exact revalidation: %d %s", w.Code, w.Body.String())
			}
			if e = f.db.QueryRow("SELECT consumed_by FROM release_guard_plans WHERE id=?", replanned.ID).Scan(&consumed); e != nil || consumed != rerun.ID {
				t.Fatal("authorized start did not consume exact plan", e, consumed)
			}
			if _, e = f.service.Stop(adminctx, f.projectID, rerun.ID); e != nil {
				t.Fatal(e)
			}
			input.ExpectedRevision, input.Grants, input.Pages = role.Revision, read, []string{"page.guard"}
			role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
			if e != nil {
				t.Fatal(e)
			}
		})
	})

	for _, tc := range []struct{ method, suffix, body string }{{"POST", "/stop", "{}"}, {"POST", "/investigate", "{}"}, {"POST", "/analysis/fake/send", `{"confirmed":true}`}, {"POST", "/analysis/fake/revoke", "{}"}, {"DELETE", "", ""}} {
		w := request(tc.method, path+tc.suffix, tc.body, team, true)
		if w.Code != 404 {
			t.Fatal("reader write", tc.suffix, w.Code, w.Body.String())
		}
	}
	if w := request("POST", path+"/stop", "{}", team, false); w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	if w := request("GET", path, "", "forged-team", true); w.Code != 403 {
		t.Fatal("forged team", w.Code)
	}
	var before, after int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM release_guards WHERE id=?`, report.ID).Scan(&before); err != nil || before != 1 {
		t.Fatal(before, err)
	}
	input.ExpectedRevision = role.Revision
	input.Grants = append(input.Grants, authz.Grant{Action: "guard.delete", Scope: scope})
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	w := request("DELETE", path, "", team, true)
	if w.Code != 204 && w.Code != 200 {
		t.Fatal("explicit delete", w.Code, w.Body.String())
	}
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM release_guards WHERE id=?`, report.ID).Scan(&after); err != nil || after != 0 {
		t.Fatal(after, err)
	}
	w = request("GET", path, "", team, true)
	if w.Code != 404 {
		t.Fatal("deleted read", w.Code)
	}
	// A single run-scoped read does not reveal arbitrary deployment attempts.
	w = request("GET", "/api/runs/not-allowed/deployment-attempts", "", team, true)
	if w.Code != 403 {
		t.Fatal("outside attempts", w.Code)
	}
	var payload map[string]any
	if json.Unmarshal(w.Body.Bytes(), &payload) != nil {
		t.Fatal("error format")
	}
}
