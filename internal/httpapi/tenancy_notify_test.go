package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTenantNotificationHTTPDeniesConfigurationAndEffects(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	var sends atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.WriteHeader(200) }))
	defer receiver.Close()
	nf := notify.New(db, vault.New(db, testMasterKey()), receiver.Client())
	ch, err := nf.Create(ctx, notify.CreateInput{Name: "Global recipient", Type: notify.TypeWebhook, Enabled: true, Config: notify.Config{URL: receiver.URL}})
	if err != nil {
		t.Fatal(err)
	}
	route, err := nf.CreateRoute(ctx, notify.CreateRouteInput{Event: notify.EventBuildFailed, ChannelID: ch.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := nf.CreateTemplate(ctx, notify.CreateTemplateInput{Event: notify.EventBuildFailed, TitleTemplate: "private title", BodyTemplate: "private body"})
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "notify-http-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "notify-http-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Read notifications", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "notification.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "notifyuser", DisplayName: "Notify", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	employee, err := accounts.Login(ctx, "notifyuser", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	employee, err = accounts.ChangePassword(ctx, employee.Token, initial.Password, "notify-http-user-password")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithNotifications(nf))
	request := func(method, path, body, token, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/notifications"+path, strings.NewReader(body))
		r.Header.Set("X-Pipewright-Team", team)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		}
		if csrf != "" {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	options := request("GET", "/channel-options", "", employee.Token, "")
	var selector struct {
		Items []map[string]any `json:"items"`
	}
	if options.Code != 200 || json.Unmarshal(options.Body.Bytes(), &selector) != nil || len(selector.Items) != 1 || len(selector.Items[0]) != 4 || selector.Items[0]["id"] != ch.ID || strings.Contains(options.Body.String(), receiver.URL) {
		t.Fatal("minimal selector", options.Code, options.Body.String())
	}
	cases := []struct{ method, path, body string }{
		{"GET", "/channels", ""}, {"GET", "/channels/" + ch.ID, ""}, {"GET", "/config", ""}, {"GET", "/routes", ""}, {"GET", "/templates", ""},
		{"POST", "/channels", `{"name":"intruder","type":"webhook","enabled":true,"config":{"url":"https://fixture.invalid"}}`},
		{"PUT", "/channels/" + ch.ID, `{"name":"changed"}`}, {"DELETE", "/channels/" + ch.ID, ""}, {"POST", "/channels/" + ch.ID + "/test", "{}"},
		{"PUT", "/config", `{"language":"en"}`}, {"POST", "/routes", `{"event":"build_failed","channelId":"` + ch.ID + `","enabled":true}`}, {"DELETE", "/routes/" + route.ID, ""},
		{"POST", "/templates", `{"event":"build_failed","titleTemplate":"x","bodyTemplate":"x"}`}, {"PUT", "/templates/" + tpl.ID, `{"titleTemplate":"changed"}`}, {"DELETE", "/templates/" + tpl.ID, ""},
	}
	for _, c := range cases {
		w := request(c.method, c.path, c.body, employee.Token, employee.CSRFToken)
		if w.Code != 403 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private body") || strings.Contains(w.Body.String(), receiver.URL) {
			t.Fatal("response leak")
		}
	}
	if sends.Load() != 0 {
		t.Fatal("denied send reached receiver")
	}
	for _, table := range []string{"notification_channels", "notification_routes", "notification_templates"} {
		var n int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Fatal(table, n, err)
		}
	}
	var name string
	if err = db.QueryRow(`SELECT name FROM notification_channels WHERE id=?`, ch.ID).Scan(&name); err != nil || name != "Global recipient" {
		t.Fatal(name, err)
	}
	w := request("GET", "/channels", "", admin.Token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Global recipient") {
		t.Fatal("admin read", w.Code, w.Body.String())
	}
	w = request("PUT", "/config", `{"language":"en"}`, admin.Token, "")
	if w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	w = request("PUT", "/config", `{"language":"en"}`, admin.Token, admin.CSRFToken)
	if w.Code != 200 {
		t.Fatal("admin write", w.Code, w.Body.String())
	}
	if err = accounts.Logout(ctx, admin.Token); err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/channels/"+ch.ID+"/test", "{}", admin.Token, admin.CSRFToken)
	if w.Code != 401 || sends.Load() != 0 {
		t.Fatal("logout effect", w.Code, sends.Load())
	}
}
