package build

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/buildcache"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

func TestMavenArtifactCacheIgnoresInvalidExclusions(t *testing.T) {
	ws := t.TempDir()
	writeMavenArtifactFixture(t, ws, "target/app.jar", []byte("application"))
	for _, invalid := range []string{"", ".", "..", "../outside", filepath.Join(string(filepath.Separator), "outside")} {
		if got := findBuildJAR(ws, []string{invalid}); got != filepath.Join(ws, "target", "app.jar") {
			t.Fatalf("invalid cache path %q hid the application artifact: %q", invalid, got)
		}
	}
}

func TestMavenArtifactCacheRetainsCustomOutputsAndDisabledPaths(t *testing.T) {
	for _, output := range []string{"out/app.jar", "custom-output/app.jar", "lib/app.jar", "target/app.jar"} {
		t.Run(output, func(t *testing.T) {
			b, artStore := newStoreBuilder(t)
			b.buildCache = &fakeCache{}
			ws := t.TempDir()
			app := []byte("custom application bytes")
			writeMavenArtifactFixture(t, ws, output, app)
			writeMavenArtifactFixture(t, ws, ".m2/repository/dep.jar", []byte("dependency"))
			b.driver = &mavenArtifactFixtureDriver{onRun: func(string, string, []string, []string) {}}
			_, art, err := b.build(context.Background(), newFakeSink(), 0,
				&project.Project{Name: "app"}, pipeline.BuildConfig{
					ArtifactType: pipeline.ArtifactJAR, Toolchain: pipeline.Toolchain{Language: "maven"},
					Cache: pipeline.Cache{Enabled: false, Paths: []string{filepath.ToSlash(filepath.Dir(output))}},
				}, ws, "test")
			if err != nil || art == nil || art.Metadata["stored"] != true {
				t.Fatalf("custom/disabled output lost: art=%+v err=%v", art, err)
			}
			rc, err := artStore.Open(art.Reference)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil || !bytes.Equal(got, app) {
				t.Fatalf("wrong custom artifact: %q %v", got, err)
			}
			if gotPath := findBuildJAR(ws, []string{".m2/repository"}); gotPath != filepath.Join(ws, filepath.FromSlash(output)) {
				t.Fatalf("enabled Maven cache hid custom output: %q", gotPath)
			}
		})
	}
}

type mavenArtifactFixtureDriver struct {
	Driver
	onRun      func(workspace, workdir string, env, cmd []string)
	workspaces []string
}

func (*mavenArtifactFixtureDriver) Binary() string { return "fixture" }

func (d *mavenArtifactFixtureDriver) RunToolchain(_ context.Context, _, workspace, workdir string, env, cmd []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	d.workspaces = append(d.workspaces, workspace)
	d.onRun(workspace, workdir, env, cmd)
	return 0, nil
}

