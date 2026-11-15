package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/dnsprovider"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func tenantRemainingPost(router http.Handler, endpoint, body, team, token, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api"+endpoint, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Pipewright-Team", team)
	r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
	if csrf != "" {
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
		r.Header.Set(headerCsrf, csrf)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

type tenantInputOnlyAI struct {
	diagStubAI
	input string
	calls int
}

func (s *tenantInputOnlyAI) accept(input string) {
	s.calls++
	s.input = input
}

func (s *tenantInputOnlyAI) CommandSuggest(_ context.Context, in ai.CommandSuggestInput) (*ai.CommandSuggestion, error) {
	s.accept(in.NL)
	return &ai.CommandSuggestion{Command: "pwd", Risk: ai.CmdRiskSafe}, nil
}

func (s *tenantInputOnlyAI) ExplainCommand(_ context.Context, in ai.ExplainCommandInput) (*ai.CommandExplanation, error) {
	s.accept(in.Command)
	return &ai.CommandExplanation{Explanation: "Prints the current path", Risk: ai.CmdRiskSafe}, nil
}

func (s *tenantInputOnlyAI) CompleteCommand(_ context.Context, in ai.CompleteCommandInput) (*ai.CompletionResult, error) {
	s.accept(in.Partial)
	return &ai.CompletionResult{Completion: "pwd"}, nil
}

func (s *tenantInputOnlyAI) GenerateCompose(_ context.Context, in ai.GenerateComposeInput) (*ai.ComposeResult, error) {
	s.accept(in.NL)
	return &ai.ComposeResult{YAML: "services: {}"}, nil
}

func (s *tenantInputOnlyAI) PreviewTerminal(_ context.Context, values []string) (*ai.TerminalPreview, error) {
	s.accept(strings.Join(values, ","))
	return &ai.TerminalPreview{Values: values}, nil
}

func TestTenantTerminalAIStatelessInputsAndSessionBoundary(t *testing.T) {
	f := newTenantAuxFixture(t)
	f.setGrants(false)
	accounts := tenancy.NewAccounts(f.db)
	session, err := accounts.Login(f.ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	model := &tenantInputOnlyAI{}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, session.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithAISettings(model))
	cases := []struct{ name, body, input, field string }{
		{"command", "{\"nl\":\"show current directory\"}", "show current directory", "command"},
		{"explain", "{\"command\":\"pwd\"}", "pwd", "explanation"},
		{"compose", "{\"nl\":\"an empty fixture\"}", "an empty fixture", "yaml"},
		{"complete", "{\"partial\":\"pw\",\"confirmed\":true,\"provider\":{\"configHash\":\"fixture\"}}", "pw", "completion"},
		{"terminal-preview", "{\"values\":[\"fixture-a\",\"fixture-b\"]}", "fixture-a,fixture-b", "values"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "/ai/" + tc.name
			before := model.calls
			for _, denied := range []struct {
				token, csrf string
				status      int
			}{
				{"", "", http.StatusUnauthorized},
				{session.Token, "", http.StatusForbidden},
			} {
				w := tenantRemainingPost(router, path, tc.body, team, denied.token, denied.csrf)
				if w.Code != denied.status || model.calls != before {
					t.Fatalf("denied request: %d, calls %d", w.Code, model.calls-before)
				}
			}
			w := tenantRemainingPost(router, path, tc.body, team, session.Token, session.CSRFToken)
			var body map[string]any
			if w.Code != 200 || model.calls != before+1 || model.input != tc.input ||
				json.Unmarshal(w.Body.Bytes(), &body) != nil || body[tc.field] == nil ||
				strings.Contains(w.Body.String(), "private-source") {
				t.Fatalf("stateless fixture request: %d, calls %d", w.Code, model.calls-before)
			}
			if (tc.name == "terminal-preview" || tc.name == "complete") && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private, user-submitted input response must not be cached")
			}
		})
	}
	if err = accounts.Logout(f.ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name+"/session-ended", func(t *testing.T) {
			before := model.calls
			w := tenantRemainingPost(router, "/ai/"+tc.name, tc.body, team, session.Token, session.CSRFToken)
			if w.Code != 401 || model.calls != before {
				t.Fatalf("ended session: %d, calls %d", w.Code, model.calls-before)
			}
		})
	}
}

