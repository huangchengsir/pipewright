package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestTenantRepositoryOAuthPreservesTeamAndCredentialScope(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	employee, e := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if e != nil {
		t.Fatal(e)
	}
	var team, foreignTeam, roleID string
	var revision uint64
	if e = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); e != nil {
		t.Fatal(e)
	}
	if e = f.db.QueryRow("SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id='foreign'").Scan(&foreignTeam); e != nil {
		t.Fatal(e)
	}
	if e = f.db.QueryRow("SELECT id,revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'", team).Scan(&roleID, &revision); e != nil {
		t.Fatal(e)
	}
	v := vault.New(f.db, testMasterKey())
	// A documentation IP keeps endpoint validation independent of DNS.
	const fixtureHost = "192.0.2.13"
	mockCalls := 0
	transport := settingsTransportFunc(func(req *http.Request) (*http.Response, error) {
		mockCalls++
		var body string
		if req.URL.Host != fixtureHost {
			t.Fatal("unexpected mock provider", req.URL.Host)
		}
		switch req.URL.Path {
		case "/oauth/token":
			body = `{"access_token":"fixture-bound-repository-token","token_type":"bearer"}`
		case "/api/v4/user":
			body = `{"username":"fixture-user","id":123}`
		default:
			t.Fatalf("unexpected mock provider route: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	oauthSvc := oauth.New(f.db, v, &http.Client{Transport: transport})
	secret := "local-fixture"
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	if _, e = oauthSvc.SaveApp(adminCtx, oauth.SaveAppInput{Provider: "custom", ClientID: "fixture-app", ClientSecret: &secret, BaseURL: "https://" + fixtureHost, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	makeCredential := func(tenant, name string) string {
		c, e := vault.WithContext(tenancy.WithActor(ctx, admin.Actor, tenant), v).Create(vault.CreateInput{Name: name, Type: vault.TypeGitToken, Secret: "fixture-input"})
		if e != nil {
			t.Fatal(e)
		}
		return c.ID
	}
	selected := makeCredential(team, "selected repository credential")
	sibling := makeCredential(team, "sibling repository credential")
	foreign := makeCredential(foreignTeam, "foreign repository credential")
	grant := func(edit bool) {
		t.Helper()
		scope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "credential", ID: selected}}}
		grants := []authz.Grant{{Action: "credential.metadata", Scope: scope}}
		if edit {
			grants = append(grants, authz.Grant{Action: "credential.edit", Scope: scope})
		}
		role, e := tenancy.NewRepository(f.db).SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{Name: "Auxiliary", Pages: []string{"page.vault"}, ExpectedRevision: revision, Grants: grants})
		if e != nil {
			t.Fatal(e)
		}
		revision = role.Revision
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, employee.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithVault(v), WithOAuth(oauthSvc))
	request := func(session tenancy.Session, method, path, body, teamHeader string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://pipewright.invalid"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", teamHeader)
		r.Header.Set(headerCsrf, session.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: session.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: session.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	stateOf := func(t *testing.T, raw string) string {
		t.Helper()
		u, e := url.Parse(raw)
		if e != nil || u.Host != fixtureHost || u.Query().Get("state") == "" {
			t.Fatal("invalid redirect state", e)
		}
		return u.Query().Get("state")
	}
	base := "/api/oauth/custom"
	callback := func(actor tenancy.Session, state string) *httptest.ResponseRecorder {
		return request(actor, "GET", base+"/callback?state="+url.QueryEscape(state)+"&code=fixture-code", "", "")
	}
	grant(false)
	t.Run("new-credential-bound-to-selected-team", func(t *testing.T) {
		begin := base + "/authorize?teamId=" + url.QueryEscape(team)
		w := request(employee, "GET", begin, "", "")
		if w.Code != 403 || mockCalls != 0 {
			t.Fatal("employee received instance connect redirect", w.Code, mockCalls)
		}
		w = request(admin, "GET", begin, "", "")
		if w.Code != 302 {
			t.Fatalf("selected browser authorization: %d %s", w.Code, w.Body.String())
		}
		state := stateOf(t, w.Header().Get("Location"))
		w = callback(admin, state)
		if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "connected=custom") || mockCalls != 2 {
			t.Fatal("matched fixture callback failed", w.Code, w.Header().Get("Location"), mockCalls)
		}
		var stored, member string
		e := f.db.QueryRow(`SELECT o.tenant_id,o.owner_id FROM access_ownership o JOIN credentials c ON o.resource_id=c.id WHERE o.resource_kind='credential' AND c.name='custom:fixture-user'`).Scan(&stored, &member)
		if e != nil || stored != team || member != admin.Actor.UserID {
			t.Fatal("created credential lacked captured ownership", e)
		}
	})
	t.Run("reconnect-rechecks-role-and-session", func(t *testing.T) {
		connect := func(id string) *httptest.ResponseRecorder {
			return request(employee, "POST", "/api/credentials/"+id+"/oauth/reconnect", `{"provider":"custom","revision":0,"adoptExisting":true}`, team)
		}
		if w := connect(selected); w.Code != 403 {
			t.Fatal("read permission issued an edit redirect", w.Code)
		}
		grant(true)
		for _, id := range []string{sibling, foreign, "missing"} {
			if w := connect(id); w.Code != 403 {
				t.Fatal("unselected credential lookup", w.Code)
			}
		}
		getState := func() string {
			w := connect(selected)
			var payload struct {
				AuthorizeURL string `json:"authorizeUrl"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil {
				t.Fatalf("selected reconnect: %d %s", w.Code, w.Body.String())
			}
			return stateOf(t, payload.AuthorizeURL)
		}
		previous := mockCalls
		state := getState()
		w := callback(admin, state)
		if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "invalid_state") || mockCalls != previous {
			t.Fatal("wrong browser session queried provider", w.Code, w.Header())
		}
		state = getState()
		grant(false)
		w = callback(employee, state)
		if w.Code != 302 || strings.Contains(w.Header().Get("Location"), "connected=") || mockCalls != previous {
			t.Fatal("revoked reconnect contacted provider", w.Code, w.Header())
		}
		var bound int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM oauth_credentials WHERE credential_id=?", selected).Scan(&bound); e != nil || bound != 0 {
			t.Fatal("disabled attempt changed binding", e, bound)
		}
	})
}
