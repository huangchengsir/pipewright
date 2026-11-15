package opschat

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"testing"
	"time"
)

func TestTenantPrivateSessionsAndRequests(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	if err := auth.NewService(db, nil).Bootstrap("admin", "administrator-chat-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	if err := db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
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
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Own assistant", Pages: []string{"page.opschat"}, Grants: grants})
	if err != nil {
		t.Fatal(err)
	}
	login := func(name string) context.Context {
		t.Helper()
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
		return tenancy.WithActor(ctx, user.Actor, team)
	}
	alice, bob := login("alice"), login("bob")
	m := &revokingChatModel{fakeModel: model()}
	s, _ := newService(t, db, &[32]byte{1}, nil, m)
	a, err := s.Create(alice, CreateInput{Title: "Alice private", Context: "servers"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(bob, CreateInput{Title: "Bob private", Context: "servers"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		ctx          context.Context
		own, foreign string
	}{{alice, a.ID, b.ID}, {bob, b.ID, a.ID}} {
		page, e := s.ListContext(item.ctx, "", 1, "servers")
		if e != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != item.own || page.ActiveSessionID != item.own || page.Cursor != "" {
			t.Fatal("private pagination/context", page, e)
		}
		if _, e = s.Get(item.ctx, item.foreign); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign get", e)
		}
		if _, e = s.Snapshot(item.ctx, item.foreign); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign snapshot", e)
		}
		if _, e = s.Entries(item.ctx, item.foreign, "", 10); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign entries", e)
		}
		if _, e = s.Events(item.ctx, item.foreign, "", 10); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign events", e)
		}
		if _, e = s.ListContext(item.ctx, item.foreign, 1, "servers"); !errors.Is(e, ErrInvalid) {
			t.Fatal("foreign cursor", e)
		}
		if e = s.Delete(item.ctx, item.foreign); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign delete", e)
		}
		title := "overwrite"
		if _, e = s.Patch(item.ctx, item.foreign, PatchInput{Revision: 1, Title: &title}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign patch", e)
		}
		if _, e = s.Confirm(item.ctx, item.foreign, ConfirmInput{}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("foreign confirmation", e)
		}
	}
	// Instance administration is not permission to read another employee's chat.
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	if _, e := s.Get(adminctx, a.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("admin private chat bypass", e)
	}
	decisions, e := repo.Decisions(ctx, admin.Actor, team, []tenancy.DecisionQuery{{Action: "opschat.read", ResourceID: a.ID}})
	if e != nil || decisions.Allowed[0] {
		t.Fatal("button disagrees with private data", decisions, e)
	}
	request := uuid.NewString()
	ar, e := s.Submit(alice, a.ID, TurnInput{ClientRequestID: request, Revision: a.Revision, Text: "Question A"})
	if e != nil {
		t.Fatal(e)
	}
	br, e := s.Submit(bob, b.ID, TurnInput{ClientRequestID: request, Revision: b.Revision, Text: "Question B"})
	if e != nil {
		t.Fatal("cross-user request collision", e)
	}
	if ar.ID == br.ID || ar.ClientRequestID != request || br.ClientRequestID != request {
		t.Fatal("request isolation", ar, br)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if e = db.QueryRow(`SELECT COUNT(*) FROM ops_chat_runs WHERE status IN ('queued','planning','running')`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, item := range []struct {
		ctx     context.Context
		id, run string
	}{{alice, a.ID, ar.ID}, {bob, b.ID, br.ID}} {
		snap, e := s.Snapshot(item.ctx, item.id)
		if e != nil || len(snap.Runs) != 1 || snap.Runs[0].ID != item.run || snap.Runs[0].Status != Succeeded || snap.Runs[0].ClientRequestID != request {
			t.Fatal("own worker/result", snap, e)
		}
	}
	if _, e = s.Submit(bob, a.ID, TurnInput{ClientRequestID: request, Revision: 1, Text: "Question A"}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("foreign duplicate response", e)
	}

	// A confirmation is private to the initiating session, not merely its user.
	serverID := uuid.NewString()
	remote := executor(serverID)
	s.executor = remote
	principal, _, e := tenancy.CurrentPrincipal(alice, db)
	if e != nil {
		t.Fatal(e)
	}
	for kind, resourceID := range map[string]string{"server": serverID, "credential": remote.snapshots[serverID].Server.CredentialID} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES(?,?,?,?,NULL,NULL)`, kind, resourceID, team, principal.ID); e != nil {
			t.Fatal(e)
		}
	}
	for _, action := range []string{"server.inspect", "server.use", "credential.metadata", "credential.use"} {
		grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Own assistant", ExpectedRevision: role.Revision, Pages: []string{"page.opschat", "page.servers", "page.vault"}, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.Get(alice, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	targets := []string{serverID}
	prepared, e := s.Patch(alice, a.ID, PatchInput{Revision: before.Revision, ServerIDs: &targets})
	if e != nil {
		t.Fatal(e)
	}
	mutationRun, e := s.Submit(alice, a.ID, TurnInput{Revision: prepared.Revision, ClientRequestID: uuid.NewString(), ToolID: "systemd_action", Args: []byte(`{"unit":"nginx.service","action":"restart"}`)})
	if e != nil {
		t.Fatal(e)
	}
	for limit := time.Now().Add(4 * time.Second); time.Now().Before(limit); time.Sleep(10 * time.Millisecond) {
		var status string
		if e = db.QueryRow(`SELECT status FROM ops_chat_runs WHERE id=?`, mutationRun.ID).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == AwaitingConfirmation || terminal(status) {
			break
		}
	}
	approval, e := s.approval(alice, db, a.ID, mutationRun.ID)
	if e != nil || !approval.View.Valid {
		t.Fatal("private mutation did not await confirmation", approval, e)
	}
	fresh, e := accounts.Login(ctx, "alice", "alice-chat-password")
	if e != nil {
		t.Fatal(e)
	}
	freshAlice := tenancy.WithActor(ctx, fresh.Actor, team)
	if _, e = s.Confirm(freshAlice, a.ID, ConfirmInput{RunID: mutationRun.ID, Nonce: approval.View.Nonce, Calls: approval.View.Calls}); !errors.Is(e, ErrApproval) {
		t.Fatal("old nonce crossed login session", e)
	}
	if remote.countMutation() != 0 {
		t.Fatal("old nonce executed")
	}
	renewed, e := s.ReissueConfirmation(freshAlice, a.ID, mutationRun.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Confirm(freshAlice, a.ID, ConfirmInput{RunID: mutationRun.ID, Nonce: renewed.Nonce, Calls: renewed.Calls}); e != nil {
		t.Fatal(e)
	}
	for limit := time.Now().Add(4 * time.Second); time.Now().Before(limit); time.Sleep(10 * time.Millisecond) {
		var status string
		if e = db.QueryRow(`SELECT status FROM ops_chat_runs WHERE id=?`, mutationRun.ID).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if terminal(status) {
			break
		}
	}
	if remote.countMutation() != 1 {
		t.Fatal("explicit new confirmation did not execute exactly once", remote.countMutation())
	}
	if _, e = db.Exec(`UPDATE selfupdate_control SET task_id='maintenance-test' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(alice, CreateInput{Title: "blocked"}); !errors.Is(e, configstate.ErrMaintenance) {
		t.Fatal("new session admitted during maintenance", e)
	}
	if _, e = s.Cancel(alice, a.ID, ar.ID); e != nil {
		t.Fatal("owned cancellation blocked during drain", e)
	}
	if _, e = s.Cancel(bob, a.ID, ar.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("foreign cancellation admitted during drain", e)
	}
	if _, e = db.Exec(`UPDATE selfupdate_control SET task_id='' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	// A live role change during model work cannot publish its response.
	current, e := s.Get(alice, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	m.hook = func() {
		var changeErr error
		role, changeErr = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Own assistant", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: grants[:1]})
		if changeErr != nil {
			t.Error(changeErr)
		}
	}
	revokedRun, e := s.Submit(alice, a.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: current.Revision, Text: "Revoke while planning"})
	if e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if e = db.QueryRow(`SELECT status FROM ops_chat_runs WHERE id=?`, revokedRun.ID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if terminal(state) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, e := s.Snapshot(alice, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range snap.Entries.Entries {
		if strings.Contains(entry.Text, "forbidden-model-answer") {
			t.Fatal("response released after revoke")
		}
	}
	if _, e = s.Submit(alice, a.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: current.Revision, Text: "denied"}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked submit", e)
	}
	// Revocation affects existing session data and writes, not only navigation.
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Own assistant", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: nil}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(alice, a.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked read", e)
	}
	title := "denied"
	if _, e = s.Patch(alice, a.ID, PatchInput{Revision: 1, Title: &title}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked write", e)
	}
}

type revokingChatModel struct {
	*fakeModel
	hook func()
}

func (m *revokingChatModel) Plan(ctx context.Context, in PlanRequest) (Plan, error) {
	if m.hook != nil {
		m.hook()
		return Plan{Text: "forbidden-model-answer"}, nil
	}
	return m.fakeModel.Plan(ctx, in)
}
