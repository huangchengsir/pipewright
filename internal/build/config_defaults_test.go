package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/buildcache"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type defaultsCall struct {
	kind, image string
	env         []string
	secret      []string
}

type defaultsDriver struct {
	Driver
	mu      sync.Mutex
	calls   []defaultsCall
	code    int
	onRun   func(string)
	outputs string
}

func (*defaultsDriver) Binary() string { return "fake" }
func (d *defaultsDriver) record(call defaultsCall) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, call)
}
func (d *defaultsDriver) Build(_ context.Context, _, _, _ string, plain, secret []string, _ func(string, string)) (int, error) {
	d.record(defaultsCall{kind: "build", env: append([]string(nil), plain...), secret: append([]string(nil), secret...)})
	return d.code, nil
}
func (d *defaultsDriver) RunToolchain(_ context.Context, image, ws, _ string, env, _ []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	d.record(defaultsCall{kind: "toolchain", image: image, env: append([]string(nil), env...)})
	if image == "producer" {
		if err := os.WriteFile(filepath.Join(ws, pipewrightEnvFileName), []byte(d.outputs), 0o600); err != nil {
			return -1, err
		}
	}
	if d.onRun != nil {
		d.onRun(ws)
	}
	return d.code, nil
}
func (*defaultsDriver) InspectImage(context.Context, string) (string, int64, error) {
	return "", 0, nil
}

func defaultsStage(mode string, jobs ...pipeline.Job) pipeline.Stage {
	if mode == "dag" {
		for i := range jobs {
			jobs[i].ID = jobs[i].Name
			if i > 0 {
				jobs[i].Needs = []string{jobs[i-1].ID}
			}
		}
		if len(jobs) == 1 {
			jobs = append([]pipeline.Job{{ID: "source", Name: "source", Type: "git_source"}}, jobs...)
			jobs[1].Needs = []string{"source"}
		}
	}
	return scriptStage(jobs...)
}

func envMap(t *testing.T, values []string) map[string]string {
	t.Helper()
	got := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			t.Fatalf("invalid env entry %q", value)
		}
		if _, duplicate := got[key]; duplicate {
			t.Fatalf("duplicate key %s at driver boundary", key)
		}
		got[key] = val
	}
	return got
}

func varsSettings() *pipeline.Settings {
	return &pipeline.Settings{
		Build: pipeline.BuildConfig{Vars: []pipeline.BuildVar{
			{Key: "GLOBAL", Value: "global"},
			{Key: "PLAIN_WINS", Secret: true, CredentialID: "old-secret"},
			{Key: "SECRET_WINS", Value: "old-plain"},
		}},
		Environments: []pipeline.Environment{
			{Name: "first", EnvVars: []pipeline.BuildVar{{Key: "WRONG_ENV", Value: "never"}}},
			{Name: "prod", EnvVars: []pipeline.BuildVar{
				{Key: "ENV", Value: "selected"},
				{Key: "PLAIN_WINS", Value: "last-plain"},
				{Key: "SECRET_WINS", Secret: true, CredentialID: "new-secret"},
			}},
		},
	}
}

func defaultsSecrets() fakeVault {
	return fakeVault{secrets: map[string]string{"old-secret": "old-secret-value", "new-secret": "new-secret-value"}}
}

func TestEffectiveBuildVariablesAtDriverBoundary(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		for _, artifact := range []string{pipeline.ArtifactImage, pipeline.ArtifactDist, pipeline.ArtifactJAR} {
			for _, envName := range []string{"prod", " prod ", "first", "unknown", ""} {
				t.Run(mode+"/"+artifact+"/"+envName, func(t *testing.T) {
					drv := &defaultsDriver{}
					b := newDAGTestBuilder(drv, &markerCloner{})
					settings := varsSettings()
					b.settings = fakeSettings{settings: settings}
					b.vault = defaultsSecrets()
					jb := pipeline.Job{Name: "build", Type: "build_image", Config: map[string]any{
						"artifactType": artifact, "toolchainLanguage": "node", "toolchainVersion": "20",
					}}
					r := &run.Run{ID: "run", ProjectID: "p1", Trigger: run.Trigger{ResolvedEnvironment: envName}}
					if err := NewStageExecutor(b, nil)(context.Background(), r, defaultsStage(mode, jb), newPushReport()); err != nil {
						t.Fatal(err)
					}
					if len(drv.calls) != 1 {
						t.Fatalf("driver calls = %d", len(drv.calls))
					}
					call := drv.calls[0]
					got := envMap(t, append(append([]string(nil), call.env...), call.secret...))
					want := map[string]string{"GLOBAL": "global", "PLAIN_WINS": "old-secret-value", "SECRET_WINS": "old-plain"}
					switch envName {
					case "prod", " prod ":
						want["ENV"], want["PLAIN_WINS"], want["SECRET_WINS"] = "selected", "last-plain", "new-secret-value"
					case "first":
						want["WRONG_ENV"] = "never"
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("driver vars = %v, want %v", got, want)
					}
					if artifact == pipeline.ArtifactImage && strings.TrimSpace(envName) == "prod" && !reflect.DeepEqual(call.secret, []string{"SECRET_WINS=new-secret-value"}) {
						t.Fatalf("secret build args = %v", call.secret)
					}
					if !reflect.DeepEqual(settings, varsSettings()) {
						t.Fatal("execution mutated shared settings")
					}
				})
			}
		}
	}
}

