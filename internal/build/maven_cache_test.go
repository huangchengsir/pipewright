package build

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/buildcache"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

func TestMavenCacheExecutionMatrix(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		for _, kind := range []string{"script", pipeline.ArtifactJAR} {
			for _, tc := range []struct {
				name          string
				global        pipeline.Cache
				node          map[string]any
				noStore, want bool
			}{
				{name: "inherit", global: pipeline.Cache{Enabled: true, Paths: []string{".m2/repository"}}, want: true},
				{name: "parent", node: map[string]any{"cachePaths": ".m2"}, want: true},
				{name: "normalized", node: map[string]any{"cachePaths": " ./.m2//repository/ "}, want: true},
				{name: "template", node: map[string]any{"cachePaths": "{{repo}}", "params": "repo=.m2/repository"}, want: true},
				{name: "node_override", global: pipeline.Cache{Enabled: true, Paths: []string{"node_modules"}}, node: map[string]any{"cachePaths": ".m2"}, want: true},
				{name: "no_config"},
				{name: "disabled", global: pipeline.Cache{Paths: []string{".m2"}}},
				{name: "empty_defaults", global: pipeline.Cache{Enabled: true}},
				{name: "empty_override", global: pipeline.Cache{Enabled: true, Paths: []string{".m2"}}, node: map[string]any{"cachePaths": ""}},
				{name: "nil_override", global: pipeline.Cache{Enabled: true, Paths: []string{".m2"}}, node: map[string]any{"cachePaths": nil}},
				{name: "non_maven", node: map[string]any{"cachePaths": ".npm\nnode_modules"}},
				{name: "absolute", node: map[string]any{"cachePaths": "/.m2/repository"}},
				{name: "escape", node: map[string]any{"cachePaths": "../.m2/repository"}},
				{name: "literal_shell_path", node: map[string]any{"cachePaths": ".m2/$(touch injected)"}},
				{name: "global_switch_off", global: pipeline.Cache{Enabled: true, Paths: []string{".m2"}}, noStore: true},
			} {
				t.Run(mode+"/"+kind+"/"+tc.name, func(t *testing.T) {
					d := &recordingDriver{}
					b := newDAGTestBuilder(d, &markerCloner{})
					if !tc.noStore {
						b.buildCache = &fakeCache{}
					}
					b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{Cache: tc.global,
						Vars: []pipeline.BuildVar{{Key: "MAVEN_OPTS", Value: "-Xmx256m"}, {Key: "MAVEN_ARGS", Value: "-B"}},
					}}}
					jb := scriptJobWithConfig("build", "maven:3.9", "mvn package", tc.node)
					jb.Config["workDir"] = "backend"
					root := "/workspace"
					original := []string{"sh", "-c", "set -e\nmvn package"}
					if kind == pipeline.ArtifactJAR {
						jb.Type = "build_image"
						jb.Config["artifactType"], jb.Config["toolchainLanguage"] = kind, "maven"
						root, original = "/src", toolchainBuildCmd(kind)
					}
					if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1"}, defaultsStage(mode, jb), newPushReport()); err != nil {
						t.Fatal(err)
					}
					if tc.want {
						if len(d.gotCmd) < 5 || d.gotCmd[3] != "pipewright-maven-cache" || !strings.Contains(d.gotCmd[2], root+"/.m2/repository") || !reflect.DeepEqual(d.gotCmd[4:], original) {
							t.Fatalf("Maven did not receive root-relative default and original command: %v", d.gotCmd)
						}
					} else if !reflect.DeepEqual(d.gotCmd, original) {
						t.Fatalf("unconfigured command changed: %v", d.gotCmd)
					}
					if kind == "script" && d.gotWork != "/workspace/backend" {
						t.Fatalf("workDir changed: %s", d.gotWork)
					}
					vars := envMap(t, d.gotEnv)
					if vars["MAVEN_OPTS"] != "-Xmx256m" || vars["MAVEN_ARGS"] != "-B" {
						t.Fatal("user environment modified before entering container")
					}
				})
			}
		}
	}
}

func mavenCacheTestShell(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell
	}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			shell := filepath.Join(root, "Git", "usr", "bin", "sh.exe")
			if _, err := os.Stat(shell); err == nil {
				return shell
			}
		}
	}
	t.Skip("POSIX shell unavailable for wrapper unit test")
	return ""
}

