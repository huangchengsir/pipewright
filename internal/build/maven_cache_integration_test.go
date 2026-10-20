package build

import (
	"context"
	"encoding/xml"
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

func TestMavenCacheFixtureJavaVersions(t *testing.T) {
	for _, java8 := range []bool{false, true} {
		t.Run(fmt.Sprintf("java8=%t", java8), func(t *testing.T) {
			workspace := t.TempDir()
			cloner := &mavenFixtureCloner{java8: java8}
			if _, err := cloner.Clone(context.Background(), "", "", "", "", "", workspace); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(workspace, "backend", "pom.xml"))
			if err != nil {
				t.Fatal(err)
			}
			var pom struct {
				Properties struct {
					Release string `xml:"maven.compiler.release"`
					Source  string `xml:"maven.compiler.source"`
					Target  string `xml:"maven.compiler.target"`
				} `xml:"properties"`
				BootVersion string `xml:"dependencies>dependency>version"`
			}
			if err := xml.Unmarshal(data, &pom); err != nil {
				t.Fatal(err)
			}
			if java8 {
				if pom.Properties.Release != "" || pom.Properties.Source != "8" || pom.Properties.Target != "8" || pom.BootVersion != "2.7.18" {
					t.Fatalf("invalid JDK 8 fixture: %+v", pom)
				}
			} else if pom.Properties.Release != "17" || pom.BootVersion != "3.3.5" {
				t.Fatalf("default fixture changed: %+v", pom)
			}
		})
	}
}

// Opt-in: this downloads public Maven dependencies and runs disposable containers.
// Run with -run '^TestRealMavenCacheOfflineReuse$' -timeout=45m for both execution modes.
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
	java8 := os.Getenv("PIPEWRIGHT_TEST_MAVEN_JAVA") == "8"
	for _, mode := range []string{"grouped_explicit", "dag_inherited"} {
		t.Run(mode, func(t *testing.T) {
			cacheRoot := t.TempDir()
			store, err := buildcache.New(cacheRoot)
			if err != nil {
				t.Fatal(err)
			}
			cache := &realMavenCache{store: store}
			cloner := &mavenFixtureCloner{java8: java8}
			commands := &cacheAcceptanceCommander{Commander: execCommander{}}
			driver := &realMavenNetworkDriver{shellDriver: &shellDriver{bin: "docker", cmdr: commands, copyWorkspace: true,
				workspaceReconcileAfter: time.Now().Add(24 * time.Hour)}}
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
					driver.offline = iteration == 1
					firstCall := len(commands.creates)
					versionCheck := ""
					if java8 {
						versionCheck = "java -version 2>&1 | grep 'version \"1.8.'\n"
					}
					job := scriptJobWithConfig("Maven real cache", image,
						"java -version\nmvn -version\n"+versionCheck+
							"test ! -e target\n"+
							"mvn "+flags+"-B -DskipTests package\n"+
							"test -s target/maven-cache-acceptance-1.0.0.jar\n"+
							"jar tf target/maven-cache-acceptance-1.0.0.jar | grep 'local/pipewright/CacheApplication.class'\n"+
							"echo MAVEN_CACHE_ARTIFACT_OK", map[string]any{
							"workDir": "backend", "memory": "512m", "cpu": "1",
							"timeoutSeconds": 600, "artifactPath": "backend/target/maven-cache-acceptance-1.0.0.jar",
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
					ctx, cancel := context.WithTimeout(context.Background(), 630*time.Second)
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
					if iteration == 1 && !strings.Contains(logs, "--network none") {
						t.Fatal("offline warm build did not disable the toolchain network")
					}
					wantCalls := 1
					if mode == "dag_inherited" {
						wantCalls = 2
					}
					if err := validateCacheAcceptanceCreates(commands.creates[firstCall:], driver.offline, wantCalls); err != nil {
						t.Fatal(err)
					}
					for _, line := range report.logs {
						if strings.HasPrefix(line, "maven: openjdk version ") || strings.HasPrefix(line, "maven: Java version:") || strings.HasPrefix(line, "maven: Apache Maven ") {
							t.Logf("runtime: %s", line)
						}
					}
					t.Logf("copy-workspace %s: Maven success, java8=%t, networkNone=%t, cacheHit=%t, dependencyFiles=%d, dependencyBytes=%d, jarBytes=%d",
						name, java8, driver.offline, cache.hits[iteration], cache.files, cache.bytes, report.arts[0].SizeBytes)
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

type realMavenNetworkDriver struct {
	*shellDriver
	offline bool
}

func (d *realMavenNetworkDriver) RunToolchain(ctx context.Context, image, hostDir, workdir string, env, cmd []string, res pipeline.Resource, onLine func(string, string)) (int, error) {
	if d.offline {
		res.Network = "none"
	}
	return d.shellDriver.RunToolchain(ctx, image, hostDir, workdir, env, cmd, res, onLine)
}

type mavenFixtureCloner struct {
	workspaces []string
	java8      bool
}

func (c *mavenFixtureCloner) Clone(_ context.Context, _, _, _, _, _, ws string) (*CloneResolved, error) {
	c.workspaces = append(c.workspaces, ws)
	compilerProperties := "<maven.compiler.release>17</maven.compiler.release>"
	bootVersion := "3.3.5"
	if c.java8 {
		compilerProperties = "<maven.compiler.source>8</maven.compiler.source><maven.compiler.target>8</maven.compiler.target>"
		bootVersion = "2.7.18"
	}
	files := map[string]string{
		"backend/pom.xml": fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<groupId>local.pipewright</groupId><artifactId>maven-cache-acceptance</artifactId><version>1.0.0</version>
<properties>%s<project.build.sourceEncoding>UTF-8</project.build.sourceEncoding></properties>
<dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter</artifactId><version>%s</version></dependency></dependencies>
<build><plugins>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-resources-plugin</artifactId><version>3.3.1</version></plugin>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-compiler-plugin</artifactId><version>3.13.0</version></plugin>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-surefire-plugin</artifactId><version>3.2.5</version></plugin>
<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-jar-plugin</artifactId><version>3.4.2</version></plugin>
</plugins></build></project>`, compilerProperties, bootVersion),
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
