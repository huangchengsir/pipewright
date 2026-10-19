package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/buildcache"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// Opt-in: this downloads public Maven dependencies and runs disposable containers.
func TestRealMavenCacheOfflineReuse(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_TEST_MAVEN_CACHE") != "1" {
		t.Skip("set PIPEWRIGHT_TEST_MAVEN_CACHE=1 on an authorized Docker test machine")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("PIPEWRIGHT_TEST_MAVEN_IMAGE")
	if image == "" {
		image = "maven:3.9-eclipse-temurin-21"
	}
	for _, mode := range []string{"grouped_explicit", "dag_inherited"} {
		t.Run(mode, func(t *testing.T) {
			cacheRoot := t.TempDir()
			store, err := buildcache.New(cacheRoot)
			if err != nil {
				t.Fatal(err)
			}
			cache := &realMavenCache{store: store}
			cloner := &mavenFixtureCloner{}
			driver := &shellDriver{bin: "docker", cmdr: execCommander{}, copyWorkspace: true,
				workspaceReconcileAfter: time.Now().Add(24 * time.Hour)}
			b := newDAGTestBuilder(driver, cloner)
			b.buildCache = cache
			settings := &pipeline.Settings{Build: pipeline.BuildConfig{Vars: []pipeline.BuildVar{
				{Key: "MAVEN_OPTS", Value: "-Xmx192m -Djava.awt.headless=true"},
			}}}
			if mode == "dag_inherited" {
				settings.Build.Cache = pipeline.Cache{Enabled: true, Paths: []string{".m2/repository"}}
			}
			b.settings = fakeSettings{settings: settings}
			for iteration := 0; iteration < 2; iteration++ {
				name := "cold"
				flags := ""
				if iteration == 1 {
					name, flags = "offline_warm", "-o "
				}
				t.Run(name, func(t *testing.T) {
					job := scriptJobWithConfig("Maven real cache", image,
						"mvn "+flags+"-B -DskipTests package\n"+
							"test -s target/maven-cache-acceptance-1.0.0.jar\n"+
							"echo MAVEN_CACHE_ARTIFACT_OK", map[string]any{
							"workDir": "backend", "memory": "512m", "cpu": "1",
							"timeoutSeconds": 360, "artifactPath": "backend/target/maven-cache-acceptance-1.0.0.jar",
						})
					job.ID, job.Type = "maven", "build_backend"
					if mode == "grouped_explicit" {
						job.Config["cachePaths"] = ".m2/repository"
					}
					stage := scriptStage(job)
					if mode == "dag_inherited" {
						after := scriptJobWithConfig("After Maven", image, "true", map[string]any{
							"cachePaths": "", "memory": "128m", "cpu": "0.5", "timeoutSeconds": 30,
						})
						after.ID, after.Needs = "after", []string{"maven"}
						stage.Jobs = append(stage.Jobs, after)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 390*time.Second)
					defer cancel()
					report := newPushReport()
					err := NewStageExecutor(b, nil)(ctx, &run.Run{ID: name,
						ProjectID: "p1", Trigger: run.Trigger{Branch: "cache196"}}, stage, report)
					logs := strings.Join(report.logs, "\n")
					if err != nil {
						lines := strings.Split(logs, "\n")
						if len(lines) > 60 {
							lines = lines[len(lines)-60:]
						}
						t.Fatalf("real Maven execution: %v\n%s", err, strings.Join(lines, "\n"))
					}
					if !strings.Contains(logs, "BUILD SUCCESS") || !strings.Contains(logs, "MAVEN_CACHE_ARTIFACT_OK") {
						t.Fatal("real Maven success or artifact evidence missing")
					}
					if len(report.arts) != 1 || report.arts[0].SizeBytes <= 0 {
						t.Fatalf("expected one real nonempty JAR, got %+v", report.arts)
					}
					if len(cache.hits) != iteration+1 || cache.hits[iteration] != (iteration == 1) {
						t.Fatalf("real cache miss/hit sequence: %v", cache.hits)
					}
					if cache.files == 0 || cache.bytes == 0 {
						t.Fatal("Maven dependency bytes did not return to the controller")
					}
					if iteration == 1 && (strings.Contains(logs, "Downloading from") || strings.Contains(logs, "Downloaded from")) {
						t.Fatal("offline warm build unexpectedly downloaded dependencies")
					}
					t.Logf("copy-workspace %s: Maven success, cacheHit=%t, dependencyFiles=%d, dependencyBytes=%d, jarBytes=%d",
						name, cache.hits[iteration], cache.files, cache.bytes, report.arts[0].SizeBytes)
				})
				if t.Failed() {
					return
				}
			}
			seen := make(map[string]bool)
			for _, ws := range cloner.workspaces {
				if seen[ws] {
					t.Fatal("fixture reused a workspace instead of a clean clone")
				}
				seen[ws] = true
			}
		})
	}
}

type mavenFixtureCloner struct{ workspaces []string }

func (c *mavenFixtureCloner) Clone(_ context.Context, _, _, _, _, _, ws string) (*CloneResolved, error) {
	c.workspaces = append(c.workspaces, ws)
	files := map[string]string{
		"backend/pom.xml": `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<groupId>local.pipewright</groupId><artifactId>maven-cache-acceptance</artifactId><version>1.0.0</version>
<properties><maven.compiler.release>17</maven.compiler.release><project.build.sourceEncoding>UTF-8</project.build.sourceEncoding></properties>
<dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter</artifactId><version>3.3.5</version></dependency></dependencies>
<build><plugins>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-resources-plugin</artifactId><version>3.3.1</version></plugin>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-compiler-plugin</artifactId><version>3.13.0</version></plugin>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-surefire-plugin</artifactId><version>3.2.5</version></plugin>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-jar-plugin</artifactId><version>3.4.2</version></plugin>
</plugins></build></project>`,
		"backend/src/main/java/local/pipewright/CacheApplication.java": `package local.pipewright;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
@SpringBootApplication
public class CacheApplication { public static void main(String[] args) { SpringApplication.run(CacheApplication.class, args); } }
`,
	}
	for name, content := range files {
		full := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	return &CloneResolved{CommitShort: "cache196"}, nil
}

type realMavenCache struct {
	store *buildcache.Store
	hits  []bool
	files int
	bytes int64
}

func (c *realMavenCache) Restore(ctx context.Context, key string, paths []string, ws string) (bool, error) {
	hit, err := c.store.Restore(ctx, key, paths, ws)
	c.hits = append(c.hits, hit)
	return hit, err
}

func (c *realMavenCache) Save(ctx context.Context, key string, paths []string, ws string) error {
	root := filepath.Join(ws, ".m2", "repository")
	c.files, c.bytes = 0, 0
	if err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			c.files++
			c.bytes += info.Size()
		}
		return nil
	}); err != nil {
		return fmt.Errorf("real Maven cache absent from controller workspace: %w", err)
	}
	if c.files == 0 || c.bytes == 0 {
		return fmt.Errorf("real Maven cache contains no dependency payload")
	}
	return c.store.Save(ctx, key, paths, ws)
}
