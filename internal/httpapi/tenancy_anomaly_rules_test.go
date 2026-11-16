package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTenantAnomalyRulesHTTPScopesAndMutationBoundaries(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	svc := anomaly.New(db, nil)
	defer svc.Close()
	ids := map[string]string{}
	for _, server := range []string{"s1", "s2", "s3", ""} {
		in := anomaly.CreateRuleInput{Metric: "cpu", Operator: "gt", Threshold: 80, Enabled: true}
		if server != "" {
			in.ServerID = &server
		}
		r, e := svc.CreateRule(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		ids[server] = r.ID
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "rules-http-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "rules-http-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []struct{ id, team string }{{"s1", team}, {"s2", team}, {"s3", other.ID}} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('server',?,?,?)`, s.id, s.team, admin.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	grants := []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "anomaly.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1", "s2"}}}, {Action: "anomaly.manage", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Rules", Pages: []string{"page.servers", "page.anomalies"}, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "rulehttp", DisplayName: "Rules", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "rulehttp", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "rules-http-new-password")
	if e != nil {
		t.Fatal(e)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithAnomaly(svc), WithAnomalyConfig(60, 180))
	request := func(method, path, body, teamHeader string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.Header.Set("X-Pipewright-Team", teamHeader)
		if csrf {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
			r.Header.Set("X-CSRF-Token", login.CSRFToken)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	t.Run("scoped-alerts-and-limit", func(t *testing.T) {
		for i, server := range []string{"s1", "s2", "s3"} {
			id := "alert-" + server
			stamp := []string{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z"}[i]
			if _, err := db.Exec(`INSERT INTO anomaly_alerts(id,server_id,server_name,metric,operator,threshold,value,message,created_at)
				VALUES (?,?,?,'cpu','gt',80,81,'local fixture alert',?)`, id, server, "fixture-"+server, stamp); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			query string
			ids   []string
		}{
			{"", []string{"alert-s2", "alert-s1"}}, {"?limit=1", []string{"alert-s2"}},
			{"?serverId=s1", []string{"alert-s1"}}, {"?serverId=s3", nil}, {"?serverId=unknown", nil},
		} {
			w := request("GET", "/api/anomaly/alerts"+tc.query, "", team, false)
			var out struct {
				Alerts []anomalyAlertDTO `json:"alerts"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
				t.Fatalf("alert query failed: %d %s", w.Code, w.Body.String())
			}
			var ids []string
			for _, row := range out.Alerts {
				ids = append(ids, row.ID)
			}
			if !slices.Equal(ids, tc.ids) || strings.Contains(w.Body.String(), "fixture-s3") {
				t.Fatal("unexpected scoped alert list", tc.query, ids)
			}
		}
	})
	t.Run("instance-cadence-and-global-check", func(t *testing.T) {
		w := request("GET", "/api/anomaly/config", "", team, false)
		var setup map[string]int
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &setup) != nil || !reflect.DeepEqual(setup, map[string]int{"intervalSeconds": 60, "cooldownSeconds": 180}) {
			t.Fatalf("only configured scheduling cadence should be returned: %d %s", w.Code, w.Body.String())
		}
		w = request("POST", "/api/anomaly/check", "", team, true)
		if w.Code != 403 {
			t.Fatalf("selected rule manager ran instance-wide check: %d %s", w.Code, w.Body.String())
		}
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM anomaly_alerts").Scan(&count); err != nil || count != 3 {
			t.Fatal("denied check added or changed alerts", count, err)
		}
	})

	w := request("GET", "/api/anomaly/rules", "", team, false)
	var listed struct {
		Items []anomalyRuleDTO `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Items) != 2 {
		t.Fatal("list", w.Code, w.Body.String())
	}
	for _, r := range listed.Items {
		if r.ServerID == nil || (*r.ServerID != "s1" && *r.ServerID != "s2") {
			t.Fatal("leaked rule")
		}
	}
	payload := `{"metric":"cpu","operator":"gt","threshold":42,"serverId":"s1","enabled":true}`
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/anomaly/rules", strings.Replace(payload, "s1", "s2", 1), 403},
		{"POST", "/api/anomaly/rules", strings.Replace(payload, "s1", "s3", 1), 403},
		{"POST", "/api/anomaly/rules", strings.Replace(payload, `"s1"`, `null`, 1), 403},
		{"POST", "/api/anomaly/rules", strings.Replace(payload, `"enabled":true`, `"enabled":true,"aiAnalysisEnabled":true`, 1), 403},
		{"PATCH", "/api/anomaly/rules/" + ids["s1"], strings.Replace(payload, "s1", "s2", 1), 403},
		{"PATCH", "/api/anomaly/rules/" + ids["s2"], payload, 403},
		{"PATCH", "/api/anomaly/rules/" + ids["s3"], payload, 404},
		{"DELETE", "/api/anomaly/rules/" + ids["s2"], "", 403},
		{"DELETE", "/api/anomaly/rules/" + ids["s3"], "", 404},
		{"DELETE", "/api/anomaly/rules/" + ids[""], "", 404},
		{"DELETE", "/api/anomaly/rules/unknown", "", 404},
	} {
		w = request(tc.method, tc.path, tc.body, team, true)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	var n int
	if e = db.QueryRow(`SELECT COUNT(*) FROM anomaly_rules WHERE threshold=80`).Scan(&n); e != nil || n != 4 {
		t.Fatal("denied mutation wrote data", n, e)
	}
	w = request("POST", "/api/anomaly/rules", payload, team, false)
	if w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	w = request("POST", "/api/anomaly/rules", payload, other.ID, true)
	if w.Code != 403 {
		t.Fatal("forged team", w.Code)
	}
	w = request("POST", "/api/anomaly/rules", payload, team, true)
	var created anomalyRuleDTO
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatal("create", w.Code, w.Body.String())
	}
	w = request("PATCH", "/api/anomaly/rules/"+created.ID, strings.Replace(payload, "42", "43", 1), team, true)
	if w.Code != 200 {
		t.Fatal("update", w.Code, w.Body.String())
	}
	w = request("DELETE", "/api/anomaly/rules/"+created.ID, "", team, true)
	if w.Code != 204 {
		t.Fatal("delete", w.Code, w.Body.String())
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Rules", Pages: []string{"page.servers", "page.anomalies"}, Grants: grants[:2]}); e != nil {
		t.Fatal(e)
	}
	w = request("POST", "/api/anomaly/rules", payload, team, true)
	if w.Code != 403 {
		t.Fatal("live revocation", w.Code)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	w = request("GET", "/api/anomaly/rules", "", team, false)
	if w.Code != 401 {
		t.Fatal("logout", w.Code)
	}
}
