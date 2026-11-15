package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"reflect"
	"testing"
	"time"
)

func TestTenantKubernetesConfigurationScopesAndRejectedWrites(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	f := newFixture(t, st)
	second, e := f.s.SaveConnection(ctx, "", 0, ConnectionInput{Name: "test cluster", CredentialID: f.c.ID, Enabled: true, Profile: testProfile(), Executors: []Executor{{Kind: "local"}}})
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := f.s.SaveConnection(ctx, "", 0, ConnectionInput{Name: "test cluster", CredentialID: f.c.ID, Enabled: true, Profile: testProfile(), Executors: []Executor{{Kind: "local"}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(st.DB, nil).Bootstrap("admin", "admin-kubernetes-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(st.DB)
	if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	if e = st.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(st.DB)
	admin, e := accounts.Login(ctx, "admin", "admin-kubernetes-password")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='kubernetes' AND resource_id=?`, other.ID, foreign.ID); e != nil {
		t.Fatal(e)
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Selected cluster metadata", Pages: []string{"page.kubernetes"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "kubernetes.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "kubernetes_credential.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "viewer", DisplayName: "Viewer", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "kubernetes", ID: f.conn.ID}, {Kind: "kubernetes_credential", ID: f.c.ID}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "viewer", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "viewer-kubernetes-password")
	if e != nil {
		t.Fatal(e)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	rows, e := f.s.ListConnections(actor)
	if e != nil || len(rows) != 1 || rows[0].ID != f.conn.ID {
		t.Fatal("exact connection IDs", rows, e)
	}
	creds, e := f.s.ListCredentials(actor)
	if e != nil || len(creds) != 1 || creds[0].ID != f.c.ID {
		t.Fatal("exact credential IDs", creds, e)
	}
	if _, e = f.s.GetConnection(actor, f.conn.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.GetGrant(actor, f.grant.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("connection metadata did not imply project grant visibility", e)
	}
	if grants, e := f.s.ListGrants(actor, f.conn.ID); e != nil || len(grants) != 0 {
		t.Fatal("grant project intersection", grants, e)
	}
	for _, id := range []string{second.ID, foreign.ID, "unknown"} {
		if _, e = f.s.GetConnection(actor, id); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("detail range", id, e)
		}
		if _, e = f.s.ListGrants(actor, id); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("child range", id, e)
		}
	}
	snapshot := func() string {
		var body string
		if e := st.DB.QueryRow(`SELECT connection_json FROM k8s_connections WHERE id=?`, f.conn.ID).Scan(&body); e != nil {
			t.Fatal(e)
		}
		return body
	}
	for _, previewID := range []string{"", f.c.ID, "unknown"} {
		if _, e = f.s.PreviewCredentialFor(actor, previewID, CredentialInput{}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("unauthorized preview must fail before parsing", previewID, e)
		}
	}
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	for _, previewID := range []string{"", f.c.ID} {
		if _, e = f.s.PreviewCredentialFor(adminCtx, previewID, credentialInput(t)); e != nil {
			t.Fatal("authorized preview", e)
		}
	}
	before := snapshot()
	if _, e = f.s.SaveConnection(actor, f.conn.ID, f.conn.Revision, ConnectionInput{Name: "changed", CredentialID: f.c.ID, Enabled: true, Profile: testProfile(), Executors: []Executor{{Kind: "local"}}}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("edit missing", e)
	}
	if e = f.s.DeleteConnection(actor, f.conn.ID, f.conn.Revision); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("delete missing", e)
	}
	if _, e = f.s.SaveCredential(actor, "", 0, credentialInput(t)); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("create missing", e)
	}
	if _, e = f.s.SetCredentialEnabled(actor, f.c.ID, f.c.Revision, false); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("credential edit missing", e)
	}
	if e = f.s.DeleteGrant(actor, f.grant.ID, f.grant.Revision); !errors.Is(e, ErrNotFound) {
		t.Fatal("grant edit missing", e)
	}
	var connections, credentials, grants int
	st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_connections`).Scan(&connections)
	st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_credentials`).Scan(&credentials)
	st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_deploy_grants`).Scan(&grants)
	if snapshot() != before || connections != 3 || credentials != 1 || grants != 1 {
		t.Fatal("denied writes changed rows", connections, credentials, grants)
	}
	if _, e = f.s.ListConnections(ctx); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("missing principal", e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ListConnections(actor); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked session", e)
	}
}

func TestTenantKubernetesRuntimeAndRecentPassword(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	f := newFixture(t, st)
	if e := auth.NewService(st.DB, nil).Bootstrap("admin", "admin-k8s-runtime-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(st.DB)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	st.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(st.DB)
	admin, e := accounts.Login(ctx, "admin", "admin-k8s-runtime-password")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('env-production',?,'project-1','production','production',1)`, team); e != nil {
		t.Fatal(e)
	}
	actor := tenancy.WithActor(ctx, admin.Actor, team)
	if _, e = f.s.Authorize(actor, f.target); e != nil {
		t.Fatal("authorized runtime", e)
	}
	if _, e = f.s.Authorize(ctx, f.target); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("runtime without principal", e)
	}
	called := false
	if e = f.s.WithAdminMutation(actor, admin.Token, func(context.Context) error { called = true; return nil }); e != nil || !called {
		t.Fatal("recent authenticated mutation", e)
	}
	if _, e = st.DB.Exec(`UPDATE access_sessions SET password_verified_at=? WHERE token_hash=?`, time.Now().Add(-time.Hour).Unix(), admin.Actor.SessionHash); e != nil {
		t.Fatal(e)
	}
	called = false
	if e = f.s.WithAdminMutation(actor, admin.Token, func(context.Context) error { called = true; return nil }); !errors.Is(e, ErrRecentPassword) || called {
		t.Fatal("expired step-up", e, called)
	}
	if e = f.s.WithAdminMutation(actor, "different-token", func(context.Context) error { called = true; return nil }); !errors.Is(e, ErrSession) || called {
		t.Fatal("token/context mismatch", e, called)
	}
	grants := []authz.Grant{{Action: "kubernetes.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "kubernetes.use", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "kubernetes_credential.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "kubernetes_credential.use", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "kubernetes.deploy", Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentPolicy: "test"}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Test clusters", Pages: []string{"page.kubernetes"}, RequiresSelectedBinding: true, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "deployer", DisplayName: "Deployer", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "kubernetes", ID: f.conn.ID}, {Kind: "kubernetes_credential", ID: f.c.ID}}, ProjectIDs: []string{"project-1"}, EnvironmentIDs: []string{"env-production"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "deployer", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "deployer-k8s-password")
	if e != nil {
		t.Fatal(e)
	}
	member := tenancy.WithActor(ctx, login.Actor, team)
	if e = tenancy.CheckEnvironmentAction(member, st.DB, "kubernetes.deploy", "kubernetes", f.conn.ID, "project-1", "production"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("production purpose rejected independently", e)
	}
	if _, e = st.DB.Exec(`UPDATE access_environments SET purpose='test' WHERE id='env-production'`); e != nil {
		t.Fatal(e)
	}
	if e = tenancy.CheckEnvironmentAction(member, st.DB, "kubernetes.deploy", "kubernetes", f.conn.ID, "project-1", "production"); e != nil {
		t.Fatal("explicit test purpose and assigned environment", e)
	}
	if _, e = f.s.Authorize(member, f.target); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("ordinary user used controller-local executor", e)
	}
	if _, e = st.DB.Exec(`UPDATE access_environments SET purpose='production' WHERE id='env-production'`); e != nil {
		t.Fatal(e)
	}

	if _, e = f.s.Authorize(member, f.target); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("test policy admitted production", e)
	}
	changed := f.target
	changed.Environment = ""
	if _, e = f.s.Authorize(member, changed); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("empty environment", e)
	}
	changed.Environment = "forged"
	if _, e = f.s.Authorize(member, changed); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("forged environment", e)
	}
	var operations int
	st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_operations`).Scan(&operations)
	if operations != 0 {
		t.Fatal("denial created runtime", operations)
	}
	if e = accounts.Logout(ctx, admin.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Authorize(actor, f.target); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked runtime principal", e)
	}
}

func TestReleaseAuthorizationRequestContextJSON(t *testing.T) {
	field, _ := reflect.TypeFor[ReleaseAuthorizationRequest]().FieldByName("Context")
	if string(field.Tag) != `json:"context"` {
		t.Fatalf("invalid context tag: %q", field.Tag)
	}
	original := ReleaseAuthorizationRequest{ProjectID: "project", Context: "cluster-context"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["context"] != original.Context {
		t.Fatal("missing wire context")
	}
	var restored ReleaseAuthorizationRequest
	if err = json.Unmarshal(data, &restored); err != nil || restored.Context != original.Context {
		t.Fatal("roundtrip", err)
	}
}
