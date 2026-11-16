package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type proxyHTTPFixtureTarget struct {
	target.Service
	servers  map[string]target.Server
	observed []string
	uploads  []string
}

func (p *proxyHTTPFixtureTarget) Capture(_ context.Context, id string) (target.ConnectionSnapshot, error) {
	server, ok := p.servers[id]
	if !ok {
		return target.ConnectionSnapshot{}, tenancy.ErrForbidden
	}
	return target.ConnectionSnapshot{Server: server, CredentialVersion: "synthetic"}, nil
}

func (p *proxyHTTPFixtureTarget) ExecLimited(ctx context.Context, s target.ConnectionSnapshot, args []string, limits target.ExecutionLimits) (*target.LimitedResult, error) {
	return p.ExecPinned(ctx, s, args, limits)
}

func (p *proxyHTTPFixtureTarget) ExecPinned(ctx context.Context, s target.ConnectionSnapshot, args []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
	if e := tenancy.VerifyExecution(ctx); e != nil {
		return nil, e
	}
	p.observed = append(p.observed, s.Server.ID)
	if strings.Contains(strings.Join(args, " "), "State.Running") {
		return &target.LimitedResult{Stdout: "true|fixture/proxy-image"}, nil
	}
	if strings.Contains(strings.Join(args, " "), "NetworkSettings.Ports") {
		return &target.LimitedResult{Stdout: `{"80/tcp":[{"HostPort":"80"}],"443/tcp":[{"HostPort":"443"}]}`}, nil
	}
	return &target.LimitedResult{}, nil
}

func (p *proxyHTTPFixtureTarget) UploadPinned(ctx context.Context, s target.ConnectionSnapshot, r io.Reader, _ string) error {
	if e := tenancy.VerifyExecution(ctx); e != nil {
		return e
	}
	p.uploads = append(p.uploads, s.Server.ID)
	_, e := io.Copy(io.Discard, r)
	return e
}

