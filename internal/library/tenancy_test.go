package library

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestTenantReusableAssetsHaveTypedScopesAndTeamLocalNames(t *testing.T) {
	for _, kind := range []string{"template", "variable_group", "custom_node"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			templates := NewTemplateService(db, pipeline.New(db))
			groups := NewVarGroupService(db, nil)
			nodes := NewCustomNodeService(db)
			table := ""
			var missing, taken error
			var create func(context.Context, string) (string, error)
			var get func(context.Context, string) error
			var list func(context.Context) ([]string, error)
			var remove func(context.Context, string) error
			switch kind {
			case "template":
				table = "pipeline_templates"
				missing = ErrTemplateNotFound
				taken = ErrTemplateNameTaken
				create = func(c context.Context, name string) (string, error) {
					v, e := templates.Create(c, TemplateInput{Name: name, Spec: sampleSpec()})
					if e != nil {
						return "", e
					}
					return v.ID, nil
				}
				get = func(c context.Context, id string) error { _, e := templates.Get(c, id); return e }
				remove = templates.Delete
				list = func(c context.Context) ([]string, error) {
					rows, e := templates.List(c)
					var out []string
					for _, r := range rows {
						out = append(out, r.ID)
					}
					return out, e
				}
			case "variable_group":
				table = "variable_groups"
				missing = ErrVarGroupNotFound
				taken = ErrVarGroupNameTaken
				create = func(c context.Context, name string) (string, error) {
					v, e := groups.Create(c, VarGroupInput{Name: name, Vars: []pipeline.BuildVar{{Key: "PUBLIC", Value: "safe"}}})
					if e != nil {
						return "", e
					}
					return v.ID, nil
				}
				get = func(c context.Context, id string) error { _, e := groups.Get(c, id); return e }
				remove = groups.Delete
				list = func(c context.Context) ([]string, error) {
					rows, e := groups.List(c)
					var out []string
					for _, r := range rows {
						out = append(out, r.ID)
					}
					return out, e
				}
			case "custom_node":
				table = "custom_nodes"
				missing = ErrCustomNodeNotFound
				taken = ErrCustomNodeNameTaken
				create = func(c context.Context, name string) (string, error) {
					v, e := nodes.Create(c, CustomNodeInput{Name: name, NodeType: "script", Config: map[string]any{"commands": []string{"echo safe"}}})
					if e != nil {
						return "", e
					}
					return v.ID, nil
				}
				get = func(c context.Context, id string) error { _, e := nodes.Get(c, id); return e }
				remove = nodes.Delete
				list = func(c context.Context) ([]string, error) {
					rows, e := nodes.List(c)
					var out []string
					for _, r := range rows {
						out = append(out, r.ID)
					}
					return out, e
				}
			}
			legacy, e := create(ctx, "Shared")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = create(ctx, "Shared"); !errors.Is(e, taken) {
				t.Fatal("off name uniqueness", e)
			}
			if e = auth.NewService(db, nil).Bootstrap("admin", "admin-library-password"); e != nil {
				t.Fatal(e)
			}
			repo := tenancy.NewRepository(db)
			if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
				t.Fatal(e)
			}
			var team string
			if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
				t.Fatal(e)
			}
			accounts := tenancy.NewAccounts(db)
			admin, e := accounts.Login(ctx, "admin", "admin-library-password")
			if e != nil {
				t.Fatal(e)
			}
			adminctx := tenancy.WithActor(ctx, admin.Actor, team)
			other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
			if e != nil {
				t.Fatal(e)
			}
			foreign, e := create(tenancy.WithActor(ctx, admin.Actor, other.ID), "Shared")
			if e != nil {
				t.Fatal("cross-team same name", e)
			}
			if _, e = create(adminctx, "Shared"); !errors.Is(e, taken) {
				t.Fatal("same-team name conflict", e)
			}
			outside, e := create(adminctx, "Outside")
			if e != nil {
				t.Fatal(e)
			}
			role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Selected library", Pages: []string{"page.library"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: kind + ".read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: kind + ".delete", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
			if e != nil {
				t.Fatal(e)
			}
			initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: kind, ID: legacy}}}}}})
			if e != nil {
				t.Fatal(e)
			}
			login, e := accounts.Login(ctx, "reader", initial.Password)
			if e != nil {
				t.Fatal(e)
			}
			login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "reader-library-password")
			if e != nil {
				t.Fatal(e)
			}
			actor := tenancy.WithActor(ctx, login.Actor, team)
			rows, e := list(actor)
			if e != nil || len(rows) != 1 || rows[0] != legacy {
				t.Fatal("exact scoped list", rows, e)
			}
			if e = get(actor, legacy); e != nil {
				t.Fatal(e)
			}
			for _, id := range []string{outside, foreign, "unknown"} {
				if e = get(actor, id); !errors.Is(e, missing) {
					t.Fatal("out-of-scope detail", id, e)
				}
				if e = remove(actor, id); !errors.Is(e, tenancy.ErrForbidden) {
					t.Fatal("out-of-scope delete", id, e)
				}
			}
			if _, e = create(actor, "Denied"); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("missing create action", e)
			}
			var n int
			if e = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil || n != 3 {
				t.Fatal("denied writes changed rows", n, e)
			}
			if kind == "variable_group" {
				if _, e = groups.Update(actor, legacy, VarGroupInput{Name: "Changed"}); !errors.Is(e, tenancy.ErrForbidden) {
					t.Fatal("missing update action", e)
				}
			}
			if kind == "custom_node" {
				if _, e = nodes.Update(actor, legacy, CustomNodeInput{Name: "Changed", NodeType: "script"}); !errors.Is(e, tenancy.ErrForbidden) {
					t.Fatal("missing update action", e)
				}
			}
			if e = remove(actor, legacy); e != nil {
				t.Fatal("allowed scoped delete", e)
			}
			if e = db.QueryRow(`SELECT COUNT(*) FROM access_ownership WHERE resource_kind=? AND resource_id=?`, kind, legacy).Scan(&n); e != nil || n != 0 {
				t.Fatal("stale ownership", n, e)
			}
			creatorRole, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Creator only", Pages: []string{"page.library"}, Grants: []authz.Grant{{Action: kind + ".create", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
			if e != nil {
				t.Fatal(e)
			}
			newUser, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "creator", DisplayName: "Creator", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: creatorRole.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
			if e != nil {
				t.Fatal(e)
			}
			creator, e := accounts.Login(ctx, "creator", newUser.Password)
			if e != nil {
				t.Fatal(e)
			}
			creator, e = accounts.ChangePassword(ctx, creator.Token, newUser.Password, "creator-library-password")
			if e != nil {
				t.Fatal(e)
			}
			creatorCtx := tenancy.WithActor(ctx, creator.Actor, team)
			created, e := create(creatorCtx, "Created without inspect")
			if e != nil || created == "" {
				t.Fatal("create-only grant failed", e)
			}
			if e = get(creatorCtx, created); !errors.Is(e, missing) {
				t.Fatal("create implicitly granted read", e)
			}
			if _, e = db.Exec(`UPDATE selfupdate_control SET task_id='db-migration-fixture' WHERE id=1`); e != nil {
				t.Fatal(e)
			}
			if _, e = create(adminctx, "Blocked maintenance"); !errors.Is(e, configstate.ErrMaintenance) {
				t.Fatal("maintenance write", e)
			}
			if _, e = db.Exec(`UPDATE selfupdate_control SET task_id='' WHERE id=1`); e != nil {
				t.Fatal(e)
			}
			if kind == "variable_group" {
				if _, e = groups.Update(tenancy.WithActor(ctx, admin.Actor, other.ID), foreign, VarGroupInput{Name: "Outside"}); e != nil {
					t.Fatal("admin rename used current team's namespace", e)
				}
			}
			if kind == "custom_node" {
				if _, e = nodes.Update(tenancy.WithActor(ctx, admin.Actor, other.ID), foreign, CustomNodeInput{Name: "Outside", NodeType: "script"}); e != nil {
					t.Fatal("admin rename used current team's namespace", e)
				}
			}
			if e = accounts.Logout(ctx, login.Token); e != nil {
				t.Fatal(e)
			}
			if _, e = list(actor); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("revoked reader", e)
			}
			if _, e = list(ctx); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("missing actor", e)
			}
			if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: false, Explicit: true}, true); e != nil {
				t.Fatal(e)
			}
			if _, e = create(ctx, "Outside"); !errors.Is(e, taken) {
				t.Fatal("off mode must retain global name conflict", e)
			}
			if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
				t.Fatal(e)
			}
			var namespace string
			if e = db.QueryRow("SELECT name_scope FROM "+table+" WHERE id=?", foreign).Scan(&namespace); e != nil || namespace != other.ID {
				t.Fatal("mode transition reassigned namespace", namespace, e)
			}

		})
	}
}
