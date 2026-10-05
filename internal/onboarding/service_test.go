package onboarding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

const stamp = "2026-10-05T00:00:00Z"

func exec(t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (*sql.DB, *Service) {
	db := storetest.OpenDB(t)
	exec(t, db, `INSERT INTO credentials (id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES ('cred','secret-name','git_token','',X'00','SECRET_MASK',?,?)`, stamp, stamp)
	return db, New(db, Capabilities{Runtime: RuntimeAvailable, VaultConfigured: true})
}
func project(t *testing.T, db *sql.DB, id, created string) {
	exec(t, db, `INSERT INTO projects (id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,?,'https://secret.example/repo','main','cred',?,?)`, id, id, created, created)
}
func runRow(t *testing.T, db *sql.DB, id, projectID, status, mode, created string) {
	exec(t, db, `INSERT INTO pipeline_runs (id,project_id,status,execution_mode,created_at,failure_log,params_json) VALUES (?,?,?,?,?,'SECRET_LOG','{"SECRET_PARAM":"SECRET_VALUE"}')`, id, projectID, status, mode, created)
}
func job(typ string) pipeline.Job {
	return pipeline.Job{ID: "job", Name: "job", Type: typ, Config: map[string]any{"image": "alpine", "commands": "echo SECRET_COMMAND"}}
}
func spec(jobs ...pipeline.Job) pipeline.Spec {
	return pipeline.Spec{Stages: []pipeline.Stage{{ID: "source", Name: "source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "source-job", Name: "source", Type: "git_source"}}}, {ID: "build", Name: "build", Kind: pipeline.KindBuild, Jobs: jobs}}}
}
func config(t *testing.T, db *sql.DB, id string, spec pipeline.Spec, saved bool) {
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var savedAt any
	if saved {
		savedAt = stamp
	}
	exec(t, db, `INSERT INTO pipeline_configs (project_id,spec_json,spec_yaml,created_at,updated_at,saved_at) VALUES (?,?,'SECRET_YAML',?,?,?)`, id, string(raw), stamp, stamp, savedAt)
}
func settings(t *testing.T, db *sql.DB, id string, st *pipeline.Settings) {
	build, _ := json.Marshal(st.Build)
	envs, _ := json.Marshal(st.Environments)
	steps, _ := json.Marshal(st.Steps)
	exec(t, db, `INSERT INTO pipeline_settings (project_id,build_json,environments_json,steps_json,created_at,updated_at) VALUES (?,?,?,?,?,?)`, id, string(build), string(envs), string(steps), stamp, stamp)
}
func hasIssue(snapshot Snapshot, code, scope string) bool {
	for _, issue := range snapshot.Pipeline.Issues {
		if issue.Code == code && issue.Scope == scope {
			return true
		}
	}
	return false
}

func TestEmptyCountsAndUnknown(t *testing.T) {
	db, svc := fixture(t)
	out := svc.Status(context.Background(), "")
	if out.ProjectsState != Known || out.RunsState != Known || out.SuccessState != Known || out.ProjectCount == nil || *out.ProjectCount != 0 || out.RunCount == nil || *out.RunCount != 0 || out.SelectedProject != nil || out.Success != nil || out.Pipeline.State != "absent" {
		t.Fatalf("empty = %+v", out)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	out = svc.Status(context.Background(), "")
	if out.ProjectsState != Unknown || out.RunsState != Unknown || out.SuccessState != Unknown || out.ProjectCount != nil || out.RunCount != nil {
		t.Fatalf("failed counts masquerade as zero: %+v", out)
	}
}

func TestProjectSelectionAndDeletion(t *testing.T) {
	db, svc := fixture(t)
	project(t, db, "a", stamp)
	project(t, db, "b", stamp)
	if got := svc.Status(context.Background(), "").SelectedProject.ID; got != "b" {
		t.Fatal(got)
	}
	runRow(t, db, "a-run", "a", "failed", "real", stamp)
	out := svc.Status(context.Background(), "")
	if out.SelectedProject.ID != "a" || out.LatestRun.ID != "a-run" || out.LatestRun.ProjectID != "a" {
		t.Fatalf("latest selection: %+v", out)
	}
	if got := svc.Status(context.Background(), "b").SelectedProject.ID; got != "b" {
		t.Fatal(got)
	}
	runRow(t, db, "z-run", "b", "running", "pending", stamp)
	if got := svc.Status(context.Background(), "deleted").SelectedProject.ID; got != "b" {
		t.Fatal(got)
	}
	exec(t, db, `DELETE FROM projects WHERE id='b'`)
	out = svc.Status(context.Background(), "b")
	if out.SelectedProject.ID != "a" || len(out.Projects) != 1 || out.LatestRun.ID != "a-run" {
		t.Fatalf("deleted selection: %+v", out)
	}
	exec(t, db, `DELETE FROM projects WHERE id='a'`)
	if out = svc.Status(context.Background(), "a"); out.SelectedProject != nil || len(out.Projects) != 0 {
		t.Fatalf("all deleted: %+v", out)
	}
}

func TestQualifyingSuccessModes(t *testing.T) {
	for _, mode := range []string{"real", "legacy_unknown", "stub", "mixed", "pending", ""} {
		t.Run(mode, func(t *testing.T) {
			db, svc := fixture(t)
			project(t, db, "p", stamp)
			runRow(t, db, "old", "p", "success", mode, stamp)
			runRow(t, db, "latest", "p", "failed", "real", "2026-10-05T01:00:00Z")
			out := svc.Status(context.Background(), "p")
			want := mode == "real" || mode == "legacy_unknown"
			if out.SuccessState != Known || (out.Success != nil) != want || out.LatestRun.ID != "latest" {
				t.Fatalf("mode %s: %+v", mode, out)
			}
		})
	}
}

func TestIndependentSuccessDespitePartialErrors(t *testing.T) {
	storetest.SkipIfMySQL(t)
	for _, table := range []string{"pipeline_configs", "pipeline_settings", "project_runners", "credentials", "projects"} {
		t.Run(table, func(t *testing.T) {
			db, svc := fixture(t)
			project(t, db, "p", stamp)
			config(t, db, "p", spec(job("script")), true)
			runRow(t, db, "good", "p", "success", "real", stamp)
			exec(t, db, `ALTER TABLE `+table+` RENAME TO broken_table`)
			out := svc.Status(context.Background(), "p")
			if out.SuccessState != Known || out.Success == nil || out.Success.ID != "good" {
				t.Fatalf("success lost after %s fault: %+v", table, out)
			}
			if table == "projects" {
				if out.ProjectsState != Unknown {
					t.Fatal(out)
				}
			} else if out.Pipeline.State != Unknown {
				t.Fatalf("partial should be unknown: %+v", out)
			}
		})
	}
}

func TestActualSavePreservesEvidenceSeparatelyFromReadiness(t *testing.T) {
	for _, runtime := range []string{RuntimeAvailable, RuntimeStub} {
		t.Run(runtime, func(t *testing.T) {
			db, _ := fixture(t)
			project(t, db, "p", stamp)
			ctx := context.Background()
			svc := New(db, Capabilities{Runtime: runtime, VaultConfigured: true})
			editor := pipeline.New(db)
			if _, err := editor.Get(ctx, "p"); err != nil {
				t.Fatal(err)
			}
			if out := svc.Status(ctx, "p"); out.Pipeline.SavedAt != nil {
				t.Fatal("opening editor manufactured save evidence")
			}
			sourceOnly := pipeline.Spec{Stages: spec().Stages[:1]}
			if _, err := editor.Save(ctx, "p", sourceOnly); err != nil {
				t.Fatal(err)
			}
			saved := svc.Status(ctx, "p")
			if saved.Pipeline.SavedAt == nil || saved.Pipeline.State != "needs_configuration" || !hasIssue(saved, "no_tasks", pipeline.ScopeCanvas) {
				t.Fatalf("save lost evidence or claimed readiness: %+v", saved.Pipeline)
			}
			if _, err := editor.Save(ctx, "p", pipeline.Spec{Stages: []pipeline.Stage{{Kind: pipeline.KindSource}}}); err == nil {
				t.Fatal("invalid save succeeded")
			}
			if out := svc.Status(ctx, "p"); out.Pipeline.SavedAt == nil || !out.Pipeline.SavedAt.Equal(*saved.Pipeline.SavedAt) {
				t.Fatal("failed save changed previous evidence")
			}
			if _, err := editor.Save(ctx, "p", spec(job("script"))); err != nil {
				t.Fatal(err)
			}
			out := svc.Status(ctx, "p")
			want := "ready"
			if runtime == RuntimeStub {
				want = "needs_configuration"
			}
			if out.Pipeline.SavedAt == nil || out.Pipeline.State != want {
				t.Fatalf("configured state = %+v want %s", out.Pipeline, want)
			}
		})
	}
}

func TestReadOnlySavedDefaultAndRepository(t *testing.T) {
	storetest.SkipIfMySQL(t)
	for _, state := range []string{"absent", "unconfirmed", "ready", "repository"} {
		t.Run(state, func(t *testing.T) {
			db, svc := fixture(t)
			project(t, db, "p", stamp)
			if state != "absent" {
				config(t, db, "p", spec(job("script")), state == "ready")
			}
			if state == "repository" {
				exec(t, db, `UPDATE projects SET pac_enabled=1 WHERE id='p'`)
			}
			before := businessSnapshot(t, db)
			exec(t, db, `PRAGMA query_only=ON`)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for i := 0; i < 3; i++ {
				out := svc.Status(ctx, "p")
				if out.Pipeline.State != state {
					t.Fatalf("state = %s want %s, %+v", out.Pipeline.State, state, out)
				}
				data, _ := json.Marshal(out)
				if strings.Contains(string(data), "SECRET_") || strings.Contains(string(data), "secret.example") {
					t.Fatalf("unsafe snapshot: %s", data)
				}
			}
			if after := businessSnapshot(t, db); !reflect.DeepEqual(before, after) {
				t.Fatal("status changed business rows or timestamps")
			}
		})
	}
}

func businessSnapshot(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"projects", "credentials", "pipeline_runs", "pipeline_configs", "pipeline_settings", "pipeline_triggers", "project_runners", "servers"} {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var all [][]any
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			all = append(all, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		raw, _ := json.Marshal(all)
		result[table] = string(raw)
	}
	return result
}

func TestApplicablePrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name               string
		jobs               []pipeline.Job
		caps               Capabilities
		code, scope, state string
	}{
		{name: "source_only", state: "needs_configuration", code: "no_tasks", scope: pipeline.ScopeCanvas},
		{name: "unsupported", jobs: []pipeline.Job{job("future_task")}, state: "needs_configuration", code: "no_tasks", scope: pipeline.ScopeCanvas},
		{name: "push_marker", jobs: []pipeline.Job{job("push_image")}, state: "needs_configuration", code: "no_tasks", scope: pipeline.ScopeCanvas},
		{name: "missing_node_server", jobs: []pipeline.Job{job("deploy_ssh")}, state: "needs_configuration", code: "server_missing", scope: pipeline.ScopeCanvas},
		{name: "known_stub", jobs: []pipeline.Job{job("script")}, caps: Capabilities{Runtime: RuntimeStub, VaultConfigured: true}, state: "needs_configuration", code: "runtime_stub", scope: pipeline.ScopeVars},
		{name: "vault_unconfigured", jobs: []pipeline.Job{job("script")}, caps: Capabilities{Runtime: RuntimeAvailable}, state: "needs_configuration", code: "vault_unconfigured", scope: pipeline.ScopeEnvs},
		{name: "unknown_executor", jobs: []pipeline.Job{job("script")}, caps: Capabilities{VaultConfigured: true}, state: "unconfirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := fixture(t)
			project(t, db, "p", stamp)
			config(t, db, "p", spec(tc.jobs...), true)
			caps := tc.caps
			if caps == (Capabilities{}) {
				caps = Capabilities{Runtime: RuntimeAvailable, VaultConfigured: true}
			}
			out := New(db, caps).Status(context.Background(), "p")
			if out.Pipeline.State != tc.state || (tc.code != "" && !hasIssue(out, tc.code, tc.scope)) {
				t.Fatalf("prerequisites: %+v", out)
			}
		})
	}
}

func TestUnusedEnvironmentAndTablesDoNotBlockScript(t *testing.T) {
	db, svc := fixture(t)
	project(t, db, "p", stamp)
	config(t, db, "p", spec(job("script")), true)
	st := pipeline.DefaultSettings()
	st.Build.Toolchain = pipeline.Toolchain{}
	st.Build.Model = pipeline.BuildModelToolchain
	st.Environments = []pipeline.Environment{{Name: "unused", EnvVars: []pipeline.BuildVar{{Key: "SECRET_VAR", Secret: true, CredentialID: "deleted"}}, ImageRegistry: pipeline.ImageRegistry{Type: "custom", CredentialID: "deleted"}}}
	settings(t, db, "p", st)
	out := svc.Status(context.Background(), "p")
	if out.Pipeline.State != "ready" {
		t.Fatalf("unused configuration blocks: %+v", out)
	}
	storetest.SkipIfMySQL(t)
	exec(t, db, `DROP TABLE pipeline_triggers`)
	exec(t, db, `DROP TABLE servers`)
	if out = svc.Status(context.Background(), "p"); out.Pipeline.State != "ready" {
		t.Fatalf("unused tables block: %+v", out)
	}
}

type blockedTransport struct{ attempts atomic.Int64 }

func (b *blockedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	b.attempts.Add(1)
	return nil, errors.New("outbound blocked")
}
func TestNoOutboundAttempts(t *testing.T) {
	blocked := &blockedTransport{}
	prior := http.DefaultTransport
	http.DefaultTransport = blocked
	defer func() { http.DefaultTransport = prior }()
	db, svc := fixture(t)
	project(t, db, "p", stamp)
	config(t, db, "p", spec(job("script")), true)
	for i := 0; i < 3; i++ {
		svc.Status(context.Background(), "p")
	}
	exec(t, db, `UPDATE projects SET pac_enabled=1 WHERE id='p'`)
	svc.Status(context.Background(), "p")
	if blocked.attempts.Load() != 0 {
		t.Fatalf("outbound attempts: %d", blocked.attempts.Load())
	}
}

func TestActualExecutorAndRemotePrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, task, state, code, scope string
		legacy, serverExists                    bool
	}{
		{"startup_stub_remote", RuntimeStub, "script", "needs_configuration", "runtime_stub", pipeline.ScopeVars, false, false},
		{"remote_missing_server", RuntimeAvailable, "script", "needs_configuration", "server_missing", pipeline.ScopeVars, false, false},
		{"remote_connection_unknown", RuntimeAvailable, "script", "unconfirmed", "", "", false, true},
		{"remote_omitted_image", RuntimeAvailable, "build_image", "needs_configuration", "task_execution_unavailable", pipeline.ScopeCanvas, false, true},
		{"legacy_ignores_remote", RuntimeAvailable, "git_source", "ready", "", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := fixture(t)
			project(t, db, "p", stamp)
			config(t, db, "p", spec(job(tc.task)), true)
			exec(t, db, `INSERT INTO project_runners (project_id,runner_server_id,created_at,updated_at) VALUES ('p','remote',?,?)`, stamp, stamp)
			if tc.serverExists {
				exec(t, db, `INSERT INTO servers (id,name,host,user,credential_id,created_at,updated_at) VALUES ('remote','remote','SECRET_HOST','SECRET_USER','cred',?,?)`, stamp, stamp)
			}
			out := New(db, Capabilities{Runtime: tc.runtime, Legacy: tc.legacy, VaultConfigured: true}).Status(context.Background(), "p")
			wantRuntime := tc.runtime
			if !tc.legacy && tc.runtime != RuntimeStub {
				wantRuntime = RuntimeUnknown
			}
			if out.Runtime != wantRuntime || out.Pipeline.State != tc.state || (tc.code != "" && !hasIssue(out, tc.code, tc.scope)) {
				t.Fatalf("actual executor: %+v", out)
			}
		})
	}
}

func TestOnlyApplicableImageEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, branch, registryURL, credential, state, code, scope string }{
		{"unused_mapping", "other", "", "", "ready", "", ""},
		{"missing_environment", "main", "", "", "needs_configuration", "environment_undefined", pipeline.ScopeTriggers},
		{"unused_env_credentials", "main", "", "", "ready", "", ""},
		{"optional_incomplete_registry", "main", "", "deleted", "ready", "", ""},
		{"consumed_registry", "main", "registry.example", "deleted", "needs_configuration", "credential_missing", pipeline.ScopeEnvs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, svc := fixture(t)
			project(t, db, "p", stamp)
			config(t, db, "p", spec(job("build_image")), true)
			mapping, _ := json.Marshal([]map[string]any{{"branchPattern": tc.branch, "environment": "prod", "targetServerIds": []string{"deleted-server"}}})
			exec(t, db, `INSERT INTO pipeline_triggers (project_id,webhook_token,webhook_secret_ciphertext,branch_mappings_json,created_at,updated_at) VALUES ('p','token',X'00',?,?,?)`, string(mapping), stamp, stamp)
			if tc.name != "missing_environment" {
				st := pipeline.DefaultSettings()
				st.Environments = []pipeline.Environment{{Name: "prod", EnvVars: []pipeline.BuildVar{{Key: "SECRET_UNUSED", Secret: true, CredentialID: "deleted"}}, ImageRegistry: pipeline.ImageRegistry{Type: "custom", URL: tc.registryURL, CredentialID: tc.credential}}}
				settings(t, db, "p", st)
			}
			out := svc.Status(context.Background(), "p")
			if out.Pipeline.State != tc.state || (tc.code != "" && !hasIssue(out, tc.code, tc.scope)) {
				t.Fatalf("environment applicability: %+v", out)
			}
		})
	}
}

