package onboarding

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/trigger"
)

func readinessMapping(t *testing.T, db *sql.DB, branch, environment string) {
	t.Helper()
	laterBranch := "release/other"
	if branch == "release/*" {
		laterBranch = "release/current"
	}
	raw, err := json.Marshal([]trigger.BranchMapping{
		{BranchPattern: branch, Environment: environment, TargetServerIDs: []string{"ignored-trigger-target"}},
		{BranchPattern: laterBranch, Environment: "unused"},
	})
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO pipeline_triggers (project_id,webhook_token,webhook_secret_ciphertext,branch_mappings_json,created_at,updated_at) VALUES ('p','token',X'00',?,?,?)`, string(raw), stamp, stamp)
}

func readinessServer(t *testing.T, db *sql.DB, id, credential string) {
	t.Helper()
	exec(t, db, `INSERT INTO servers (id,name,host,user,credential_id,created_at,updated_at) VALUES (?,?,'localhost','test',?,?,?)`, id, id, credential, stamp, stamp)
}

func readinessJob(mode string) pipeline.Job {
	jb := job("script")
	switch mode {
	case "image":
		jb.Type = "build_image"
	case "file":
		jb.Type = "build_image"
		jb.Config = map[string]any{"buildModel": pipeline.BuildModelToolchain, "artifactType": pipeline.ArtifactDist, "toolchainLanguage": "node", "toolchainVersion": "20"}
	case "legacy":
		jb.Type = "git_source"
	}
	return jb
}

func TestReadinessConsumedEnvironmentVariables(t *testing.T) {
	for _, mode := range []string{"script", "image", "file", "legacy"} {
		for _, tc := range []struct {
			name, branch, envName, credential, state string
			overrideGlobal                           bool
		}{
			{"selected_secret", "release/*", "prod", "cred", "ready", false},
			{"selected_missing_secret", "release/*", "prod", "deleted", "needs_configuration", false},
			{"selected_empty_secret", "release/*", "prod", "", "needs_configuration", false},
			{"unmatched_environment", "other", "prod", "deleted", "ready", false},
			{"case_sensitive_environment", "release/*", "Prod", "deleted", "needs_configuration", false},
			{"environment_plaintext_wins", "release/*", "prod", "", "ready", true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				db, _ := fixture(t)
				project(t, db, "p", stamp)
				exec(t, db, `UPDATE projects SET default_branch='release/current' WHERE id='p'`)
				config(t, db, "p", spec(readinessJob(mode)), true)
				readinessMapping(t, db, tc.branch, tc.envName)
				st := pipeline.DefaultSettings()
				selected := pipeline.BuildVar{Key: "TOKEN", Secret: true, CredentialID: tc.credential}
				if tc.overrideGlobal {
					st.Build.Vars = []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: "deleted"}}
					selected = pipeline.BuildVar{Key: "TOKEN", Value: "plain"}
				}
				st.Environments = []pipeline.Environment{
					{Name: "prod", EnvVars: []pipeline.BuildVar{selected}},
					{Name: "unused", EnvVars: []pipeline.BuildVar{{Key: "UNUSED", Secret: true, CredentialID: "deleted"}}},
				}
				settings(t, db, "p", st)
				out := New(db, Capabilities{Runtime: RuntimeAvailable, Legacy: mode == "legacy", VaultConfigured: true}).Status(context.Background(), "p")
				want := tc.state
				// No selected environment means no EnvVars are consumed.
				if (tc.name == "unmatched_environment" || tc.name == "case_sensitive_environment") && (mode == "script" || mode == "file") {
					want = "ready"
				}
				if out.Pipeline.State != want {
					t.Fatalf("consumption = %+v, want %s", out.Pipeline, want)
				}
				missing := strings.HasPrefix(tc.name, "selected_") && tc.credential != "cred"
				if hasIssue(out, "credential_missing", pipeline.ScopeEnvs) != missing {
					t.Fatalf("secret applicability = %+v", out.Pipeline)
				}
			})
		}
	}
}

func TestReadinessBuildImageGlobalMissingSecret(t *testing.T) {
	for _, mode := range []string{"image", "file"} {
		t.Run(mode, func(t *testing.T) {
			db, svc := fixture(t)
			project(t, db, "p", stamp)
			config(t, db, "p", spec(readinessJob(mode)), true)
			st := pipeline.DefaultSettings()
			st.Build.Vars = []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: "deleted"}}
			settings(t, db, "p", st)
			out := svc.Status(context.Background(), "p")
			if out.Pipeline.State != "needs_configuration" || !hasIssue(out, "credential_missing", pipeline.ScopeEnvs) {
				t.Fatalf("build variables not checked: %+v", out.Pipeline)
			}
		})
	}
}

func TestReadinessScriptMatrixWinsAndLegacyStepReferences(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "matrix", true: "legacy_step"}[legacy], func(t *testing.T) {
			db, _ := fixture(t)
			project(t, db, "p", stamp)
			jb := job("script")
			jb.Config["__matrixEnv"] = map[string]string{"TOKEN": "plain"}
			config(t, db, "p", spec(jb), true)
			readinessMapping(t, db, "main", "prod")
			st := pipeline.DefaultSettings()
			st.Environments = []pipeline.Environment{{Name: "prod", EnvVars: []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: "deleted"}}}}
			if legacy {
				st.Environments[0].EnvVars[0].Secret = false
				st.Steps = []pipeline.PipelineStep{{Type: pipeline.StepTypeScript, Image: "alpine", Commands: []string{"true"}, Env: []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: "deleted"}}}}
			}
			settings(t, db, "p", st)
			out := New(db, Capabilities{Runtime: RuntimeAvailable, Legacy: legacy, VaultConfigured: true}).Status(context.Background(), "p")
			want := "ready"
			if legacy {
				want = "needs_configuration"
			}
			if out.Pipeline.State != want || hasIssue(out, "credential_missing", pipeline.ScopeEnvs) != legacy {
				t.Fatalf("node precedence = %+v", out.Pipeline)
			}
		})
	}
}

func TestReadinessDeploymentEnvironmentFallback(t *testing.T) {
	for _, typ := range []string{"deploy_ssh", "deploy_frontend"} {
		for _, tc := range []struct {
			name, explicit, envName, state, code string
			targets                              []string
		}{
			{"all_unique_targets", "", "prod", "ready", "", []string{"", " a ", "a", "b", " "}},
			{"missing_one_target", "", "prod", "needs_configuration", "server_missing", []string{"a", "deleted"}},
			{"missing_target_credential", "", "prod", "needs_configuration", "credential_missing", []string{"a", "bad"}},
			{"empty_targets", "", "prod", "needs_configuration", "server_missing", nil},
			{"blank_targets", "", "prod", "needs_configuration", "server_missing", []string{" ", ""}},
			{"no_environment", "", "", "needs_configuration", "server_missing", []string{"a"}},
			{"undefined_environment", "", "undefined", "needs_configuration", "server_missing", []string{"a"}},
			{"explicit_wins", "a", "prod", "ready", "", []string{"deleted", "bad"}},
			{"explicit_missing", "deleted", "prod", "needs_configuration", "server_missing", []string{"a"}},
		} {
			t.Run(typ+"/"+tc.name, func(t *testing.T) {
				db, svc := fixture(t)
				project(t, db, "p", stamp)
				// A deployment-only flow does not consume build secrets.
				jb := job(typ)
				jb.Config["serverId"] = tc.explicit
				sp := spec(jb)
				sp.Stages[1].Kind = pipeline.KindDeploy
				config(t, db, "p", sp, true)
				readinessMapping(t, db, "main", tc.envName)
				readinessServer(t, db, "a", "cred")
				readinessServer(t, db, "b", "cred")
				readinessServer(t, db, "bad", "cred")
				if tc.name == "missing_target_credential" || tc.name == "explicit_wins" {
					storetest.SkipIfMySQL(t)
					exec(t, db, `PRAGMA foreign_keys=OFF`)
					exec(t, db, `UPDATE servers SET credential_id='deleted' WHERE id='bad'`)
					exec(t, db, `PRAGMA foreign_keys=ON`)
				}
				st := pipeline.DefaultSettings()
				st.Build.Vars = []pipeline.BuildVar{{Key: "GLOBAL_UNUSED", Secret: true, CredentialID: "deleted"}}
				st.Environments = []pipeline.Environment{
					{Name: "prod", TargetServerIDs: tc.targets, EnvVars: []pipeline.BuildVar{{Key: "ENV_UNUSED", Secret: true, CredentialID: "deleted"}}},
					{Name: "unused", TargetServerIDs: []string{"deleted"}},
				}
				settings(t, db, "p", st)
				out := svc.Status(context.Background(), "p")
				scope := pipeline.ScopeCanvas
				if tc.code == "credential_missing" {
					scope = pipeline.ScopeEnvs
				}
				if out.Pipeline.State != tc.state || (tc.code != "" && !hasIssue(out, tc.code, scope)) {
					t.Fatalf("deployment applicability = %+v", out.Pipeline)
				}
			})
		}
	}
}

func TestReadinessTriggerErrorsOnlyForConsumers(t *testing.T) {
	storetest.SkipIfMySQL(t)
	for _, fault := range []string{"malformed", "missing_table"} {
		for _, mode := range []string{"script", "file", "legacy", "fallback", "explicit"} {
			t.Run(fault+"/"+mode, func(t *testing.T) {
				db, _ := fixture(t)
				project(t, db, "p", stamp)
				jb := readinessJob(mode)
				if mode == "fallback" || mode == "explicit" {
					jb.Type = "deploy_ssh"
					if mode == "explicit" {
						jb.Config["serverId"] = "a"
						readinessServer(t, db, "a", "cred")
					}
				}
				config(t, db, "p", spec(jb), true)
				st := pipeline.DefaultSettings()
				st.Environments = []pipeline.Environment{{Name: "prod", EnvVars: []pipeline.BuildVar{{Key: "TOKEN", Secret: true, CredentialID: "deleted"}}}}
				settings(t, db, "p", st)
				readinessMapping(t, db, "main", "prod")
				if fault == "malformed" {
					exec(t, db, `UPDATE pipeline_triggers SET branch_mappings_json='{' WHERE project_id='p'`)
				} else {
					exec(t, db, `DROP TABLE pipeline_triggers`)
					if mode == "explicit" {
						exec(t, db, `DROP TABLE pipeline_settings`)
					}
				}
				out := New(db, Capabilities{Runtime: RuntimeAvailable, Legacy: mode == "legacy", VaultConfigured: true}).Status(context.Background(), "p")
				want := Unknown
				if mode == "explicit" {
					want = "ready"
				}
				if out.Pipeline.State != want || hasIssue(out, "triggers_unavailable", pipeline.ScopeTriggers) != (mode != "explicit") {
					t.Fatalf("trigger dependency = %+v", out.Pipeline)
				}
			})
		}
	}
}

func TestReadinessEnvironmentChecksRemainReadOnlyAndLocal(t *testing.T) {
	storetest.SkipIfMySQL(t)
	blocked := &blockedTransport{}
	prior := http.DefaultTransport
	http.DefaultTransport = blocked
	defer func() { http.DefaultTransport = prior }()
	for _, mode := range []string{"script", "image", "file", "legacy", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := fixture(t)
			project(t, db, "p", stamp)
			jb := readinessJob(mode)
			if mode == "fallback" {
				jb.Type = "deploy_ssh"
				readinessServer(t, db, "a", "cred")
			}
			config(t, db, "p", spec(jb), true)
			readinessMapping(t, db, "main", "prod")
			st := pipeline.DefaultSettings()
			st.Environments = []pipeline.Environment{{Name: "prod", TargetServerIDs: []string{"a"}, EnvVars: []pipeline.BuildVar{{Key: "SECRET_VAR", Secret: true, CredentialID: "cred"}}}}
			settings(t, db, "p", st)
			before := businessSnapshot(t, db)
			exec(t, db, `PRAGMA query_only=ON`)
			svc := New(db, Capabilities{Runtime: RuntimeAvailable, Legacy: mode == "legacy", VaultConfigured: true})
			for i := 0; i < 3; i++ {
				out := svc.Status(context.Background(), "p")
				if out.Pipeline.State != "ready" {
					t.Fatalf("read-only status = %+v", out.Pipeline)
				}
				raw, _ := json.Marshal(out)
				if strings.Contains(string(raw), "SECRET_") {
					t.Fatalf("unsafe snapshot: %s", raw)
				}
			}
			if !reflect.DeepEqual(before, businessSnapshot(t, db)) {
				t.Fatal("environment readiness changed business rows")
			}
		})
	}
	if blocked.attempts.Load() != 0 {
		t.Fatalf("outbound attempts: %d", blocked.attempts.Load())
	}
}
