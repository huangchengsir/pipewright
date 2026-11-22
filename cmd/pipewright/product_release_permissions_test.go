package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/productrelease"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"github.com/huangchengsir/pipewright/internal/workspace"
)

func TestProductReleaseTenantHTTPPermissions(t *testing.T) {
	ctx := t.Context()
	db := storetest.OpenDB(t)
	key := [32]byte{5, 7, 11}
	secrets := vault.New(db, &key)
	gitCredential, err := secrets.Create(vault.CreateInput{Name: "Git fixture", Type: vault.TypeGitToken, Secret: "local-source-token"})
	if err != nil {
		t.Fatal(err)
	}
	sshCredential, err := secrets.Create(vault.CreateInput{Name: "SSH fixture", Type: vault.TypeSSHPassword, Secret: "local-target-password", Username: "fixture"})
	if err != nil {
		t.Fatal(err)
	}

	projects := project.New(db, secrets, nil)
	workspaceSvc := workspace.New(db, projects)
	org, err := workspaceSvc.CreateBusinessProject(ctx, "Permission fixture")
	if err != nil {
		t.Fatal(err)
	}
	product, err := workspaceSvc.CreateProduct(ctx, org.ID, "Store")
	if err != nil {
		t.Fatal(err)
	}
	const stamp = "2026-11-21T00:00:00Z"
	app, hidden := uuid.NewString(), uuid.NewString()
	for _, id := range []string{app, hidden} {
		if _, err = db.ExecContext(ctx, `INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			id, id, "https://example.invalid/local-fixture", "main", gitCredential.ID, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err = workspaceSvc.AssignApplication(ctx, id, product.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	const targetID = "fixture-server"
	if _, err = db.ExecContext(ctx, `INSERT INTO servers(id,name,host,port,"user",credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		targetID, "Fixture", "192.0.2.12", 22, "fixture", sshCredential.ID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	pipelines := pipeline.New(db)
	settings := pipeline.NewSettingsService(db, secrets)
	if _, err = pipelines.Save(ctx, app, productReleaseSpec()); err != nil {
		t.Fatal(err)
	}
	defaultSettings, err := settings.Get(ctx, app)
	if err != nil {
		t.Fatal(err)
	}

	const ownerPassword = "local-fixture-owner-password"
	if err = auth.NewService(db, nil).Bootstrap("release-admin", ownerPassword); err != nil {
		t.Fatal(err)
	}
	repository := tenancy.NewRepository(db)
	state, err := repository.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	owner, err := accounts.Login(ctx, "release-admin", ownerPassword)
	if err != nil {
		t.Fatal(err)
	}
	var teamID, stagingID string
	if err = db.QueryRowContext(ctx, `SELECT default_team_id FROM access_control WHERE id=1`).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if _, err = settings.Save(tenancy.WithActor(ctx, owner.Actor, teamID), app, pipeline.SettingsInput{Build: defaultSettings.Build, Environments: []pipeline.Environment{
		{Name: "staging", TargetServerIDs: []string{targetID}},
		{Name: "production", TargetServerIDs: []string{targetID}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT id FROM access_environments WHERE project_id=? AND name='staging'`, app).Scan(&stagingID); err != nil {
		t.Fatal(err)
	}
	otherTeam, err := repository.CreateTeam(ctx, "Other team")
	if err != nil {
		t.Fatal(err)
	}
	otherContext := tenancy.WithActor(ctx, owner.Actor, otherTeam)
	otherOrg, err := workspaceSvc.CreateBusinessProject(otherContext, "Other business")
	if err != nil {
		t.Fatal(err)
	}
	otherProduct, err := workspaceSvc.CreateProduct(otherContext, otherOrg.ID, "Other product")
	if err != nil {
		t.Fatal(err)
	}

	selected := authz.Scope{
		Kind: authz.ScopeSelected, ProjectIDs: []string{app}, EnvironmentIDs: []string{stagingID},
		ResourceRefs: []authz.ResourceRef{
			{Kind: "business_project", ID: org.ID}, {Kind: "product", ID: product.ID},
			{Kind: "server", ID: targetID}, {Kind: "credential", ID: gitCredential.ID}, {Kind: "credential", ID: sshCredential.ID},
		},
	}
	roleInput := func(name string, omitted ...string) tenancy.RoleInput {
		input := tenancy.RoleInput{Name: name, Pages: []string{"page.projects", "page.runs", "page.environments", "page.servers", "page.vault"}, RequiresSelectedBinding: true}
		for _, action := range []string{"business_project.read", "product.read", "project.read", "run.read", "run.start",
			"environment.read", "deployment.execute", "server.inspect", "server.use", "credential.metadata", "credential.use"} {
			if slices.Contains(omitted, action) {
				continue
			}
			scope := authz.Scope{Kind: authz.ScopeTeam}
			if action == "deployment.execute" {
				scope.EnvironmentIDs = []string{stagingID}
			}
			input.Grants = append(input.Grants, authz.Grant{Action: action, Scope: scope})
		}
		return input
	}
	newUser := func(name string, role tenancy.RoleView) tenancy.Session {
		t.Helper()
		initial, err := accounts.Create(ctx, owner.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name,
			Assignments: []tenancy.MemberAssignment{{TeamID: teamID, RoleID: role.ID, Scope: selected}}})
		if err != nil {
			t.Fatal(err)
		}
		session, err := accounts.Login(ctx, name, initial.Password)
		if err != nil {
			t.Fatal(err)
		}
		session, err = accounts.ChangePassword(ctx, session.Token, initial.Password, name+"-long-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		return session
	}

	reader := &accessSourceReader{}
	servers := target.New(db, secrets, nil)
	compiler := &runAccessCompiler{db: db, projects: projects, pipelines: pipelines, settings: settings, source: reader,
		vault: secrets, targets: servers, recovery: runretry.New(db, secrets)}
	runs := run.New(db, run.WithExecutionAuthorizer(compiler))
	// Use the original queue admission while leaving its worker stopped.
	pool := run.NewWorkerPool(runs)
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pool.Stop(stop)
	})
	controller := &productReleaseController{db: db, workspace: workspaceSvc, compiler: compiler, runs: runs,
		targets: servers.(target.LimitedExecutor), attempts: deployattempt.New(db), ready: true}
	service := productrelease.New(db, controller, controller)
	router := httpapi.New(fstest.MapFS{"index.html": {Data: []byte("fixture")}}, tenancy.NewAuthenticator(accounts, state.Epoch),
		httpapi.WithTenancyAccounts(accounts), httpapi.WithWorkspace(workspaceSvc), httpapi.WithProductReleases(service))
	request := func(session tenancy.Session, method, path, body string, csrf bool, status int, out any) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/workspace"+path, strings.NewReader(body))
		req.Header.Set("X-Pipewright-Team", teamID)
		if session.Token != "" {
			req.AddCookie(&http.Cookie{Name: "pipewright_session", Value: session.Token})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", session.CSRFToken)
		}
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)
		if rw.Code != status {
			t.Fatalf("%s %s returned %d, expected %d: %s", method, path, rw.Code, status, rw.Body.String())
		}
		if out != nil {
			if err := json.NewDecoder(rw.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	const optionsPath = "/release-options"
	path := "/products/" + product.ID
	body := func(application, environment string) string {
		data, _ := json.Marshal(map[string]any{"items": []productrelease.PreviewItem{{ApplicationID: application, Environment: environment}}})
		return string(data)
	}

	operatorInput := roleInput("Staging publisher", "")
	operatorRole, err := repository.SaveRole(ctx, owner.Actor, teamID, "", operatorInput)
	if err != nil {
		t.Fatal(err)
	}
	operator := newUser("staging-publisher", operatorRole)
	peer := newUser("staging-peer", operatorRole)
	request(tenancy.Session{}, "GET", path+optionsPath, "", false, 401, nil)
	request(operator, "POST", path+"/release-preview", body(app, "staging"), false, 403, nil)
	request(operator, "GET", "/products/"+otherProduct.ID+optionsPath, "", false, 404, nil)

	var options productrelease.Options
	request(operator, "GET", path+optionsPath, "", false, 200, &options)
	if !options.CanRelease || len(options.Applications) != 1 || options.Applications[0].ApplicationID != app ||
		len(options.Applications[0].Environments) != 1 || options.Applications[0].Environments[0].Name != "staging" {
		t.Fatalf("publisher sees incorrect application/environment choices: %+v", options)
	}
	request(operator, "POST", path+"/release-preview", body(hidden, "staging"), true, 404, nil)
	request(operator, "POST", path+"/release-preview", body(app, "production"), true, 403, nil)
	if reader.calls != 0 {
		t.Fatal("rejected request read the repository")
	}

	var plan productrelease.Plan
	request(operator, "POST", path+"/release-preview", body(app, "staging"), true, 200, &plan)
	confirm, err := json.Marshal(map[string]any{"requestId": plan.Items[0].SnapshotRef, "plan": plan})
	if err != nil {
		t.Fatal(err)
	}
	var batch productrelease.Batch
	request(operator, "POST", path+"/releases", string(confirm), true, 200, &batch)
	batchPath := "/releases/" + batch.ID
	request(peer, "GET", batchPath, "", false, 404, nil)
	request(peer, "POST", batchPath+"/advance", `{"expectedRevision":1}`, true, 404, nil)
	request(peer, "POST", batchPath+"/reconcile", `{"expectedRevision":1}`, true, 404, nil)
	request(operator, "POST", batchPath+"/advance", `{"expectedRevision":1}`, false, 403, nil)
	var secondPlan productrelease.Plan
	request(operator, "POST", path+"/release-preview", body(app, "staging"), true, 200, &secondPlan)
	secondConfirm, err := json.Marshal(map[string]any{"requestId": secondPlan.Items[0].SnapshotRef, "plan": secondPlan})
	if err != nil {
		t.Fatal(err)
	}
	var queued productrelease.Batch
	request(operator, "POST", path+"/releases", string(secondConfirm), true, 200, &queued)
	request(operator, "POST", "/releases/"+queued.ID+"/advance", `{"expectedRevision":1}`, true, 200, &queued)
	if queued.State != "running" || queued.Items[0].RunID == "" {
		t.Fatalf("authorized publisher did not enqueue one staging run: %+v", queued)
	}

	var unconfirmed productrelease.Plan
	request(operator, "POST", path+"/release-preview", body(app, "staging"), true, 200, &unconfirmed)
	unconfirmedPayload, err := json.Marshal(map[string]any{"requestId": unconfirmed.Items[0].SnapshotRef, "plan": unconfirmed})
	if err != nil {
		t.Fatal(err)
	}

	changed := roleInput(operatorInput.Name, "deployment.execute")
	changed.ExpectedRevision = operatorRole.Revision
	if operatorRole, err = repository.SaveRole(ctx, owner.Actor, teamID, operatorRole.ID, changed); err != nil {
		t.Fatal(err)
	}
	request(operator, "GET", batchPath, "", false, 200, &batch)
	request(operator, "GET", path+optionsPath, "", false, 200, &options)
	if options.CanRelease || len(options.Applications) != 1 || len(options.Applications[0].Environments) != 0 {
		t.Fatalf("withdrawn deployment targets still visible in release options: %+v", options)
	}
	request(operator, "POST", batchPath+"/advance", `{"expectedRevision":1}`, true, 403, nil)
	request(operator, "POST", path+"/release-preview", body(app, "staging"), true, 403, nil)
	request(operator, "POST", path+"/releases", string(unconfirmedPayload), true, 403, nil)
	queuedPath := "/releases/" + queued.ID + "/reconcile"
	mutation := fmt.Sprintf(`{"expectedRevision":%d}`, queued.Revision)
	request(operator, "POST", queuedPath, mutation, true, 200, &queued)
	request(operator, "POST", batchPath+"/reconcile", `{"expectedRevision":1}`, true, 409, nil)
	withoutInspection := roleInput(operatorInput.Name, "deployment.execute", "server.inspect", "server.use")
	withoutInspection.ExpectedRevision = operatorRole.Revision
	if _, err = repository.SaveRole(ctx, owner.Actor, teamID, operatorRole.ID, withoutInspection); err != nil {
		t.Fatal(err)
	}
	request(operator, "GET", batchPath, "", false, 403, nil)
	request(operator, "POST", queuedPath, fmt.Sprintf(`{"expectedRevision":%d}`, queued.Revision), true, 403, nil)
	var history productrelease.BatchPage
	request(operator, "GET", path+"/releases", "", false, 200, &history)
	if len(history.Items) != 0 {
		t.Fatal("history exposed deployment targets after server inspection was revoked")
	}

	for _, entry := range []struct{ role, omitted string }{
		{"read-only", "run.start"}, {"without-target-use", "server.use"}, {"without-secret-use", "credential.use"},
	} {
		t.Run(entry.role, func(t *testing.T) {
			role, err := repository.SaveRole(ctx, owner.Actor, teamID, "", roleInput(entry.role, entry.omitted))
			if err != nil {
				t.Fatal(err)
			}
			session := newUser(entry.role, role)
			var deniedOptions productrelease.Options
			request(session, "GET", path+optionsPath, "", false, 200, &deniedOptions)
			if deniedOptions.CanRelease || len(deniedOptions.Applications) != 1 || len(deniedOptions.Applications[0].Environments) != 0 {
				t.Fatalf("role without %s has enabled deployment options: %+v", entry.omitted, deniedOptions)
			}
			request(session, "POST", path+"/release-preview", body(app, "staging"), true, 403, nil)
			var out productrelease.BatchPage
			request(session, "GET", path+"/releases", "", false, 200, &out)
			if len(out.Items) != 0 {
				t.Fatal("another account's release history was visible")
			}
		})
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_runs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("allowed staging action should be the only run: %d %v", count, err)
	}
	if reader.calls != 3 {
		t.Fatalf("expected only the three authorized previews to read the repository; got %d", reader.calls)
	}
}