func TestSkippedTaskDoesNotBecomeReady(t *testing.T) {
	db, svc := fixture(t)
	project(t, db, "p", stamp)
	sp := spec(job("script"))
	sp.Stages[1].When = pipeline.When{Branches: []string{"other"}}
	config(t, db, "p", sp, true)
	out := svc.Status(context.Background(), "p")
	if out.Pipeline.State != "needs_configuration" || !hasIssue(out, "no_tasks", pipeline.ScopeCanvas) {
		t.Fatal(out)
	}
}

func TestNotificationReadinessRequiresEnabledChannel(t *testing.T) {
	for _, enabled := range []int{0, 1} {
		db, svc := fixture(t)
		project(t, db, "p", stamp)
		jb := job("notify")
		jb.Config = map[string]any{"channel": "channel"}
		config(t, db, "p", spec(jb), true)
		exec(t, db, `INSERT INTO notification_channels (id,name,type,enabled,config_json,created_at,updated_at) VALUES ('channel','channel','webhook',?,'{}',?,?)`, enabled, stamp, stamp)
		out := svc.Status(context.Background(), "p")
		if enabled == 0 {
			if out.Pipeline.State != "needs_configuration" || !hasIssue(out, "notification_channel_missing", pipeline.ScopeCanvas) {
				t.Fatal(out)
			}
		} else if out.Pipeline.State != "ready" {
			t.Fatal(out)
		}
	}
}