func TestEffectiveScriptVariablesAtDriverBoundary(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		for _, envName := range []string{"prod", "unknown", ""} {
			t.Run(mode+"/"+envName, func(t *testing.T) {
				drv := &defaultsDriver{outputs: "UPSTREAM=upstream\nBUILD_ORDER=upstream\nENV_ORDER=upstream\n"}
				b := newDAGTestBuilder(drv, &markerCloner{})
				settings := varsSettings()
				settings.Build.Vars = append(settings.Build.Vars,
					pipeline.BuildVar{Key: "BUILD_ORDER", Value: "build"},
					pipeline.BuildVar{Key: "ENV_ORDER", Value: "build"},
					pipeline.BuildVar{Key: "PIPEWRIGHT_ENV", Secret: true, CredentialID: "old-secret"})
				settings.Environments[1].EnvVars = append(settings.Environments[1].EnvVars,
					pipeline.BuildVar{Key: "ENV_ORDER", Value: "environment"},
					pipeline.BuildVar{Key: "NODE_ORDER", Secret: true, CredentialID: "new-secret"})
				b.settings = fakeSettings{settings: settings}
				b.vault = defaultsSecrets()
				producer := scriptJob("produce", "producer", "true")
				consumer := scriptJobWithConfig("consume", "consumer", "true", map[string]any{
					"__matrixEnv": map[string]string{"NODE_ORDER": "node", "PIPEWRIGHT_ENV": "wrong-node-path"},
				})
				r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{ResolvedEnvironment: envName, Params: map[string]string{
					"PARAM": "param", "UPSTREAM": "param", "BUILD_ORDER": "param", "ENV_ORDER": "param",
				}}}
				if err := NewStageExecutor(b, nil)(context.Background(), r, defaultsStage(mode, producer, consumer), newPushReport()); err != nil {
					t.Fatal(err)
				}
				call := drv.calls[1]
				got := envMap(t, call.env)
				want := map[string]string{
					"GLOBAL": "global", "PARAM": "param", "UPSTREAM": "upstream", "BUILD_ORDER": "build", "ENV_ORDER": "build",
					"NODE_ORDER": "node", "PLAIN_WINS": "old-secret-value", "SECRET_WINS": "old-plain",
					"PIPEWRIGHT_ENV": pipewrightEnvVar().Value,
				}
				if envName == "prod" {
					want["ENV"], want["ENV_ORDER"], want["PLAIN_WINS"], want["SECRET_WINS"] = "selected", "environment", "last-plain", "new-secret-value"
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("script vars = %v, want %v", got, want)
				}
				if last := call.env[len(call.env)-1]; last != "PIPEWRIGHT_ENV="+pipewrightEnvVar().Value {
					t.Fatalf("system env must be last, got %q", last)
				}
			})
		}
	}
}

type revealRecordingVault struct {
	vault.Vault
	ids []string
}

func (v *revealRecordingVault) Reveal(id string) (string, error) {
	v.ids = append(v.ids, id)
	if id == "missing" {
		return "", vault.ErrNotFound
	}
	return "revealed", nil
}