// This executes only a printf fixture, not Maven or a host build.
func TestMavenCacheRuntimeDefaults(t *testing.T) {
	shell := mavenCacheTestShell(t)
	for _, root := range []string{"/workspace", "/src"} {
		for _, tc := range []struct{ name, opts, args string }{
			{name: "empty"},
			{name: "image_jvm_options", opts: "-Xmx256m -Dfoo=bar", args: "-B -ntp"},
			{name: "explicit_jvm_repository", opts: "-Xmx256m -Dmaven.repo.local=/custom"},
			{name: "explicit_args_repository", opts: "-Xmx256m", args: "-Dmaven.repo.local=/custom"},
			{name: "literal_environment", opts: "-Dvalue=$(printf should-not-run);`echo literal`"},
		} {
			t.Run(root+"/"+tc.name, func(t *testing.T) {
				const cli = "-Dmaven.repo.local=/explicit cli;$(echo literal)"
				original := []string{shell, "-c", `printf '%s\n' "${MAVEN_OPTS-}" "${MAVEN_ARGS-}" "$1" "$HOME"; exit 17`, "fixture", cli}
				wrapped := mavenCacheCommand(original, root, jobCacheConfig{enabled: true, paths: []string{".m2"}})
				cmd := exec.Command(shell, wrapped[1:]...)
				for _, entry := range os.Environ() {
					key, _, _ := strings.Cut(entry, "=")
					if key != "MAVEN_OPTS" && key != "MAVEN_ARGS" && key != "HOME" {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				cmd.Env = append(cmd.Env, "MAVEN_OPTS="+tc.opts, "MAVEN_ARGS="+tc.args, "HOME=/unchanged-home", "MSYS_NO_PATHCONV=1")
				out, err := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 17 {
					t.Fatalf("wrapper changed exit status: %v %s", err, out)
				}
				wantOpts := tc.opts
				if !strings.Contains(tc.opts+" "+tc.args, "maven.repo.local") {
					wantOpts = "-Dmaven.repo.local=" + root + "/.m2/repository"
					if tc.opts != "" {
						wantOpts += " " + tc.opts
					}
				}
				want := wantOpts + "\n" + tc.args + "\n" + cli + "\n/unchanged-home\n"
				if string(out) != want {
					t.Fatalf("runtime values = %q, want %q", out, want)
				}
			})
		}
	}
}

type mavenPayloadDriver struct {
	Driver
	t          *testing.T
	workspaces []string
}

func (*mavenPayloadDriver) Binary() string { return "fixture" }

func (d *mavenPayloadDriver) RunToolchain(_ context.Context, _, ws, workdir string, _ []string, cmd []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	d.t.Helper()
	root := toolchainWorkspaceMount(workdir)
	if len(cmd) < 5 || !strings.Contains(cmd[2], root+"/.m2/repository") {
		d.t.Fatalf("cache data and Maven execution do not share workspace root: %v", cmd)
	}
	file := filepath.Join(ws, ".m2/repository/org/example/dependency/1/dependency-1.jar")
	data, err := os.ReadFile(file)
	if len(d.workspaces) == 0 {
		if !os.IsNotExist(err) {
			d.t.Fatalf("first workspace was not cold: %v", err)
		}
	} else if err != nil || string(data) != "dependency bytes" {
		d.t.Fatalf("fresh workspace did not restore dependency bytes: %q %v", data, err)
	}
	d.workspaces = append(d.workspaces, ws)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("dependency bytes"), 0o600); err != nil {
		d.t.Fatal(err)
	}
	return 0, nil
}

func TestMavenCacheFreshWorkspacesReuseBytes(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		for _, kind := range []string{"script", pipeline.ArtifactJAR} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				cache, err := buildcache.New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				d := &mavenPayloadDriver{t: t}
				b := newDAGTestBuilder(d, &markerCloner{file: "pom.xml", content: "fixed dependency declaration"})
				b.buildCache = cache
				b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{Cache: pipeline.Cache{Enabled: true, Paths: []string{".m2/repository"}}}}}
				jb := scriptJobWithConfig("maven", "maven:3.9", "mvn package", map[string]any{"workDir": "backend"})
				if kind == pipeline.ArtifactJAR {
					jb.Type = "build_image"
					jb.Config["artifactType"], jb.Config["toolchainLanguage"] = kind, "maven"
				}
				for i := 0; i < 2; i++ {
					rep := newPushReport()
					if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "feature"}}, defaultsStage(mode, jb), rep); err != nil {
						t.Fatal(err)
					}
					warm := strings.Contains(strings.Join(rep.logs, "\n"), "已恢复构建缓存(暖构建)")
					if warm != (i == 1) {
						t.Fatalf("cache log disagrees with disk reuse: %v", rep.logs)
					}
				}
				if len(d.workspaces) != 2 || d.workspaces[0] == d.workspaces[1] {
					t.Fatal("test did not use two clean workspaces")
				}
			})
		}
	}
}

