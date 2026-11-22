package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/workspace"
)

func TestProjectDisplayModeBelongsToAccountNotTeam(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	state, err := tenancy.NewRepository(db).ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	alice, err := accounts.Login(ctx, "alice", initial.Password)
	if err != nil {
		t.Fatal(err)
	}

	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, state.Epoch), WithTenancyAccounts(accounts), WithProjectDisplayMode(db))
	request := func(method, endpoint, body, token, csrf, team string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, endpoint, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		r.Header.Set(headerCsrf, csrf)
		if team != "" {
			r.Header.Set("X-Pipewright-Team", team)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, r)
		return res
	}
	const modePath = "/api/account/project-mode"
	const multi = `{"mode":"multi"}`
	if res := request(http.MethodPut, modePath, multi, admin.Token, admin.CSRFToken, ""); res.Code != 200 {
		t.Fatalf("admin preference save: %d", res.Code)
	}
	if res := request(http.MethodGet, modePath, "", alice.Token, "", ""); res.Code != 403 {
		t.Fatalf("initial-password account preference: %d", res.Code)
	}
	password, _ := json.Marshal(map[string]string{"currentPassword": initial.Password, "newPassword": "alice-updated-password"})
	rotated := request(http.MethodPost, "/api/access/self/password", string(password), alice.Token, alice.CSRFToken, "")
	if rotated.Code != 200 {
		t.Fatalf("password setup: %d", rotated.Code)
	}
	alice, err = accounts.Login(ctx, "alice", "alice-updated-password")
	if err != nil {
		t.Fatal(err)
	}
	if res := request(http.MethodGet, modePath, "", alice.Token, "", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"mode":"single"`) {
		t.Fatalf("employee inherited different account preference: %d %s", res.Code, res.Body.String())
	}
	if res := request(http.MethodGet, modePath, "", admin.Token, "", "another-team"); res.Code != 200 || !strings.Contains(res.Body.String(), `"mode":"multi"`) {
		t.Fatalf("admin preference depends on team header: %d", res.Code)
	}
}

func TestWorkspaceMountedForLegacyUser(t *testing.T) {
	st := testStoreAuth(t)
	authn := auth.NewService(st.DB, nil)
	if err := authn.Bootstrap("admin", "testpass"); err != nil {
		t.Fatal(err)
	}
	svc := workspace.New(st.DB, project.New(st.DB, nil, nil))
	server := httptest.NewServer(New(testWebFSAuth(), authn, WithWorkspace(svc)))
	defer server.Close()
	client := newTestClient(t)
	url := server.URL + "/api/workspace"
	request := func(method, suffix, csrf, body string, code int, target any) {
		t.Helper()
		res := doJSON(t, client, method, url+suffix, csrf, body)
		defer res.Body.Close()
		if res.StatusCode != code {
			t.Fatalf("%s %s: %d, wanted %d", method, suffix, res.StatusCode, code)
		}
		if target != nil {
			if err := json.NewDecoder(res.Body).Decode(target); err != nil {
				t.Fatal(err)
			}
		}
	}
	request(http.MethodGet, "", "", "", 401, nil)
	csrf := loginWithClient(t, client, server.URL)
	request(http.MethodPost, "/business-projects", "", `{"name":"Retail"}`, 403, nil)
	var business workspace.BusinessProject
	request(http.MethodPost, "/business-projects", csrf, `{"name":"Retail"}`, 201, &business)
	if business.ID == "" || business.Revision != 1 {
		t.Fatalf("invalid business project: %+v", business)
	}
	var item workspace.Product
	request(http.MethodPost, "/business-projects/"+business.ID+"/products", csrf, `{"name":"Store"}`, 201, &item)
	var directory workspace.Directory
	request(http.MethodGet, "", "", "", 200, &directory)
	if len(directory.BusinessProjects) != 1 || len(directory.BusinessProjects[0].Products) != 1 || directory.BusinessProjects[0].Products[0].ID != item.ID {
		t.Fatalf("missing workspace grouping: %+v", directory)
	}
	request(http.MethodDelete, "/business-projects/"+business.ID+"?expectedRevision=1", csrf, "", 409, nil)
	request(http.MethodDelete, "/products/"+item.ID+"?expectedRevision=1", csrf, "", 204, nil)
	request(http.MethodDelete, "/business-projects/"+business.ID+"?expectedRevision=1", csrf, "", 204, nil)
}