func TestVariablesDeduplicateBeforeSecretResolution(t *testing.T) {
	for _, boundary := range []string{"build", "script"} {
		t.Run(boundary, func(t *testing.T) {
			v := &revealRecordingVault{}
			b := &Builder{vault: v}
			vars := []pipeline.BuildVar{
				{Key: "PLAIN", Secret: true, CredentialID: "must-not-reveal"},
				{Key: "PLAIN", Value: "last"},
				{Key: "SECRET", Value: "first"},
				{Key: "SECRET", Secret: true, CredentialID: "winner"},
				{Key: "MISSING", Value: "must-not-revive"},
				{Key: "MISSING", Secret: true, CredentialID: "missing"},
				{Key: "PIPEWRIGHT_ENV", Secret: true, CredentialID: "must-not-reveal-system"},
				pipewrightEnvVar(),
			}
			var env []string
			if boundary == "build" {
				plain, secret := b.buildArgs(vars)
				env = append(plain, secret...)
			} else {
				env = b.buildEnv(vars)
			}
			if !reflect.DeepEqual(envMap(t, env), map[string]string{"PLAIN": "last", "SECRET": "revealed", "PIPEWRIGHT_ENV": pipewrightEnvVar().Value}) {
				t.Fatalf("unexpected effective variables: %v", env)
			}
			if !reflect.DeepEqual(v.ids, []string{"winner", "missing"}) {
				t.Fatalf("revealed overridden secret: %v", v.ids)
			}
		})
	}
}

func TestLegacyBuilderUsesSelectedVariables(t *testing.T) {
	for _, envName := range []string{"prod", "unknown", ""} {
		t.Run(envName, func(t *testing.T) {
			drv := &defaultsDriver{}
			b := newDAGTestBuilder(drv, &markerCloner{})
			settings := varsSettings()
			settings.Build.ArtifactType = pipeline.ArtifactImage
			settings.Steps = []pipeline.PipelineStep{{Name: "script", Type: pipeline.StepTypeScript, Image: "consumer", Commands: []string{"true"}, Env: []pipeline.BuildVar{
				{Key: "SECRET_WINS", Value: "node-plain"},
				{Key: "PLAIN_WINS", Secret: true, CredentialID: "new-secret"},
				{Key: "PIPEWRIGHT_ENV", Secret: true, CredentialID: "old-secret"},
			}}}
			b.settings = fakeSettings{settings: settings}
			b.vault = defaultsSecrets()
			b.disableImageGC = true
			r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{ResolvedEnvironment: envName, Params: map[string]string{"GLOBAL": "param", "PARAM": "param"}}}
			if err := b.Run(context.Background(), r, newFakeSink()); err != nil {
				t.Fatal(err)
			}
			build := envMap(t, append(drv.calls[0].env, drv.calls[0].secret...))
			script := envMap(t, drv.calls[1].env)
			if build["GLOBAL"] != "global" || script["GLOBAL"] != "global" || script["PARAM"] != "param" || script["SECRET_WINS"] != "node-plain" || script["PLAIN_WINS"] != "new-secret-value" {
				t.Fatalf("legacy precedence: build=%v script=%v", build, script)
			}
			for _, vars := range []map[string]string{build, script} {
				if _, ok := vars["WRONG_ENV"]; ok || (vars["ENV"] == "selected") != (envName == "prod") {
					t.Fatalf("legacy guessed environment: %v", vars)
				}
			}
			if last := drv.calls[1].env[len(drv.calls[1].env)-1]; last != "PIPEWRIGHT_ENV="+pipewrightEnvVar().Value {
				t.Fatalf("legacy system env is not last: %q", last)
			}
			if settings.Steps[0].Env[0].Value != "node-plain" || len(settings.Steps[0].Env) != 3 || len(settings.Build.Vars) != 3 {
				t.Fatal("legacy mutated settings")
			}
		})
	}
}

func TestSelectedVariablesRetainMaskedCommandEcho(t *testing.T) {
	for _, artifact := range []string{pipeline.ArtifactImage, pipeline.ArtifactDist} {
		t.Run(artifact, func(t *testing.T) {
			cmdr := newFakeCommander()
			b := newDAGTestBuilder(&shellDriver{bin: "docker", cmdr: cmdr}, &markerCloner{})
			b.settings = fakeSettings{settings: varsSettings()}
			b.vault = defaultsSecrets()
			jb := pipeline.Job{Name: "build", Type: "build_image", Config: map[string]any{"artifactType": artifact, "toolchainLanguage": "node"}}
			r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{ResolvedEnvironment: "prod"}}
			rep := newPushReport()
			if err := NewStageExecutor(b, nil)(context.Background(), r, scriptStage(jb), rep); err != nil {
				t.Fatal(err)
			}
			logs := strings.Join(rep.logs, "\n")
			if strings.Contains(logs, "new-secret-value") || strings.Contains(logs, "old-secret-value") || !strings.Contains(logs, "SECRET_WINS=***") {
				t.Fatalf("command echo lost masking: %s", logs)
			}
			if !strings.Contains(cmdr.allArgsText(), "SECRET_WINS=new-secret-value") {
				t.Fatal("secret was not injected into driver argv")
			}
		})
	}
}