func TestLegacyMavenCacheFreshWorkspacesReuseBytes(t *testing.T) {
	cache, err := buildcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &mavenPayloadDriver{t: t}
	b := newDAGTestBuilder(d, &markerCloner{file: "pom.xml", content: "fixed dependencies"})
	b.buildCache, b.disableImageGC = cache, true
	b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{
		ArtifactType: pipeline.ArtifactJAR, Toolchain: pipeline.Toolchain{Language: "maven"},
		Cache: pipeline.Cache{Enabled: true, Paths: []string{".m2/repository"}},
	}}}
	for i := 0; i < 2; i++ {
		if err := b.Run(context.Background(), &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "feature"}}, newFakeSink()); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.workspaces) != 2 || d.workspaces[0] == d.workspaces[1] {
		t.Fatal("legacy cache test did not use clean workspaces")
	}
}

func TestLegacyNonMavenCacheLeavesExecutionUnchanged(t *testing.T) {
	d := &recordingDriver{}
	cache := &fakeCache{}
	b := newDAGTestBuilder(d, &markerCloner{})
	b.buildCache, b.disableImageGC = cache, true
	b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{
		ArtifactType: pipeline.ArtifactDist, Toolchain: pipeline.Toolchain{Language: "node"},
		Cache: pipeline.Cache{Enabled: true, Paths: []string{".npm"}},
	}}}
	if err := b.Run(context.Background(), &run.Run{ProjectID: "p1"}, newFakeSink()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.gotCmd, toolchainBuildCmd(pipeline.ArtifactDist)) || len(cache.restoreKeys) != 0 || len(cache.saveKeys) != 0 {
		t.Fatal("Maven integration changed legacy npm execution")
	}
}

type cancelMavenDriver struct {
	Driver
	cancel context.CancelFunc
}

func (*cancelMavenDriver) Binary() string { return "fixture" }

func (d *cancelMavenDriver) RunToolchain(context.Context, string, string, string, []string, []string, pipeline.Resource, func(string, string)) (int, error) {
	d.cancel()
	return 0, nil // Cancellation must win even over an apparently successful driver.
}

func TestMavenCacheCanceledExecutionNeverSaves(t *testing.T) {
	for _, mode := range []string{"grouped", "dag"} {
		for _, kind := range []string{"script", pipeline.ArtifactJAR} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cache := &fakeCache{}
				b := newDAGTestBuilder(&cancelMavenDriver{cancel: cancel}, &markerCloner{})
				b.buildCache = cache
				jb := scriptJobWithConfig("build", "maven:3.9", "mvn package", map[string]any{"cachePaths": ".m2"})
				if kind == pipeline.ArtifactJAR {
					jb.Type = "build_image"
					jb.Config["artifactType"], jb.Config["toolchainLanguage"] = kind, "maven"
				}
				err := NewStageExecutor(b, nil)(ctx, &run.Run{ProjectID: "p1"}, defaultsStage(mode, jb), newPushReport())
				if !errors.Is(err, run.ErrCanceled) || len(cache.saveKeys) != 0 {
					t.Fatalf("cancel = %v; saved %v", err, cache.saveKeys)
				}
			})
		}
	}
}

func TestMavenCacheEmptySaveDoesNotClaimContent(t *testing.T) {
	cache, err := buildcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := newDAGTestBuilder(&recordingDriver{}, &markerCloner{})
	b.buildCache = cache
	jb := scriptJobWithConfig("build", "maven:3.9", "true", map[string]any{"cachePaths": ".m2"})
	rep := newPushReport()
	if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1"}, scriptStage(jb), rep); err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(rep.logs, "\n")
	if strings.Contains(logs, "已保存构建缓存") || strings.Contains(logs, "暖构建") || !strings.Contains(logs, "无有效文件内容") {
		t.Fatalf("empty cache claimed successful content: %s", logs)
	}
}

func TestMavenCacheWrapperRetainsSecretMasking(t *testing.T) {
	cmdr := newFakeCommander()
	b := newDAGTestBuilder(&shellDriver{bin: "docker", cmdr: cmdr}, &markerCloner{})
	b.buildCache = &fakeCache{}
	b.vault = fakeVault{secrets: map[string]string{"opts": "-Xmx256m -Dpassword=private-value"}}
	b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{Vars: []pipeline.BuildVar{{Key: "MAVEN_OPTS", Secret: true, CredentialID: "opts"}}}}}
	jb := scriptJobWithConfig("build", "maven:3.9", "mvn package", map[string]any{"cachePaths": ".m2"})
	rep := newPushReport()
	if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1"}, scriptStage(jb), rep); err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(rep.logs, "\n")
	if strings.Contains(logs, "private-value") || !strings.Contains(logs, "MAVEN_OPTS=***") || !strings.Contains(cmdr.allArgsText(), "-Dpassword=private-value") {
		t.Fatal("Maven wrapper lost environment injection or command masking")
	}
}