func TestTenantProxyHTTPReadsAndWrites(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	login, e := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if e != nil {
		t.Fatal(e)
	}
	var team, roleID string
	var roleRevision uint64
	if e = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); e != nil {
		t.Fatal(e)
	}
	admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.db.QueryRow("SELECT id, revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'", team).Scan(&roleID, &roleRevision); e != nil {
		t.Fatal(e)
	}
	v := vault.WithContext(tenancy.WithActor(ctx, admin.Actor, team), vault.New(f.db, testMasterKey()))
	credential, e := v.Create(vault.CreateInput{Name: "Proxy mock password", Type: vault.TypeSSHPassword, Secret: "mock-only"})
	if e != nil {
		t.Fatal(e)
	}
	cred := credential.ID
	setGrants := func(t *testing.T, grants ...authz.Grant) {
		t.Helper()
		role, e := tenancy.NewRepository(f.db).SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{
			Name: "Auxiliary", Pages: []string{"page.servers", "page.containers", "page.vault"}, Grants: grants, ExpectedRevision: roleRevision,
		})
		if e != nil {
			t.Fatal(e)
		}
		roleRevision = role.Revision
	}
	for _, p := range []struct{ id, server string }{{"proxy-a", "s1"}, {"proxy-b", "s2"}, {"proxy-foreign", "foreign-server"}} {
		if _, e = f.db.Exec(`INSERT INTO proxy_routes(id,server_id,domain,upstream_container,upstream_port,tls_mode,enabled,cert_status,cert_detail,config,created_at,updated_at)
     VALUES (?,?,?,'web',80,'auto',1,'pending','unchanged','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, p.id, p.server, p.id+".invalid"); e != nil {
			t.Fatal(e)
		}
	}
	fake := &proxyHTTPFixtureTarget{servers: map[string]target.Server{"s1": {ID: "s1", Host: "192.0.2.15", User: "fixture", CredentialID: cred}}}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, login.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithProxy(proxy.New(f.db, fake)))
	req := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", team)
		r.Header.Set(headerCsrf, login.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	want := func(t *testing.T, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("expected %d, got %d: %s", status, w.Code, w.Body.String())
		}
	}
	hostScope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: "s1"}}}
	credScope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "credential", ID: cred}}}
	grants := []authz.Grant{
		{Action: "proxy.read", Scope: hostScope}, {Action: "server.inspect", Scope: hostScope}, {Action: "server.use", Scope: hostScope},
		{Action: "credential.metadata", Scope: credScope}, {Action: "credential.use", Scope: credScope},
	}
	setGrants(t, grants...)
	t.Run("routes-filtered", func(t *testing.T) {
		for _, caseData := range []struct {
			suffix string
			ids    []string
		}{{"", []string{"proxy-a"}}, {"?serverId=s1", []string{"proxy-a"}}, {"?serverId=s2", nil}, {"?serverId=foreign-server", nil}, {"?serverId=unknown", nil}} {
			w := req("GET", "/api/proxy/routes"+caseData.suffix, "")
			want(t, w, 200)
			var out struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
			var ids []string
			for _, item := range out.Items {
				ids = append(ids, item.ID)
			}
			if !reflect.DeepEqual(ids, caseData.ids) {
				t.Fatal("incorrect exact routes", caseData.suffix, ids)
			}
		}
	})
	t.Run("selected-caddy-status", func(t *testing.T) {
		w := req("GET", "/api/proxy/caddy?serverId=s1", "")
		want(t, w, 200)
		if !strings.Contains(w.Body.String(), `"serverId":"s1"`) || !strings.Contains(w.Body.String(), `"routeCount":1`) || !reflect.DeepEqual(fake.observed, []string{"s1", "s1"}) {
			t.Fatal("did not observe exact selected server", w.Body.String(), fake.observed)
		}
		for _, id := range []string{"s2", "foreign-server", "unknown"} {
			want(t, req("GET", "/api/proxy/caddy?serverId="+id, ""), 403)
		}
		if len(fake.observed) != 2 {
			t.Fatal("out of scope server reached SSH fixture")
		}
	})
	t.Run("independent-mutation-actions", func(t *testing.T) {
		for _, test := range []struct{ name, method, path, body string }{
			{"install", "POST", "/api/proxy/caddy?serverId=s1", ""},
			{"cleanup", "DELETE", "/api/proxy/caddy?serverId=s1", ""},
			{"create", "POST", "/api/proxy/routes", `{"serverId":"s1","domain":"new-proxy.invalid","upstreamContainer":"web","upstreamPort":80}`},
			{"update", "PUT", "/api/proxy/routes/proxy-a", `{"upstreamContainer":"edited","upstreamPort":8080}`},
			{"enable", "POST", "/api/proxy/routes/proxy-a/enabled", `{"enabled":false}`},
			{"delete", "DELETE", "/api/proxy/routes/proxy-a", ""},
		} {
			t.Run(test.name, func(t *testing.T) { want(t, req(test.method, test.path, test.body), 403) })
		}
		var rows int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM proxy_routes WHERE enabled=1 AND upstream_port=80").Scan(&rows); e != nil || rows != 3 || len(fake.observed) != 2 || len(fake.uploads) != 0 {
			t.Fatal("denied action mutated stored routes or invoked SSH", e, rows, fake.observed, fake.uploads)
		}
	})
	t.Run("exact-toggle-and-revoke", func(t *testing.T) {
		setGrants(t, append(grants, authz.Grant{Action: "proxy.manage", Scope: hostScope})...)
		want(t, req("POST", "/api/proxy/routes/proxy-b/enabled", `{"enabled":false}`), 403)
		want(t, req("POST", "/api/proxy/routes/proxy-foreign/enabled", `{"enabled":false}`), 403)
		w := req("POST", "/api/proxy/routes/proxy-a/enabled", `{"enabled":false}`)
		if w.Code != 200 && w.Code != 204 {
			t.Fatalf("authorized toggle failed: %d %s", w.Code, w.Body.String())
		}
		setGrants(t, grants...)
		want(t, req("POST", "/api/proxy/routes/proxy-a/enabled", `{"enabled":true}`), 403)
		var a, b int
		if e := f.db.QueryRow("SELECT enabled FROM proxy_routes WHERE id='proxy-a'").Scan(&a); e != nil {
			t.Fatal(e)
		}
		if e := f.db.QueryRow("SELECT enabled FROM proxy_routes WHERE id='proxy-b'").Scan(&b); e != nil {
			t.Fatal(e)
		}
		if a != 0 || b != 1 || !reflect.DeepEqual(fake.uploads, []string{"s1"}) {
			t.Fatal("unexpected route effects", a, b, fake.uploads)
		}
	})
}
