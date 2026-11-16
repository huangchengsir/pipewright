package httpapi

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/chain"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runner"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantTriggerChainConfigurationBoundaries(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	if _, e := db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','masked','now','now')`); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"p1", "p2", "p3", "foreign"} {
		if _, e := db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,'https://example.invalid','main','c','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, id); e != nil {
			t.Fatal(e)
		}
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "trigger-chain-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "trigger-chain-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='project' AND resource_id='foreign'`, other.ID); e != nil {
		t.Fatal(e)
	}
	for _, s := range []struct{ id, team string }{{"s1", team}, {"s2", other.ID}} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('server',?,?,?)`, s.id, s.team, admin.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	selected := func(ids ...string) authz.Scope { return authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: ids} }
	grants := []authz.Grant{
		{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}},
		{Action: "pipeline.read", Scope: authz.Scope{Kind: authz.ScopeTeam}},
		{Action: "pipeline.edit", Scope: selected("p1")},
		{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}},
		{Action: "run.start", Scope: selected("p2")},
		{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}},
		{Action: "server.use", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}},
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Config editor", Pages: []string{"page.projects", "page.pipeline", "page.runs", "page.servers"}, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "triggereditor", DisplayName: "Editor", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "triggereditor", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "trigger-chain-member-password")
	if e != nil {
		t.Fatal(e)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	ts := trigger.New(db, vault.New(db, testMasterKey()))
	cs := chain.NewService(db)
	concurrency := run.NewConcurrencyService(db)
	parameters := run.NewParameterService(db)
	runners := runner.New(db, nil)
	environments := promotion.NewStore(db)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithTriggers(ts), WithChain(cs), WithConcurrency(concurrency), WithParameters(parameters), WithRunnerConfig(runners), WithPromotion(environments))
	request := func(method, path, body string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.Header.Set("X-Pipewright-Team", team)
		if csrf {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
			r.Header.Set("X-CSRF-Token", login.CSRFToken)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	count := func(table string) int {
		var n int
		if e := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	// Reads never seed a token, secret or revision in tenant mode.
	for _, id := range []string{"p1", "p2"} {
		w := request("GET", "/api/projects/"+id+"/trigger", "", false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"webhookUrl":""`) {
			t.Fatal("read default", w.Code, w.Body.String())
		}
	}
	if count("pipeline_triggers") != 0 {
		t.Fatal("read created credentials")
	}
	for _, id := range []string{"foreign", "missing"} {
		if _, e = ts.Get(user, id); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("hidden read", e)
		}
	}
	input := trigger.SaveInput{UnmatchedPolicy: trigger.PolicyRecord, BranchMappings: []trigger.BranchMapping{{BranchPattern: "main", Environment: "dev", TargetServerIDs: []string{"s1"}}}}
	for _, fn := range []func() error{
		func() error { _, e := ts.Save(user, "p2", input); return e }, func() error { _, e := ts.ResetSecret(user, "p2"); return e },
		func() error { _, e := ts.Save(user, "foreign", input); return e },
		func() error {
			bad := input
			bad.BranchMappings = []trigger.BranchMapping{{BranchPattern: "main", TargetServerIDs: []string{"s2"}}}
			_, e := ts.Save(user, "p1", bad)
			return e
		},
		func() error { _, e := cs.Save(user, "p2", nil); return e },
		func() error { _, e := cs.Save(user, "p1", []chain.Target{{DownstreamProjectID: "foreign"}}); return e },
		func() error {
			_, e := cs.Save(user, "p1", []chain.Target{{DownstreamProjectID: "p3", Enabled: true}})
			return e
		},
	} {
		if e = fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("denied domain", e)
		}
	}
	if count("pipeline_triggers") != 0 || count("project_chain_targets") != 0 {
		t.Fatal("denied wrote config")
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/projects/p2/trigger", `{"unmatchedPolicy":"record"}`},
		{"POST", "/api/projects/p2/trigger/secret/reset", `{}`},
		{"PUT", "/api/projects/p2/chain", `{"downstream":[]}`},
		{"GET", "/api/projects/foreign/chain", ``},
		{"PUT", "/api/projects/p1/chain", `{"downstream":[{"downstreamProjectId":"foreign","enabled":false}]}`},
	} {
		w := request(tc.method, tc.path, tc.body, true)
		if w.Code != 403 {
			t.Fatal("HTTP denied", tc.path, w.Code, w.Body.String())
		}
	}
	if w := request("PUT", "/api/projects/p1/trigger", `{"unmatchedPolicy":"record"}`, false); w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	if _, e = ts.Save(user, "p1", input); e != nil {
		t.Fatal("allowed trigger", e)
	}
	cfg, e := ts.Get(user, "p1")
	if e != nil || cfg.WebhookToken == "" {
		t.Fatal("saved endpoint", e)
	}
	reset, e := ts.ResetSecret(user, "p1")
	if e != nil || reset.Secret == "" || reset.WebhookToken != cfg.WebhookToken {
		t.Fatal("allowed reset", e)
	}
	if cfg, e = ts.Get(user, "p1"); e != nil || strings.Contains(cfg.WebhookSecretMasked, reset.Secret) {
		t.Fatal("masked only", e)
	}
	if _, e = cs.Save(user, "p1", []chain.Target{{DownstreamProjectID: "p2", Enabled: true}, {DownstreamProjectID: "p3", Enabled: false}}); e != nil {
		t.Fatal("allowed chain", e)
	}
	before, e := cs.Get(user, "p1")
	if e != nil || len(before.Targets) != 2 {
		t.Fatal("chain read", e)
	}
	// Failed or rolled-back replacement cannot erase existing targets or secrets.
	rollback := errors.New("outer rollback")
	e = configstate.WithWriteTxContext(user, db, func(c context.Context, _ configstate.DBTX) error {
		if _, e := ts.ResetSecret(c, "p1"); e != nil {
			return e
		}
		if _, e := cs.Save(c, "p1", nil); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	after, e := cs.Get(user, "p1")
	if e != nil || len(after.Targets) != 2 {
		t.Fatal("chain rollback", e)
	}
	now, e := ts.Get(user, "p1")
	if e != nil || now.WebhookSecretMasked != cfg.WebhookSecretMasked {
		t.Fatal("secret rollback", e)
	}

	// Related project config endpoints use the same per-project action boundary.
	definitions := []run.ParamDef{{Key: "COUNT", Type: run.ParamTypeNumber, Default: "2"}}
	for _, id := range []string{"p2", "foreign", "missing"} {
		for _, fn := range []func() error{
			func() error { _, e := concurrency.Save(user, id, 3); return e },
			func() error { _, e := parameters.Save(user, id, definitions); return e },
			func() error { _, e := runners.Save(user, id, "s1"); return e },
		} {
			if e = fn(); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("scoped config write", id, e)
			}
		}
	}
	for _, id := range []string{"foreign", "missing"} {
		for _, fn := range []func() error{func() error { _, e := concurrency.Get(user, id); return e }, func() error { _, e := parameters.Get(user, id); return e }, func() error { _, e := runners.Get(user, id); return e }} {
			if e = fn(); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("scoped config read", id, e)
			}
		}
	}
	if _, e = runners.Save(user, "p1", "s2"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("foreign runner", e)
	}
	for _, tc := range []struct{ path, body string }{{"concurrency", `{"maxConcurrent":3}`}, {"parameters", `{"parameters":[]}`}, {"runner", `{"runnerServerId":"s1"}`}} {
		if w := request("PUT", "/api/projects/p2/"+tc.path, tc.body, true); w.Code != 403 {
			t.Fatal("config HTTP write", tc.path, w.Code, w.Body.String())
		}
		if w := request("GET", "/api/projects/foreign/"+tc.path, "", false); w.Code != 403 {
			t.Fatal("config HTTP read", tc.path, w.Code, w.Body.String())
		}
	}
	for _, table := range []string{"project_concurrency", "project_parameters", "project_runners"} {
		if count(table) != 0 {
			t.Fatal("denied config wrote", table)
		}
	}
	if _, e = concurrency.Save(user, "p1", 3); e != nil {
		t.Fatal(e)
	}
	if _, e = parameters.Save(user, "p1", definitions); e != nil {
		t.Fatal(e)
	}
	if _, e = runners.Save(user, "p1", "s1"); e != nil {
		t.Fatal(e)
	}
	// Internal admission must retain configured limits and typed validation without an HTTP session.
	if concurrency.LimitFor(ctx, "p1") != 3 {
		t.Fatal("background lost concurrency limit")
	}
	if result, e := parameters.Resolve(ctx, "p1", nil); e != nil || result["COUNT"] != "2" {
		t.Fatal("runtime parameter resolution", e)
	}
	if id, ok := runners.RunnerFor(ctx, "p1"); !ok || id != "s1" {
		t.Fatal("runtime runner resolution")
	}
	e = configstate.WithWriteTxContext(user, db, func(c context.Context, _ configstate.DBTX) error {
		if _, e := concurrency.Save(c, "p1", 9); e != nil {
			return e
		}
		if _, e := parameters.Save(c, "p1", nil); e != nil {
			return e
		}
		if _, e := runners.Save(c, "p1", ""); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	if concurrency.LimitFor(ctx, "p1") != 3 {
		t.Fatal("config rollback")
	}
	if result, e := parameters.Get(user, "p1"); e != nil || len(result.Defs) != 1 {
		t.Fatal("parameters rollback", e)
	}
	if result, e := runners.Get(user, "p1"); e != nil || result.RunnerServerID != "s1" {
		t.Fatal("runner rollback", e)
	}

	// One environment-page save is one transaction and one revision, including variables.
	envChain := promotion.Chain{Environments: []promotion.EnvStage{{Name: "dev"}, {Name: "prod", Gated: true}}}
	envVars := map[string][]promotion.Variable{"dev": {{Key: "MODE", Value: "dev-value"}}, "prod": {{Key: "MODE", Value: "prod-value"}}}
	for _, fn := range []func() error{
		func() error { _, e := environments.SaveConfiguration(user, "p2", envChain, envVars); return e },
		func() error { _, e := environments.GetChain(user, "foreign"); return e },
		func() error { _, e := environments.ListVariables(user, "foreign", "dev"); return e },
		func() error { return environments.SetVariables(user, "p2", "dev", envVars["dev"]) },
	} {
		if e = fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("environment permission", e)
		}
	}
	if w := request("PUT", "/api/projects/p2/environments", `{"environments":[{"name":"dev"}]}`, true); w.Code != 403 {
		t.Fatal("environment HTTP edit", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/projects/foreign/environments", "", false); w.Code != 403 {
		t.Fatal("environment HTTP read", w.Code, w.Body.String())
	}
	rev, e := configstate.ReadRevision(ctx, db, "p1")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = environments.SaveConfiguration(user, "p1", envChain, envVars); e != nil {
		t.Fatal("positive environment save", e)
	}
	afterRev, e := configstate.ReadRevision(ctx, db, "p1")
	if e != nil || afterRev != rev+1 {
		t.Fatal("environment save must advance once", rev, afterRev, e)
	}
	rev = afterRev
	badChain := promotion.Chain{Environments: []promotion.EnvStage{{Name: "dev"}, {Name: "staging"}}}
	for _, bad := range []map[string][]promotion.Variable{
		{"dev": {{Key: "MODE", Value: "must-not-commit"}}, "staging": {{Key: ""}}},
		{"dev": {{Key: "MODE", Value: "must-not-commit"}}, "staging": {{Key: "SECRET", Secret: true, CredentialID: "c", Value: "must-drop"}}},
	} {
		if _, e = environments.SaveConfiguration(user, "p1", badChain, bad); e == nil {
			t.Fatal("invalid second environment accepted")
		}
	}
	saved, e := environments.GetChain(user, "p1")
	if e != nil || saved.IndexOf("prod") != 1 || saved.IndexOf("staging") >= 0 {
		t.Fatal("partial environment chain", e)
	}
	vars, e := environments.ListVariables(user, "p1", "dev")
	if e != nil || len(vars) != 1 || vars[0].Value != "dev-value" {
		t.Fatal("partial environment variables", e)
	}
	afterRev, e = configstate.ReadRevision(ctx, db, "p1")
	if e != nil || afterRev != rev {
		t.Fatal("failed save advanced revision", rev, afterRev, e)
	}
	if w := request("PUT", "/api/projects/p1/environments", `{"environments":[{"name":"dev"},{"name":"staging"}],"variables":{"dev":[{"key":"MODE","value":"must-not-commit"}],"staging":[{"key":"X","secret":true,"credentialId":"c"}]}}`, true); w.Code != 403 || w.Header().Get("X-Config-Partial-Save") != "" {
		t.Fatal("atomic HTTP forbidden", w.Code, w.Header(), w.Body.String())
	}
	e = configstate.WithWriteTxContext(user, db, func(c context.Context, _ configstate.DBTX) error {
		if _, e := environments.SaveConfiguration(c, "p1", badChain, nil); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	saved, e = environments.GetChain(user, "p1")
	if e != nil || saved.IndexOf("prod") != 1 {
		t.Fatal("environment outer rollback", e)
	}
	adminContext := tenancy.WithActor(ctx, admin.Actor, team)
	if e = environments.SetVariables(adminContext, "p1", "prod", []promotion.Variable{{Key: "SECRET", Secret: true, CredentialID: "c", Value: "must-not-store-plaintext"}}); e != nil {
		t.Fatal("authorized reference", e)
	}
	vars, e = environments.ListVariables(user, "p1", "prod")
	if e != nil || len(vars) != 1 || vars[0].Value != "" || vars[0].CredentialID != "c" {
		t.Fatal("secret reference projection", e)
	}
	var plain int
	if e = db.QueryRow(`SELECT COUNT(*) FROM environment_variables WHERE value='must-not-store-plaintext'`).Scan(&plain); e != nil || plain != 0 {
		t.Fatal("plaintext persisted", e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='credential' AND resource_id='c'`, other.ID); e != nil {
		t.Fatal(e)
	}
	if e = environments.SetVariables(adminContext, "p1", "prod", []promotion.Variable{{Key: "SECRET", Secret: true, CredentialID: "c"}}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("admin cross-team binding", e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	for _, fn := range []func() error{func() error { _, e := ts.Get(user, "p1"); return e }, func() error { _, e := ts.ResetSecret(user, "p1"); return e }, func() error { _, e := cs.Save(user, "p1", nil); return e }} {
		if e = fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("revoked", e)
		}
	}
	for _, fn := range []func() error{func() error { _, e := concurrency.Save(user, "p1", 0); return e }, func() error { _, e := parameters.Save(user, "p1", nil); return e }, func() error { _, e := runners.Save(user, "p1", ""); return e }} {
		if e = fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("revoked config write", e)
		}
	}
	if count("project_chain_targets") != 2 {
		t.Fatal("revoked changed targets")
	}
}
