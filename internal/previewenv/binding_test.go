package previewenv

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
	"time"
)

func TestPreviewReclaimUsesOriginalProviderAndRetainsFailedState(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "stored", true: "legacy_route"}[legacy], func(t *testing.T) {
			svc, _, _ := newTestSvc(t)
			ctx := context.Background()
			enablePreview(t, svc, "p1")
			env, err := svc.Provision(ctx, ProvisionInput{ProjectID: "p1", PRNumber: 1, ServerID: "server", UpstreamContainer: "app", UpstreamPort: 80, HostIP: "192.0.2.1"})
			if err != nil {
				t.Fatal(err)
			}
			db := svc.store.db
			seedTenantPreviewResources(t, db)
			if legacy {
				if _, err = db.Exec(`UPDATE preview_envs SET binding_json=NULL WHERE id=?`, env.ID); err != nil {
					t.Fatal(err)
				}
				stamp := time.Now().UTC().Format(time.RFC3339)
				if _, err = db.Exec(`INSERT INTO proxy_routes(id,server_id,domain,upstream_container,upstream_port,tls_mode,enabled,cert_status,created_at,updated_at,config) VALUES (?,?,?,'app',80,'auto',1,'unknown',?,?,?)`, env.RouteID, env.ServerID, env.Subdomain, stamp, stamp, `{"dnsProviderId":"prov-1"}`); err != nil {
					t.Fatal(err)
				}
			}
			if err = auth.NewService(db, nil).Bootstrap("admin", "preview-binding-password"); err != nil {
				t.Fatal(err)
			}
			if _, err = tenancy.NewRepository(db).ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
				t.Fatal(err)
			}
			account, err := tenancy.NewAccounts(db).Login(ctx, "admin", "preview-binding-password")
			if err != nil {
				t.Fatal(err)
			}
			var team string
			if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
				t.Fatal(err)
			}
			actor := tenancy.WithActor(ctx, account.Actor, team)
			if _, err = svc.SetConfig(actor, Config{ProjectID: "p1", Enabled: true, DNSProviderID: "prov-2", BaseDomain: "changed.example.com"}); err != nil {
				t.Fatal(err)
			}
			failed := errors.New("injected provider unavailable")
			dnsCalls, routeCalls := 0, 0
			svc.SetRecordDeleter(tenancyRecordDeleter(func(_ context.Context, provider, fqdn string) error {
				dnsCalls++
				if provider != "prov-1" || fqdn != env.Subdomain {
					t.Fatal("retargeted deletion", provider, fqdn)
				}
				return failed
			}))
			svc.SetRouteDeleter(tenancyRouteDeleter(func(context.Context, string) error { routeCalls++; return nil }))
			if err = svc.Reclaim(actor, "p1", 1); !errors.Is(err, failed) {
				t.Fatal("DNS failure lost", err)
			}
			current, err := svc.store.get(ctx, env.ID)
			if err != nil || current.Status != StatusActive || routeCalls != 0 || dnsCalls != 1 {
				t.Fatal("failed reclaim falsely completed", current, routeCalls, dnsCalls, err)
			}
			// The recovered binding is durable before effects, so a lost legacy route
			// cannot redirect a retry to the changed project configuration.
			if legacy {
				if _, err = db.Exec(`DELETE FROM proxy_routes WHERE id=?`, env.RouteID); err != nil {
					t.Fatal(err)
				}
			}
			svc.SetRecordDeleter(tenancyRecordDeleter(func(_ context.Context, provider, fqdn string) error {
				dnsCalls++
				if provider != "prov-1" || fqdn != env.Subdomain {
					t.Fatal("retry retargeted", provider, fqdn)
				}
				return nil
			}))
			svc.SetRouteDeleter(tenancyRouteDeleter(func(context.Context, string) error { routeCalls++; return failed }))
			if err = svc.Reclaim(actor, "p1", 1); !errors.Is(err, failed) {
				t.Fatal("route failure lost", err)
			}
			current, err = svc.store.get(ctx, env.ID)
			if err != nil || current.Status != StatusActive {
				t.Fatal("route failure marked success", err)
			}
			svc.SetRouteDeleter(tenancyRouteDeleter(func(context.Context, string) error { routeCalls++; return nil }))
			if err = svc.Reclaim(actor, "p1", 1); err != nil {
				t.Fatal(err)
			}
			current, err = svc.store.get(ctx, env.ID)
			if err != nil || current.Status != StatusReclaimed || dnsCalls != 3 || routeCalls != 2 {
				t.Fatal("completed reclaim", current, dnsCalls, routeCalls, err)
			}
		})
	}
}