type defaultsCache struct {
	t              *testing.T
	restoreErr     error
	saveErr        error
	restore, save  int
	key, workspace string
	paths          []string
}

func (c *defaultsCache) Restore(_ context.Context, key string, paths []string, ws string) (bool, error) {
	c.restore++
	c.key, c.workspace, c.paths = key, ws, append([]string(nil), paths...)
	if c.restoreErr != nil {
		return false, c.restoreErr
	}
	if err := os.WriteFile(filepath.Join(ws, "cache-restored"), nil, 0o600); err != nil {
		c.t.Fatal(err)
	}
	return true, nil
}
func (c *defaultsCache) Save(_ context.Context, key string, paths []string, ws string) error {
	c.save++
	if ws != c.workspace || key != c.key || !reflect.DeepEqual(paths, c.paths) {
		c.t.Fatal("cache restore/save configuration differs")
	}
	if _, err := os.Stat(filepath.Join(ws, "cache-built")); err != nil {
		c.t.Fatal("cache saved before execution")
	}
	return c.saveErr
}

func TestEffectiveCacheAtExecutionBoundary(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		for _, kind := range []string{"script", pipeline.ArtifactDist, pipeline.ArtifactJAR, pipeline.ArtifactImage} {
			for _, tc := range []struct {
				name    string
				global  pipeline.Cache
				node    map[string]any
				want    []string
				code    int
				restErr error
				saveErr error
			}{
				{name: "inherit", global: pipeline.Cache{Enabled: true, Paths: []string{"node_modules", ".npm"}}, want: []string{"node_modules", ".npm"}},
				{name: "empty_global", global: pipeline.Cache{Enabled: true}},
				{name: "disabled_global", global: pipeline.Cache{Paths: []string{"default"}}},
				{name: "explicit_empty", global: pipeline.Cache{Enabled: true, Paths: []string{"default"}}, node: map[string]any{"cachePaths": ""}},
				{name: "explicit_nil", global: pipeline.Cache{Enabled: true, Paths: []string{"default"}}, node: map[string]any{"cachePaths": nil}},
				{name: "explicit_wins", global: pipeline.Cache{Enabled: true, Paths: []string{"default"}}, node: map[string]any{"cachePaths": "own"}, want: []string{"own"}},
				{name: "explicit_survives_false", node: map[string]any{"cachePaths": "own"}, want: []string{"own"}},
				{name: "failed_execution", global: pipeline.Cache{Enabled: true, Paths: []string{"default"}}, want: []string{"default"}, code: 1},
				{name: "restore_failure", global: pipeline.Cache{Enabled: true, Paths: []string{"default"}}, want: []string{"default"}, restErr: errors.New("unavailable")},
				{name: "save_failure", global: pipeline.Cache{Enabled: true, Paths: []string{"default"}}, want: []string{"default"}, saveErr: errors.New("unavailable")},
			} {
				t.Run(mode+"/"+kind+"/"+tc.name, func(t *testing.T) {
					cacheable := kind != pipeline.ArtifactImage && len(tc.want) > 0
					cache := &defaultsCache{t: t, restoreErr: tc.restErr, saveErr: tc.saveErr}
					drv := &defaultsDriver{code: tc.code}
					drv.onRun = func(ws string) {
						_, err := os.Stat(filepath.Join(ws, "cache-restored"))
						if (err == nil) != (cacheable && tc.restErr == nil) {
							t.Fatal("cache restore did not precede execution")
						}
						if err := os.WriteFile(filepath.Join(ws, "cache-built"), nil, 0o600); err != nil {
							t.Fatal(err)
						}
						if cacheable && cache.restore != 1 {
							t.Fatal("cache was not restored once before execution")
						}
					}
					b := newDAGTestBuilder(drv, &markerCloner{})
					b.buildCache = cache
					b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{Cache: tc.global}}}
					jb := scriptJobWithConfig("job", "node:20", "true", tc.node)
					if kind != "script" {
						jb.Type = "build_image"
						jb.Config["artifactType"], jb.Config["toolchainLanguage"] = kind, "node"
					}
					r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "feature"}}
					err := NewStageExecutor(b, nil)(context.Background(), r, defaultsStage(mode, jb), newPushReport())
					if (err != nil) != (tc.code != 0) {
						t.Fatalf("execution err = %v; cache must remain best-effort", err)
					}
					wantCalls := 0
					if cacheable {
						wantCalls = 1
					}
					if cache.restore != wantCalls || cache.save != wantCalls*(1-tc.code) {
						t.Fatalf("cache calls restore=%d save=%d", cache.restore, cache.save)
					}
					if cacheable && (!reflect.DeepEqual(cache.paths, tc.want) || cache.key != buildcache.DeriveKey("feature", "", cache.workspace)) {
						t.Fatalf("cache effective paths/key = %v/%s", cache.paths, cache.key)
					}
				})
			}
		}
	}
}