func writeMavenArtifactFixture(t *testing.T, workspace, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMavenArtifactCacheSelectsApplicationBytes(t *testing.T) {
	for _, mode := range []string{"legacy", "grouped", "dag"} {
		for _, tc := range []struct{ name, repo, output string }{
			{"root_target", ".m2/repository", "target/app.jar"},
			{"nested_target", ".m2/repository", "backend/modules/service/target/app.jar"},
			{"nested_target_custom_repo", "deps/maven", "backend/modules/service/target/app.jar"},
			{"gradle_custom_repo", "deps/maven", "build/libs/app.jar"},
			{"nested_gradle_custom_repo", "deps/maven", "backend/build/libs/app.jar"},
			{"missing_output", ".m2/repository", ""},
			{"missing_output_custom_repo", "deps/maven", ""},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				cache, err := buildcache.New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				artStore, err := artifactstore.New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				dependency := []byte("PK\x03\x04 dependency bytes, not deployable")
				application := []byte("PK\x03\x04 application bytes\x00\xff")
				calls := 0
				d := &mavenArtifactFixtureDriver{}
				d.onRun = func(ws, _ string, env, cmd []string) {
					depPath := tc.repo + "/org/example/dependency/1/dependency.jar"
					got, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(depPath)))
					if calls == 0 {
						if !os.IsNotExist(err) {
							t.Fatalf("first workspace was not cold: %v", err)
						}
					} else if err != nil || !bytes.Equal(got, dependency) {
						t.Fatalf("dependency was not restored: %q, %v", got, err)
					}
					calls++
					if tc.repo == "deps/maven" {
						if !reflect.DeepEqual(cmd, toolchainBuildCmd(pipeline.ArtifactJAR)) || envMap(t, env)["MAVEN_OPTS"] != "-Xmx256m -Dmaven.repo.local=/src/deps/maven" {
							t.Fatalf("explicit custom Maven options changed: env=%v cmd=%v", env, cmd)
						}
					}
					for _, rel := range []string{
						depPath, tc.repo + "/org/example/target/dependency.jar",
						"target/a-sources.jar", "target/a-javadoc.jar", "target/a-tests.jar",
						"target/a-test.jar", "target/a-test-fixtures.jar", "target/original-app.jar",
					} {
						writeMavenArtifactFixture(t, ws, rel, dependency)
					}
					if tc.output != "" {
						for _, rel := range []string{"target/dependency/dependency.jar", "build/dependencies/dependency.jar", "src/test/resources/target/sample.jar", "lib/dependency.jar"} {
							writeMavenArtifactFixture(t, ws, rel, dependency)
						}
						for _, name := range []string{"a-sources.jar", "a-javadoc.jar", "a-tests.jar", "a-test-fixtures.jar", "original-app.jar"} {
							writeMavenArtifactFixture(t, ws, filepath.ToSlash(filepath.Join(filepath.Dir(tc.output), name)), dependency)
						}
						writeMavenArtifactFixture(t, ws, tc.output, application)
					}
				}
				b := newDAGTestBuilder(d, &markerCloner{file: "pom.xml", content: "fixed dependencies"})
				b.buildCache, b.artStore, b.disableImageGC = cache, artStore, true
				opts := "-Xmx256m"
				if tc.repo == "deps/maven" {
					opts += " -Dmaven.repo.local=/src/deps/maven"
				}
				b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{
					ArtifactType: pipeline.ArtifactJAR, Toolchain: pipeline.Toolchain{Language: "maven"},
					Cache: pipeline.Cache{Enabled: true, Paths: []string{tc.repo}},
					Vars:  []pipeline.BuildVar{{Key: "MAVEN_OPTS", Value: opts}},
				}}}
				for i := 0; i < 2; i++ {
					r := &run.Run{ID: "run", ProjectID: "p1", Trigger: run.Trigger{Branch: "feature"}}
					var arts []run.Artifact
					if mode == "legacy" {
						sink := newFakeSink()
						if err := b.Run(context.Background(), r, sink); err != nil {
							t.Fatal(err)
						}
						arts = sink.artifacts
					} else {
						jb := pipeline.Job{Name: "build", Type: "build_image", Config: map[string]any{
							"artifactType": pipeline.ArtifactJAR, "toolchainLanguage": "maven",
						}}
						rep := newPushReport()
						if err := NewStageExecutor(b, nil)(context.Background(), r, defaultsStage(mode, jb), rep); err != nil {
							t.Fatal(err)
						}
						arts = rep.arts
					}
					if len(arts) != 1 {
						t.Fatalf("artifacts = %v", arts)
					}
					art := arts[0]
					if tc.output == "" {
						if art.Metadata["located"] != false || art.Metadata["stored"] == true || art.SizeBytes != 0 || artStore.Has(art.Reference) {
							t.Fatalf("missing build output deployed a dependency: %+v", art)
						}
						continue
					}
					if art.Metadata["stored"] != true || art.Metadata["filename"] != "app.jar" || art.SizeBytes != int64(len(application)) {
						t.Fatalf("wrong application artifact: %+v", art)
					}
					rc, err := artStore.Open(art.Reference)
					if err != nil {
						t.Fatal(err)
					}
					got, err := io.ReadAll(rc)
					_ = rc.Close()
					if err != nil || !bytes.Equal(got, application) {
						t.Fatalf("stored dependency instead of application: %q, %v", got, err)
					}
				}
				if len(d.workspaces) != 2 || d.workspaces[0] == d.workspaces[1] {
					t.Fatal("expected two clean workspaces")
				}
			})
		}
	}
}

func TestMavenArtifactCachePrefersRootBuildOutput(t *testing.T) {
	b, artStore := newStoreBuilder(t)
	ws := t.TempDir()
	for _, rel := range []string{"legacy.jar", "a/target/nested.jar", "build/libs/gradle.jar"} {
		writeMavenArtifactFixture(t, ws, rel, []byte("not the primary output"))
	}
	application := []byte("root Maven output")
	writeMavenArtifactFixture(t, ws, "target/app.jar", application)
	art := b.locateFileArtifact(pipeline.ArtifactJAR, "app", ws, func(string, string) {})
	if art.Metadata["stored"] != true || art.Metadata["filename"] != "app.jar" {
		t.Fatalf("root target output lost precedence: %+v", art)
	}
	rc, err := artStore.Open(art.Reference)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, application) {
		t.Fatalf("wrong root output bytes: %q, %v", got, err)
	}
}