func TestCommandDeploymentReadinessRequiresCommand(t *testing.T) {
	for _, command := range []string{"", "  ", "true"} {
		db, svc := fixture(t)
		project(t, db, "p", stamp)
		exec(t, db, `INSERT INTO servers (id,name,host,user,credential_id,created_at,updated_at) VALUES ('server','test','localhost','test','cred',?,?)`, stamp, stamp)
		jb := job("deploy_ssh")
		jb.Config = map[string]any{"serverId": "server", "artifactType": "command", "restartCommand": command}
		config(t, db, "p", spec(jb), true)
		out := svc.Status(context.Background(), "p")
		if strings.TrimSpace(command) == "" {
			if out.Pipeline.State != "needs_configuration" || !hasIssue(out, "script_incomplete", pipeline.ScopeCanvas) {
				t.Fatal(out)
			}
		} else if out.Pipeline.State != "ready" {
			t.Fatal(out)
		}
	}
}

func TestMatrixReadinessUsesEffectiveRunnerPlan(t *testing.T) {
	db, svc := fixture(t)
	project(t, db, "p", stamp)
	sp := spec()
	sp.Stages[1].Matrix = map[string][]string{"os": {"linux"}}
	sp.Stages[1].Post = []pipeline.PostStep{{Condition: pipeline.PostAlways, Image: "alpine", Commands: []string{"true"}}}
	config(t, db, "p", sp, true)
	out := svc.Status(context.Background(), "p")
	if out.Pipeline.State != "needs_configuration" || !hasIssue(out, "no_tasks", pipeline.ScopeCanvas) {
		t.Fatal(out)
	}
}