type tenantSubdomainHost struct {
	target.Service
	lookups []string
}

func (s *tenantSubdomainHost) Get(_ context.Context, id string) (*target.Server, error) {
	s.lookups = append(s.lookups, id)
	// Numeric IPv6 stops at the local IPv4 validation, before any public DNS lookup or write.
	return &target.Server{ID: id, Host: "2001:db8::15"}, nil
}

func TestTenantSubdomainPreflightsExactScopeBeforeHostLookup(t *testing.T) {
	f := newTenantAuxFixture(t)
	accounts := tenancy.NewAccounts(f.db)
	admin, err := accounts.Login(f.ctx, "admin", "auxiliary-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	session, err := accounts.Login(f.ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	v := vault.New(f.db, testMasterKey())
	dns := dnsprovider.New(f.db, v, nil)
	creator := dns.(interface {
		CreateWithToken(context.Context, dnsprovider.CreateInput, string) (*dnsprovider.Provider, error)
	})
	create := func(name string) *dnsprovider.Provider {
		p, err := creator.CreateWithToken(tenancy.WithActor(context.Background(), admin.Actor, team),
			dnsprovider.CreateInput{Type: dnsprovider.TypeCloudflare, Name: name, BaseDomain: name + ".invalid"}, "local-fixture-only")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	selected, sibling := create("selected"), create("sibling")
	host := &tenantSubdomainHost{}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, session.Actor.ModeEpoch),
		WithTenancyAccounts(accounts), WithDNSProviders(dns), WithServers(host), WithProxy(proxy.New(f.db, host)))
	post := func(server, provider string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"serverId": server, "providerId": provider, "upstreamContainer": "web", "upstreamPort": 80})
		return tenantRemainingPost(router, "/proxy/subdomains", string(body), team, session.Token, session.CSRFToken)
	}
	var roleID string
	var revision uint64
	if err = f.db.QueryRow("SELECT id, revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'", team).Scan(&roleID, &revision); err != nil {
		t.Fatal(err)
	}
	grant := func(action, id string) authz.Grant {
		return authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{id}}}
	}
	prerequisites := []authz.Grant{grant("server.inspect", "s1"), grant("proxy.read", "s1"), grant("credential.metadata", selected.CredentialID)}
	grants := []authz.Grant{grant("dns.use", selected.ID), grant("credential.use", selected.CredentialID), grant("server.use", "s1"), grant("proxy.manage", "s1")}
	save := func(t *testing.T, grants ...authz.Grant) {
		t.Helper()
		role, err := tenancy.NewRepository(f.db).SaveRole(context.Background(), admin.Actor, team, roleID, tenancy.RoleInput{
			Name: "Auxiliary", Pages: []string{"page.servers", "page.containers", "page.vault"}, ExpectedRevision: revision,
			Grants: append(append([]authz.Grant(nil), prerequisites...), grants...),
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = role.Revision
	}
	t.Run("missing-action-before-host-lookup", func(t *testing.T) {
		for _, g := range [][]authz.Grant{nil, grants[:1], grants[:2], grants[:3]} {
			save(t, g...)
			if w := post("s1", selected.ID); w.Code != 403 || len(host.lookups) != 0 {
				t.Fatalf("incomplete grant reached host: %d, lookups %v", w.Code, host.lookups)
			}
		}
	})
	t.Run("selected-only-and-revoked", func(t *testing.T) {
		save(t, grants...)
		for _, pair := range [][2]string{{"s1", sibling.ID}, {"s2", selected.ID}, {"foreign-server", selected.ID}, {"missing", selected.ID}} {
			if w := post(pair[0], pair[1]); w.Code != 403 || len(host.lookups) != 0 {
				t.Fatalf("out of scope reached host: %d, lookups %v", w.Code, host.lookups)
			}
		}
		if w := post("s1", selected.ID); w.Code != 422 || len(host.lookups) != 1 || host.lookups[0] != "s1" {
			t.Fatalf("selected request did not reach only its IPv4 validation: %d, lookups %v", w.Code, host.lookups)
		}
		save(t, grants[:3]...)
		if w := post("s1", selected.ID); w.Code != 403 || len(host.lookups) != 1 {
			t.Fatalf("revoked request reached host: %d, lookups %v", w.Code, host.lookups)
		}
	})
}
