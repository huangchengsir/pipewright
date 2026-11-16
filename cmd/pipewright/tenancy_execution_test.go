package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

const accessSourceYAML = `version: 1
stages:
  - id: source
    name: Source
    kind: source
    jobs:
      - id: checkout
        name: Checkout
        type: git_source
  - id: build
    name: Build
    kind: build
    needs: [source]
    jobs:
      - id: script
        name: Script
        type: script
        script:
          image: alpine:3
          commands: ['echo "$VAR $PW_VAR"']
`

type accessSourceReader struct {
	calls  int
	branch string
	yaml   string
	err    error
}

func (r *accessSourceReader) PipelineSnapshot(_ context.Context, _, _, _, branch string) (httpapi.PipelineSourceSnapshot, error) {
	r.calls++
	r.branch = branch
	return httpapi.PipelineSourceSnapshot{CommitSHA: strings.Repeat("a", 40), YAML: r.yaml, Found: true}, r.err
}

type compilerFixture struct {
	db           *sql.DB
	compiler     *runAccessCompiler
	source       *accessSourceReader
	admin        context.Context
	publisher    context.Context
	noCredential context.Context
	project      string
}

func newCompilerFixture(t *testing.T, definitions []run.ParamDef) compilerFixture {
	t.Helper()
	ctx := context.Background()
	db := storetest.OpenDB(t)
	key := [32]byte{1}
	v := vault.New(db, &key)
	credential, err := v.Create(vault.CreateInput{Name: "Source", Type: vault.TypeGitToken, Secret: "local-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	id := "p"
	_, err = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,pac_enabled,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, id, "Project", "https://example.invalid/repo.git", "release", credential.ID, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	pipelines := pipeline.New(db)
	settings := pipeline.NewSettingsService(db, v)
	if _, err = pipelines.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = settings.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	if definitions != nil {
		if _, err = run.NewParameterService(db).Save(ctx, id, definitions); err != nil {
			t.Fatal(err)
		}
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "admin-compiler-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-compiler-password")
	if err != nil {
		t.Fatal(err)
	}
	create := func(username string, use bool) context.Context {
		pages := []string{"page.runs"}
		grants := []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.start", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
		refs := []authz.ResourceRef{}
		if use {
			pages = append(pages, "page.vault")
			grants = append(grants, authz.Grant{Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}}, authz.Grant{Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeTeam}})
			refs = append(refs, authz.ResourceRef{Kind: "credential", ID: credential.ID})
		}
		role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: username, Pages: pages, Grants: grants, RequiresSelectedBinding: true})
		if e != nil {
			t.Fatal(e)
		}
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: username, DisplayName: username, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{id}, ResourceRefs: refs}}}})
		if e != nil {
			t.Fatal(e)
		}
		login, e := accounts.Login(ctx, username, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, username+"-long-password")
		if e != nil {
			t.Fatal(e)
		}
		return tenancy.WithActor(ctx, login.Actor, team)
	}
	source := &accessSourceReader{yaml: accessSourceYAML}
	recovery := runretry.New(db, v, runretry.WithSnapshotProvider(func(context.Context, string) (runretry.Snapshot, error) {
		t.Error("captured run refetched provider")
		return runretry.Snapshot{}, errors.New("unexpected provider")
	}))
	compiler := &runAccessCompiler{db: db, projects: project.New(db, v, nil), pipelines: pipelines, settings: settings, vault: v, source: source, recovery: recovery}
	return compilerFixture{db: db, compiler: compiler, source: source, admin: tenancy.WithActor(ctx, admin.Actor, team), publisher: create("publisher", true), noCredential: create("without-use", false), project: id}
}
func TestRunAccessCompilerCapturesDefaultBranchAndFrozenPAC(t *testing.T) {
	f := newCompilerFixture(t, nil)
	svc := run.New(f.db, run.WithExecutionAuthorizer(f.compiler))
	created, err := svc.Create(f.publisher, f.project, run.Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	if f.source.calls != 1 || f.source.branch != "release" {
		t.Fatal("default branch lost", f.source.calls, f.source.branch)
	}
	snap, err := f.compiler.recovery.FrozenSnapshotForRelease(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Source.Kind != "repo" || snap.CommitSHA != strings.Repeat("a", 40) || snap.RequestedRef != "release" {
		t.Fatalf("wrong frozen source: %+v", snap.Source)
	}
	digest, err := f.compiler.Digest(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.source.yaml = "changed after approval"
	job := tenancy.RunContext(context.Background(), f.db, created.ID, digest)
	if _, err = f.compiler.recovery.Prepare(job, created.ID); err != nil {
		t.Fatal(err)
	}
	if f.source.calls != 1 {
		t.Fatal("execution read changed source")
	}
	if err = tenancy.Check(f.publisher, f.db, "pipeline.read", "project", f.project); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("publisher acquired config read", err)
	}
}
func TestRunAccessCompilerRequiresExplicitResourceUse(t *testing.T) {
	f := newCompilerFixture(t, nil)
	_, err := run.New(f.db, run.WithExecutionAuthorizer(f.compiler)).Create(f.noCredential, f.project, run.Trigger{})
	if !errors.Is(err, tenancy.ErrForbidden) || f.source.calls != 0 {
		t.Fatal("source read without credential use", err, f.source.calls)
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&n)
	if n != 0 {
		t.Fatal("rejected run persisted")
	}
}
func TestRunAccessCompilerParameterOverridesRequireConfigurationAuthority(t *testing.T) {
	for _, defined := range []bool{false, true} {
		t.Run(map[bool]string{false: "free-params", true: "typed-defaults"}[defined], func(t *testing.T) {
			var defs []run.ParamDef
			if defined {
				defs = []run.ParamDef{{Key: "VAR", Type: run.ParamTypeString, Default: "saved"}}
			}
			f := newCompilerFixture(t, defs)
			overrides := []map[string]string{{"VAR": "arbitrary"}}
			if !defined {
				overrides = append(overrides, map[string]string{"VAR": ""})
			}
			for _, provided := range overrides {
				_, err := f.compiler.Prepare(f.publisher, f.project, run.Trigger{Params: provided})
				if !errors.Is(err, tenancy.ErrForbidden) {
					t.Fatal("shell parameter override accepted", provided, err)
				}
			}
			if f.source.calls != 0 {
				t.Fatal("denied overrides fetched repository")
			}
			prepared, err := f.compiler.Prepare(f.publisher, f.project, run.Trigger{})
			if err != nil {
				t.Fatal("saved defaults rejected", err)
			}
			snap := prepared.Value.(preparedRunAccess).snapshot
			if defined {
				empty, emptyErr := f.compiler.Prepare(f.publisher, f.project, run.Trigger{Params: map[string]string{"VAR": ""}})
				if emptyErr != nil || empty.Value.(preparedRunAccess).snapshot.Trigger.Params["VAR"] != "saved" {
					t.Fatal("empty value must resolve to stored default", emptyErr)
				}
			}
			if defined && snap.Trigger.Params["VAR"] != "saved" {
				t.Fatal("default missing", snap.Trigger.Params)
			}
			if _, err = f.compiler.Prepare(f.admin, f.project, run.Trigger{Params: map[string]string{"VAR": "approved override"}}); err != nil {
				t.Fatal("configuration administrator override", err)
			}
		})
	}
}
func TestRunAccessCompilerRejectsChangedTriggerAndConfiguration(t *testing.T) {
	f := newCompilerFixture(t, nil)
	trigger := run.Trigger{Branch: "release", Type: run.TriggerManual}
	prepared, err := f.compiler.Prepare(f.publisher, f.project, trigger)
	if err != nil {
		t.Fatal(err)
	}
	changed := trigger
	changed.Params = map[string]string{"VAR": "changed"}
	if _, _, err = f.compiler.Approve(f.publisher, "unused", f.project, changed, prepared); !errors.Is(err, tenancy.ErrConflict) {
		t.Fatal("changed trigger", err)
	}
	if _, err = f.db.Exec(`UPDATE project_config_versions SET revision=revision+1 WHERE project_id=?`, f.project); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.compiler.Approve(f.publisher, "unused", f.project, trigger, prepared); !errors.Is(err, tenancy.ErrConflict) {
		t.Fatal("changed config", err)
	}
}
