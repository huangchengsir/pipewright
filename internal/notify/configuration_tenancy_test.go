package notify

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationConfigurationRequiresLiveInstanceAdministrator(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	svc, db, _ := newSvc(t, server.Client())
	base := context.Background()
	ch, err := svc.Create(base, CreateInput{Name: "Global", Type: TypeWebhook, Enabled: true, Config: Config{URL: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	if _, err = db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('nf-cred','fixture','git_token','',?,'masked',?,?)`, []byte{0}, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"nf-p1", "nf-p2"} {
		if _, err = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,'https://example.invalid/repo','main','nf-cred',?,?)`, id, id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	route, err := svc.CreateRoute(base, CreateRouteInput{ProjectID: "nf-p2", Event: EventBuildFailed, ChannelID: ch.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := svc.CreateTemplate(base, CreateTemplateInput{ProjectID: "nf-p2", Event: EventBuildFailed, TitleTemplate: "foreign", BodyTemplate: "foreign body"})
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "notify-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(base, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(base, "admin", "notify-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateTeamAs(base, admin.Actor, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='project' AND resource_id='nf-p2'`, other.ID); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(base, admin.Actor, team, "", tenancy.RoleInput{Name: "Notification reader", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "notification.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(base, admin.Actor, tenancy.CreateUserInput{Username: "notifyreader", DisplayName: "Notify", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(base, "notifyreader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(base, login.Token, initial.Password, "notify-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	user := tenancy.WithActor(base, login.Actor, team)
	cases := map[string]func() error{
		"list": func() error { _, e := svc.List(user); return e }, "get": func() error { _, e := svc.Get(user, ch.ID); return e },
		"create": func() error { _, e := svc.Create(user, CreateInput{}); return e }, "update": func() error { _, e := svc.Update(user, ch.ID, UpdateInput{}); return e }, "delete": func() error { return svc.Delete(user, ch.ID) }, "test": func() error { _, e := svc.Test(user, ch.ID); return e },
		"language": func() error { return svc.SetNotifyLanguage(user, "en") }, "routes": func() error { _, e := svc.ListRoutes(user); return e }, "project routes": func() error { _, e := svc.ListRoutesForProject(user, "nf-p1"); return e }, "create route": func() error { _, e := svc.CreateRoute(user, CreateRouteInput{}); return e }, "delete route": func() error { return svc.DeleteRoute(user, route.ID) },
		"templates": func() error { _, e := svc.ListTemplates(user); return e }, "create template": func() error { _, e := svc.CreateTemplate(user, CreateTemplateInput{}); return e }, "update template": func() error { _, e := svc.UpdateTemplate(user, tpl.ID, UpdateTemplateInput{}); return e }, "delete template": func() error { return svc.DeleteTemplate(user, tpl.ID) },
	}
	for name, fn := range cases {
		if e := fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatalf("%s: %v", name, e)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("denied notification sent")
	}
	for table, want := range map[string]int{"notification_channels": 1, "notification_routes": 1, "notification_templates": 1} {
		var n int
		if err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != want {
			t.Fatal("denied config mutation", table, n, err)
		}
	}
	adminctx := tenancy.WithActor(base, admin.Actor, team)
	if _, err = svc.List(adminctx); err != nil {
		t.Fatal("admin list", err)
	}
	if _, err = svc.ListRoutesForProject(adminctx, "nf-p2"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("foreign project route", err)
	}
	if err = svc.DeleteRoute(adminctx, route.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("foreign route delete", err)
	}
	if err = svc.DeleteTemplate(adminctx, tpl.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("foreign template delete", err)
	}
	templates, err := svc.ListTemplates(adminctx)
	if err != nil || len(templates) != 0 {
		t.Fatal("foreign template list", templates, err)
	}
	if _, err = svc.CreateRoute(adminctx, CreateRouteInput{ProjectID: "nf-p1", Event: EventBuildFailed, ChannelID: ch.ID, Enabled: true}); err != nil {
		t.Fatal("admin own project route", err)
	}
	if err = svc.SetNotifyLanguage(adminctx, "en"); err != nil {
		t.Fatal(err)
	}
	if err = accounts.Logout(base, admin.Token); err != nil {
		t.Fatal(err)
	}
	if err = svc.SetNotifyLanguage(adminctx, "zh-CN"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked admin write", err)
	}
}
