package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type stubProbe struct{ before func() error }

func (s *stubProbe) Probe(context.Context, string, string, string) (string, error) {
	if s.before != nil {
		return "main", s.before()
	}
	return "main", nil
}

func workspaceFixture(t *testing.T) (*Service, *sql.DB, *stubProbe, project.CreateInput) {
	t.Helper()
	db := storetest.OpenDB(t)
	key := [32]byte{1, 2, 3}
	v := vault.New(db, &key)
	c, err := v.Create(vault.CreateInput{Name: "fixture credential", Type: vault.TypeGitToken, Secret: "workspace-test-fixture-not-a-secret"})
	if err != nil {
		t.Fatal(err)
	}
	probe := &stubProbe{}
	in := project.CreateInput{Name: "Frontend", RepoURL: "https://example.test/front.git", CredentialID: c.ID}
	return New(db, project.New(db, v, probe)), db, probe, in
}

func productFixture(t *testing.T, ctx context.Context, s *Service, name string) (BusinessProject, Product) {
	t.Helper()
	b, err := s.CreateBusinessProject(ctx, name+" Business")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProduct(ctx, b.ID, name+" Product")
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

func TestWorkspaceAppLifecyclePreservesLegacyIDAndRevision(t *testing.T) {
	ctx := t.Context()
	s, _, _, in := workspaceFixture(t)
	b, p := productFixture(t, ctx, s, "Primary")
	_, q := productFixture(t, ctx, s, "Secondary")
	legacy, err := s.projects.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	unassigned, err := s.ListApplications(ctx, ApplicationFilter{Unassigned: true})
	if err != nil || unassigned.Total != 1 || unassigned.Items[0].Project.ID != legacy.ID || unassigned.Items[0].Revision != 0 {
		t.Fatalf("legacy application: %#v, %v", unassigned, err)
	}
	first, err := s.AssignApplication(ctx, legacy.ID, p.ID, 0)
	if err != nil || first.ProductID != p.ID || first.Revision != 1 {
		t.Fatal(first, err)
	}
	in.Name = "Backend"
	app, err := s.CreateApplication(ctx, p.ID, in)
	if err != nil || app.ProductID != p.ID || app.Project.ID != app.ApplicationID {
		t.Fatal(app, err)
	}
	page, err := s.ListApplications(ctx, ApplicationFilter{ProductID: p.ID, Query: "BACK", Page: 1, PageSize: 1})
	if err != nil || page.Total != 1 || page.Items[0].Project.ID != app.Project.ID {
		t.Fatal(page, err)
	}
	if err = s.DeleteBusinessProject(ctx, b.ID, b.Revision); !errors.Is(err, ErrNotEmpty) {
		t.Fatal(err)
	}
	if err = s.DeleteProduct(ctx, p.ID, p.Revision); !errors.Is(err, ErrNotEmpty) {
		t.Fatal(err)
	}

	moved, err := s.AssignApplication(ctx, legacy.ID, q.ID, 1)
	if err != nil || moved.Revision != 2 {
		t.Fatal(moved, err)
	}
	if _, err = s.AssignApplication(ctx, legacy.ID, p.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	unlinked, err := s.AssignApplication(ctx, legacy.ID, "", 2)
	if err != nil || unlinked.Revision != 3 {
		t.Fatal(unlinked, err)
	}
	if _, err = s.AssignApplication(ctx, legacy.ID, p.ID, 0); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	current, err := s.projects.Get(ctx, legacy.ID)
	if err != nil || *current != *legacy {
		t.Fatal(current, err)
	}

	if err = s.projects.Delete(ctx, app.Project.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteProduct(ctx, p.ID, p.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteBusinessProject(ctx, b.ID, b.Revision); err != nil {
		t.Fatal(err)
	}
	unassigned, err = s.ListApplications(ctx, ApplicationFilter{ApplicationID: legacy.ID, Unassigned: true})
	if err != nil || unassigned.Total != 1 || unassigned.Items[0].Revision != 3 {
		t.Fatal(unassigned, err)
	}
}

func TestWorkspaceCreateRejectsRemovedProductWithoutOrphan(t *testing.T) {
	ctx := t.Context()
	s, _, probe, in := workspaceFixture(t)
	_, p := productFixture(t, ctx, s, "Removed")
	probe.before = func() error { return s.DeleteProduct(ctx, p.ID, p.Revision) }
	if _, err := s.CreateApplication(ctx, p.ID, in); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	legacy, err := s.projects.List(ctx)
	if err != nil || len(legacy) != 0 {
		t.Fatal(legacy, err)
	}
}

func TestWorkspaceTenantScopeIntersectsParentProductAndApplication(t *testing.T) {
	ctx := t.Context()
	s, db, _, in := workspaceFixture(t)
	b, p := productFixture(t, ctx, s, "Imported")
	otherProduct, err := s.CreateProduct(ctx, b.ID, "Hidden Product")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.CreateApplication(ctx, p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "Internal"
	hidden, err := s.CreateApplication(ctx, p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	const password = "fixture-based-owner-pass"
	if err = auth.NewService(db, nil).Bootstrap("fixture-owner", password); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	owner, err := accounts.Login(ctx, "fixture-owner", password)
	if err != nil {
		t.Fatal(err)
	}
	var defaultTeam string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&defaultTeam); err != nil {
		t.Fatal(err)
	}
	foreignTeam, err := repo.CreateTeam(ctx, "Foreign")
	if err != nil {
		t.Fatal(err)
	}
	_, foreignProduct := productFixture(t, tenancy.WithActor(ctx, owner.Actor, foreignTeam), s, "Foreign")

	grants := []authz.Grant{}
	for _, action := range []string{"business_project.read", "product.read", "project.read", "project.edit", "product.manage"} {
		grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	role, err := repo.SaveRole(ctx, owner.Actor, defaultTeam, "", tenancy.RoleInput{
		Name: "Scoped reader", Pages: []string{"page.projects"}, RequiresSelectedBinding: true, Grants: grants,
	})
	if err != nil {
		t.Fatal(err)
	}
	refs := []authz.ResourceRef{{Kind: "business_project", ID: b.ID}, {Kind: "product", ID: p.ID}, {Kind: "project", ID: visible.Project.ID}}
	user, err := accounts.Create(ctx, owner.Actor, tenancy.CreateUserInput{
		Username: "workspace-operator", DisplayName: "Workspace Operator",
		Assignments: []tenancy.MemberAssignment{{TeamID: defaultTeam, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: refs}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "workspace-operator", user.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, user.Password, "workspace-operator-pass")
	if err != nil {
		t.Fatal(err)
	}
	scopedCtx := tenancy.WithActor(ctx, login.Actor, defaultTeam)
	directory, err := s.List(scopedCtx)
	if err != nil || len(directory.BusinessProjects) != 1 || directory.BusinessProjects[0].ID != b.ID || len(directory.BusinessProjects[0].Products) != 1 {
		t.Fatalf("directory: %#v %v", directory, err)
	}
	if directory.BusinessProjects[0].Products[0].ID != p.ID {
		t.Fatal(directory)
	}
	visibleApps, err := s.ListApplications(scopedCtx, ApplicationFilter{ProductID: p.ID, PageSize: 1})
	if err != nil || visibleApps.Total != 1 || visibleApps.Items[0].Project.ID != visible.Project.ID {
		t.Fatal(visibleApps, err)
	}
	filtered, err := s.ListApplications(scopedCtx, ApplicationFilter{ProductID: p.ID, Query: "internal"})
	if err != nil || filtered.Total != 0 {
		t.Fatal(filtered, err)
	}
	for _, id := range []string{otherProduct.ID, foreignProduct.ID} {
		if _, err = s.GetProduct(scopedCtx, id); !errors.Is(err, ErrNotFound) {
			t.Fatal(id, err)
		}
	}
	if _, err = s.AssignApplication(scopedCtx, hidden.Project.ID, "", 1); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("hidden application", err)
	}
	if err = s.DeleteProduct(scopedCtx, p.ID, p.Revision); !errors.Is(err, ErrNotEmpty) {
		t.Fatal("visible but non-empty product", err)
	}
	if _, err = s.AssignApplication(scopedCtx, visible.Project.ID, foreignProduct.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign product", err)
	}
	ownerCtx := tenancy.WithActor(ctx, owner.Actor, defaultTeam)
	current, err := s.ListApplications(ownerCtx, ApplicationFilter{ProductID: p.ID, IdentityOnly: true})
	if err != nil || current.Total != 2 {
		t.Fatal(fmt.Sprintf("%#v", current), err)
	}
}

func TestWorkspaceRecentRunAndProductionDeploymentRemainIndependent(t *testing.T) {
	ctx := t.Context()
	s, db, _, in := workspaceFixture(t)
	_, p := productFixture(t, ctx, s, "Release")
	app, err := s.CreateApplication(ctx, p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct{ id, status, commit, stamp string }{
		{"deployed", "success", "1111111111111111111111111111111111111111", "2026-11-20T00:00:00Z"},
		{"pending", "waiting_approval", "2222222222222222222222222222222222222222", "2026-11-21T00:00:00Z"},
	} {
		if _, err = db.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,resolved_environment,resolved_target_server_ids,params_json,created_at)
			VALUES(?,?,?,'manual','main',?,'fixture','production','[]','{}',?)`, record.id, app.Project.ID, record.status, record.commit, record.stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO deploy_targets(id,run_id,server_id,server_name,status,message,started_at,finished_at)
			VALUES('target','deployed','fixture','fixture','success','','2026-11-20T00:00:00Z','2026-11-20T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	const deployedCommit = "3333333333333333333333333333333333333333"
	if _, err = db.Exec(`UPDATE pipeline_runs SET resolved_commit_sha=? WHERE id='deployed'`, deployedCommit); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "进行中", "waiting_approval"} {
		apps, err := s.ListApplications(ctx, ApplicationFilter{ProductID: p.ID, Status: status})
		if err != nil || apps.Total != 1 {
			t.Fatal(status, apps, err)
		}
		x := apps.Items[0]
		if x.LatestRun == nil || x.LatestRun.RunID != "pending" || x.LatestRun.Status != "waiting_approval" {
			t.Fatal("recent run", x.LatestRun)
		}
		if x.ProductionDeployment == nil || x.ProductionDeployment.RunID != "deployed" || x.ProductionDeployment.Environment != "production" || x.ProductionDeployment.Commit != deployedCommit {
			t.Fatal("production deployment", x.ProductionDeployment)
		}
	}
	apps, err := s.ListApplications(ctx, ApplicationFilter{ProductID: p.ID, Status: "成功"})
	if err != nil || apps.Total != 0 {
		t.Fatal(apps, err)
	}
}

func TestWorkspaceConcurrentAssignmentsChooseOneDestination(t *testing.T) {
	ctx := t.Context()
	s, _, _, in := workspaceFixture(t)
	_, first := productFixture(t, ctx, s, "First")
	_, second := productFixture(t, ctx, s, "Second")
	app, err := s.projects.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	type response struct {
		row Assignment
		err error
	}
	begin := make(chan struct{})
	results := make(chan response, 2)
	for _, target := range []Product{first, second} {
		go func(target Product) {
			<-begin
			row, err := s.AssignApplication(ctx, app.ID, target.ID, 0)
			results <- response{row, err}
		}(target)
	}
	close(begin)
	var destination string
	conflicts := 0
	for i := 0; i < 2; i++ {
		result := <-results
		switch {
		case result.err == nil:
			if destination != "" || result.row.Revision != 1 {
				t.Fatal(result.row)
			}
			destination = result.row.ProductID
		case errors.Is(result.err, ErrConflict):
			conflicts++
		default:
			t.Fatal(result.err)
		}
	}
	if destination == "" || conflicts != 1 {
		t.Fatal(destination, conflicts)
	}
	page, err := s.ListApplications(ctx, ApplicationFilter{ApplicationID: app.ID})
	if err != nil || page.Total != 1 || page.Items[0].ProductID != destination {
		t.Fatal(page, err)
	}
}