func TestLegacyMavenArtifactCacheCustomScriptPath(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			cache, err := buildcache.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			d := &mavenArtifactFixtureDriver{}
			d.onRun = func(ws, workdir string, env, cmd []string) {
				if workdir == "/src" {
					if !reflect.DeepEqual(cmd, toolchainBuildCmd(pipeline.ArtifactJAR)) {
						t.Fatalf("legacy JAR build command changed: %v", cmd)
					}
					return
				}
				if workdir != "/workspace/backend" || envMap(t, env)["MAVEN_OPTS"] != "-Xmx256m -Dmaven.repo.local=/workspace/deps/maven" || !reflect.DeepEqual(cmd, []string{"sh", "-c", "set -e\nmvn package"}) {
					t.Fatalf("custom script invocation changed: workdir=%s env=%v cmd=%v", workdir, env, cmd)
				}
				path := "deps/maven/org/example/dependency.jar"
				got, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(path)))
				if calls == 1 && enabled {
					if err != nil || string(got) != "script dependency bytes" {
						t.Fatalf("custom script cache was not restored: %q, %v", got, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("expected cold script workspace: %q, %v", got, err)
				}
				calls++
				writeMavenArtifactFixture(t, ws, path, []byte("script dependency bytes"))
			}
			b := newDAGTestBuilder(d, &markerCloner{file: "pom.xml", content: "fixed dependencies"})
			b.buildCache, b.disableImageGC = cache, true
			b.settings = fakeSettings{settings: &pipeline.Settings{
				Build: pipeline.BuildConfig{
					ArtifactType: pipeline.ArtifactJAR, Toolchain: pipeline.Toolchain{Language: "maven"},
					Cache: pipeline.Cache{Enabled: enabled, Paths: []string{"deps/maven"}},
				},
				Steps: []pipeline.PipelineStep{{
					Name: "maven", Type: pipeline.StepTypeScript, Image: "maven:3.9", WorkDir: "backend",
					Commands: []string{"mvn package"},
					Env:      []pipeline.BuildVar{{Key: "MAVEN_OPTS", Value: "-Xmx256m -Dmaven.repo.local=/workspace/deps/maven"}},
				}},
			}}
			for i := 0; i < 2; i++ {
				if err := b.Run(context.Background(), &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "feature"}}, newFakeSink()); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 || len(d.workspaces) != 4 || d.workspaces[0] == d.workspaces[2] {
				t.Fatal("expected two scripts in fresh workspaces")
			}
		})
	}
}

func TestLegacyMavenArtifactCacheBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, artifact string
		enabled        bool
		script         bool
	}{
		{"disabled_jar", pipeline.ArtifactJAR, false, false},
		{"dockerfile", pipeline.ArtifactImage, true, false},
		{"unrelated_node_script", pipeline.ArtifactDist, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &fakeCache{}
			b := newDAGTestBuilder(&defaultsDriver{}, &markerCloner{})
			b.buildCache, b.disableImageGC = cache, true
			settings := &pipeline.Settings{Build: pipeline.BuildConfig{
				Model: pipeline.BuildModelDockerfile, ArtifactType: tc.artifact,
				Toolchain: pipeline.Toolchain{Language: "maven"},
				Cache:     pipeline.Cache{Enabled: tc.enabled, Paths: []string{"deps/maven"}},
			}}
			if tc.script {
				settings.Build.Toolchain.Language = "node"
				settings.Build.Cache.Paths = []string{".npm"}
				settings.Steps = []pipeline.PipelineStep{{
					Name: "npm", Type: pipeline.StepTypeScript, Image: "node:20", Commands: []string{"npm ci"},
				}}
			}
			b.settings = fakeSettings{settings: settings}
			if err := b.Run(context.Background(), &run.Run{ProjectID: "p1"}, newFakeSink()); err != nil {
				t.Fatal(err)
			}
			if len(cache.restoreKeys) != 0 || len(cache.saveKeys) != 0 {
				t.Fatalf("unexpected cache persistence: restore=%v save=%v", cache.restoreKeys, cache.saveKeys)
			}
		})
	}
}
