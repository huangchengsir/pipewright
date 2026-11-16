package notify

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNotificationRuntimeSeparatesSourcesRecipientsAndDirectSending(t *testing.T) {
	var sends atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.WriteHeader(200) }))
	defer receiver.Close()
	svc, db, _ := newSvc(t, receiver.Client())
	base := context.Background()
	first, err := svc.Create(base, CreateInput{Name: "Visible", Type: TypeWebhook, Enabled: true, Config: Config{URL: receiver.URL + "/private-recipient"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(base, CreateInput{Name: "Hidden", Type: TypeWebhook, Enabled: true, Config: Config{URL: receiver.URL + "/hidden"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "notify-runtime-admin"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(base, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(base, "admin", "notify-runtime-admin")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	adminctx := tenancy.WithActor(base, admin.Actor, team)
	if _, err = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id) VALUES('server','s1',?)`, team); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(base, admin.Actor, team, "", tenancy.RoleInput{Name: "Reader", Pages: []string{"page.runs", "page.servers", "page.anomalies"}, Grants: []authz.Grant{{Action: "notification.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{first.ID}}}, {Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}}, {Action: "anomaly.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(base, admin.Actor, tenancy.CreateUserInput{Username: "runtime-reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	employee, err := accounts.Login(base, "runtime-reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	employee, err = accounts.ChangePassword(base, employee.Token, initial.Password, "runtime-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	user := tenancy.WithActor(base, employee.Actor, team)
	options, err := svc.ListChannelOptions(user)
	if err != nil || len(options) != 1 || options[0].ID != first.ID {
		t.Fatal(options, err)
	}
	body, _ := json.Marshal(options)
	if strings.Contains(string(body), "private-recipient") || strings.Contains(string(body), "config") {
		t.Fatal("recipient leaked")
	}
	attempted := false
	if err = svc.SendVia(WithDeliveryObserver(user, func() { attempted = true }), first.ID, Payload{Title: "Direct"}); !errors.Is(err, tenancy.ErrForbidden) || attempted || sends.Load() != 0 {
		t.Fatal("read granted delivery", err, attempted, sends.Load())
	}
	if _, _, err = svc.ResolveChannel(user, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("hidden channel resolved", err)
	}
	if _, err = svc.CreateRoute(adminctx, CreateRouteInput{Event: EventAnomalyDetected, ChannelID: first.ID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	source := WithAnomalySource(user, "s1")
	vars := TemplateVars{Event: EventAnomalyDetected, Project: "Node", Status: "high CPU"}
	if err = svc.RouteEventForProject(source, "", EventAnomalyDetected, vars); err != nil || sends.Load() != 1 {
		t.Fatal("approved routed delivery", err, sends.Load())
	}
	if err = svc.RouteEventForProject(WithAnomalySource(user, "outside"), "", EventAnomalyDetected, vars); !errors.Is(err, tenancy.ErrForbidden) || sends.Load() != 1 {
		t.Fatal("source scope", err, sends.Load())
	}
	queued, err := svc.routedContext(source, "", EventAnomalyDetected, first.ID, vars)
	if err != nil {
		t.Fatal(err)
	}
	replacement := Config{URL: receiver.URL + "/new-recipient"}
	if _, err = svc.Update(adminctx, first.ID, UpdateInput{Config: &replacement}); err != nil {
		t.Fatal(err)
	}
	if err = svc.SendVia(queued, first.ID, Payload{Title: "Queued"}); err == nil || sends.Load() != 1 {
		t.Fatal("recipient redirect", err, sends.Load())
	}
	if err = svc.RouteEventForProject(source, "", EventAnomalyDetected, vars); err != nil || sends.Load() != 2 {
		t.Fatal("new explicit approval", err, sends.Load())
	}
	if _, err = db.Exec(`UPDATE access_users SET security_generation=security_generation+1 WHERE id=?`, admin.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.RouteEventForProject(source, "", EventAnomalyDetected, vars); err == nil || sends.Load() != 2 {
		t.Fatal("approver revoked", err, sends.Load())
	}
}
