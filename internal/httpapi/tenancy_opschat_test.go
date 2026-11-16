package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/opschat"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func TestTenantOpsChatHTTPPrivateActions(t *testing.T) {
	ctx := context.Background()
	svc, db, exec, _, model := opsFixture(t)
	if err := auth.NewService(db, nil).Bootstrap("admin", "administrator-chat-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "administrator-chat-password")
	if err != nil {
		t.Fatal(err)
	}
	grants := []authz.Grant{}
	for _, action := range []string{"read", "create", "edit", "delete", "submit", "confirm", "cancel"} {
		grants = append(grants, authz.Grant{Action: "opschat." + action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	input := tenancy.RoleInput{Name: "Private assistant", Pages: []string{"page.opschat"}, Grants: grants}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	type login struct {
		token, csrf string
		actor       tenancy.Actor
	}
	member := func(name string) login {
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
		if e != nil {
			t.Fatal(e)
		}
		user, e := accounts.Login(ctx, name, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		user, e = accounts.ChangePassword(ctx, user.Token, initial.Password, name+"-chat-password")
		if e != nil {
			t.Fatal(e)
		}
		return login{user.Token, user.CSRFToken, user.Actor}
	}
	alice, bob := member("alice"), member("bob")
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithOpsChat(svc))
	request := func(user login, method, path, body string, csrf bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/ai/ops"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", team)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: user.token})
		if csrf {
			r.Header.Set(headerCsrf, user.csrf)
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: user.csrf})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	create := func(user login, title string) string {
		w := request(user, "POST", "/sessions", `{"title":"`+title+`","context":"servers"}`, true, 201)
		var s opschat.Session
		if json.Unmarshal(w.Body.Bytes(), &s) != nil || s.ID == "" {
			t.Fatal(w.Body.String())
		}
		return s.ID
	}
	a, b := create(alice, "Alice private"), create(bob, "Bob private")
	request(alice, "POST", "/sessions", `{}`, false, 403)
	request(alice, "GET", "/capabilities", "", false, 200)
	for _, row := range []struct {
		u            login
		own, foreign string
	}{{alice, a, b}, {bob, b, a}} {
		w := request(row.u, "GET", "/sessions?limit=1&context=servers", "", false, 200)
		if !strings.Contains(w.Body.String(), row.own) || strings.Contains(w.Body.String(), row.foreign) {
			t.Fatal("list leakage", w.Body.String())
		}
		request(row.u, "GET", "/sessions/"+row.own, "", false, 200)
		for _, tail := range []string{"", "/entries", "/events", "/analysis/preview?callIds=" + uuid.NewString(), "/calls/" + uuid.NewString()} {
			request(row.u, "GET", "/sessions/"+row.foreign+tail, "", false, 403)
		}
		for _, tail := range []string{"/activate", "/turns", "/retry", "/calls/confirm", "/analysis", "/runs/" + uuid.NewString() + "/cancel", "/runs/" + uuid.NewString() + "/confirmation"} {
			request(row.u, "POST", "/sessions/"+row.foreign+tail, `{}`, true, 403)
		}
		request(row.u, "PATCH", "/sessions/"+row.foreign, `{"title":"stolen"}`, true, 403)
		request(row.u, "DELETE", "/sessions/"+row.foreign, "", true, 403)
	}
	request(login{admin.Token, admin.CSRFToken, admin.Actor}, "GET", "/sessions/"+a, "", false, 403)
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM ops_chat_sessions`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if exec.calls.Load() != 0 || model.calls.Load() != 0 {
		t.Fatal("denied operation reached external side effect")
	}
	input.Grants = []authz.Grant{{Action: "opschat.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
	input.ExpectedRevision = role.Revision
	if role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input); err != nil {
		t.Fatal(err)
	}
	request(alice, "GET", "/sessions/"+a, "", false, 200)
	request(alice, "PATCH", "/sessions/"+a, `{"title":"blocked"}`, true, 403)
	request(alice, "POST", "/sessions/"+a+"/turns", `{}`, true, 403)
	request(alice, "DELETE", "/sessions/"+a, "", true, 403)
	streamRouter := chi.NewRouter()
	streamRouter.Get("/sessions/{id}/events", opsEventsHandler(svc, tenancy.NewAuthenticator(accounts, mode.Epoch), 10*time.Millisecond))
	streamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamRouter.ServeHTTP(w, r.WithContext(tenancy.WithActor(r.Context(), alice.actor, team)))
	}))
	defer streamServer.Close()
	streamRequest, _ := http.NewRequest("GET", streamServer.URL+"/sessions/"+a+"/events", nil)
	streamRequest.AddCookie(&http.Cookie{Name: cookieSession, Value: alice.token})
	client := &http.Client{Timeout: 2 * time.Second}
	streamResponse, e := client.Do(streamRequest)
	if e != nil {
		t.Fatal(e)
	}
	defer streamResponse.Body.Close()
	reader := bufio.NewReader(streamResponse.Body)
	ready, e := reader.ReadString('\n')
	if e != nil || !strings.Contains(ready, "ready") {
		t.Fatal(ready, e)
	}
	input.Grants = nil
	input.ExpectedRevision = role.Revision
	if role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input); err != nil {
		t.Fatal(err)
	}
	var streamTail strings.Builder
	for {
		line, e := reader.ReadString('\n')
		streamTail.WriteString(line)
		if e != nil {
			break
		}
	}
	if !strings.Contains(streamTail.String(), "access_denied") {
		t.Fatal("stream failed to stop on role revocation", streamTail.String())
	}
	request(alice, "GET", "/sessions/"+a, "", false, 403)
	request(alice, "GET", "/capabilities", "", false, 403)
	request(alice, "GET", "/tools", "", false, 403)
	if exec.calls.Load() != 0 || model.calls.Load() != 0 {
		t.Fatal("revoked operation reached external side effect")
	}
}