func TestCorruptConfigurationPreservesSuccess(t *testing.T) {
	for _, table := range []string{"pipeline_configs", "pipeline_settings"} {
		t.Run(table, func(t *testing.T) {
			db, svc := fixture(t)
			project(t, db, "p", stamp)
			config(t, db, "p", spec(job("script")), true)
			runRow(t, db, "good", "p", "success", "real", stamp)
			if table == "pipeline_configs" {
				exec(t, db, `UPDATE pipeline_configs SET spec_json='{' WHERE project_id='p'`)
			} else {
				settings(t, db, "p", pipeline.DefaultSettings())
				exec(t, db, `UPDATE pipeline_settings SET build_json='{' WHERE project_id='p'`)
			}
			out := svc.Status(context.Background(), "p")
			if out.Pipeline.State != Unknown || out.SuccessState != Known || out.Success == nil {
				t.Fatal(out)
			}
		})
	}
}

func TestRunStorageFailureDoesNotMasqueradeAsEmpty(t *testing.T) {
	storetest.SkipIfMySQL(t)
	db, svc := fixture(t)
	project(t, db, "p", stamp)
	exec(t, db, `ALTER TABLE pipeline_runs RENAME TO broken_runs`)
	out := svc.Status(context.Background(), "p")
	if out.ProjectsState != Known || out.RunsState != Unknown || out.RunCount != nil || out.SuccessState != Unknown {
		t.Fatal(out)
	}
}