func TestEffectiveDeployTargetsAtBoundary(t *testing.T) {
	settings := &pipeline.Settings{Environments: []pipeline.Environment{
		{Name: "first", TargetServerIDs: []string{"first-only"}},
		{Name: "prod", TargetServerIDs: []string{"", "prod-a", "prod-b", "prod-a", " ", " prod-b "}},
		{Name: "empty", TargetServerIDs: []string{"", " "}},
	}}
	for _, mode := range []string{"grouped", "dag"} {
		for _, kind := range []string{"deploy_ssh", "deploy_frontend"} {
			for _, tc := range []struct {
				name, env, node string
				want            []string
			}{
				{name: "selected", env: "prod", want: []string{"prod-a", "prod-b"}},
				{name: "trimmed_environment", env: " prod ", want: []string{"prod-a", "prod-b"}},
				{name: "first_explicitly_selected", env: "first", want: []string{"first-only"}},
				{name: "node_wins", env: "prod", node: " explicit ", want: []string{"explicit"}},
				{name: "node_with_unknown", env: "unknown", node: "explicit", want: []string{"explicit"}},
				{name: "unknown", env: "unknown"},
				{name: "no_environment"},
				{name: "no_targets", env: "empty"},
			} {
				t.Run(mode+"/"+kind+"/"+tc.name, func(t *testing.T) {
					dep := &stubStageDeployer{}
					b := newDAGTestBuilder(nil, nil)
					b.deployer = dep
					b.settings = fakeSettings{settings: settings}
					if strings.TrimSpace(tc.node) != "" {
						b.projects, b.settings = nil, nil
					}
					jb := pipeline.Job{Name: "deploy", Type: kind, Config: map[string]any{
						"serverId": tc.node, "deployPath": "/srv/{{ROOT}}", "runArgs": "-e RUNTIME=unchanged",
					}}
					r := &run.Run{ID: "run", ProjectID: "p1", Trigger: run.Trigger{ResolvedEnvironment: tc.env, Params: map[string]string{"ROOT": "app"}}}
					err := NewStageExecutor(b, nil)(context.Background(), r, defaultsStage(mode, jb), newPushReport())
					if (err != nil) != (len(tc.want) == 0) || !reflect.DeepEqual(dep.gotServers, tc.want) {
						t.Fatalf("deploy received %v, want %v; err=%v", dep.gotServers, tc.want, err)
					}
					if len(tc.want) > 0 && !reflect.DeepEqual(dep.gotCfg, map[string]string{"releaseBase": "/srv/app", "runArgs": "-e RUNTIME=unchanged"}) {
						t.Fatalf("deployment cfg changed runtime injection: %v", dep.gotCfg)
					}
				})
			}
		}
	}
}

func TestDeployOnlyStageFailsWhenSettingsUnavailable(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		t.Run(mode, func(t *testing.T) {
			dep := &stubStageDeployer{}
			b := newDAGTestBuilder(nil, nil)
			b.deployer = dep
			b.settings = fakeSettings{err: errors.New("unavailable")}
			jb := pipeline.Job{Name: "deploy", Type: "deploy_ssh"}
			err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1"}, defaultsStage(mode, jb), newPushReport())
			if !errors.Is(err, ErrBuildFailed) || dep.gotServers != nil {
				t.Fatalf("unreadable settings deployed: %v, %v", err, dep.gotServers)
			}
		})
	}
}
