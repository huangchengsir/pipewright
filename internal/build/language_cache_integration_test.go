package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/buildcache"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// Opt-in only: public fixtures, public package downloads, and disposable Docker containers.
// Run with -run '^TestRealLanguageCacheOfflineReuse$' -timeout=90m for the full matrix.
func TestRealLanguageCacheOfflineReuse(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_TEST_LANGUAGE_CACHE") != "1" {
		t.Skip("set PIPEWRIGHT_TEST_LANGUAGE_CACHE=1 on an authorized Docker test machine")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
	nodeImage := os.Getenv("PIPEWRIGHT_TEST_NODE_IMAGE")
	if nodeImage == "" {
		nodeImage = "node:22-alpine"
	}
	goImage := os.Getenv("PIPEWRIGHT_TEST_GO_IMAGE")
	if goImage == "" {
		goImage = "golang:1.26-alpine"
	}
	for _, fixture := range []realLanguageFixture{
		realNodeCacheFixture("npm", nodeImage), realNodeCacheFixture("pnpm", nodeImage), realGoCacheFixture(goImage),
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, mode := range []string{"grouped_explicit", "dag_inherited"} {
				t.Run(mode, func(t *testing.T) { runRealLanguageCacheReuse(t, fixture, mode) })
			}
		})
	}
}

type realLanguageFixture struct {
	name, image, identity, expectedText string
	paths                               []string
	vars                                []pipeline.BuildVar
	files                               map[string]string
	cold, warm                          string
	binary                              bool
}

func runRealLanguageCacheReuse(t *testing.T, fixture realLanguageFixture, mode string) {
	t.Helper()
	store, err := buildcache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artStore, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := &realLanguageCache{store: store, fixture: fixture}
	cloner := &languageFixtureCloner{fixture: fixture}
	commander := &cacheAcceptanceCommander{Commander: execCommander{}}
	driver := &languageCacheNetworkDriver{shellDriver: &shellDriver{
		bin: "docker", cmdr: commander, copyWorkspace: true,
		workspaceReconcileAfter: time.Now().Add(24 * time.Hour),
	}}
	b := newDAGTestBuilder(driver, cloner)
	b.buildCache, b.artStore = cache, artStore
	settings := &pipeline.Settings{Build: pipeline.BuildConfig{Vars: fixture.vars}}
	if mode == "dag_inherited" {
		settings.Build.Cache = pipeline.Cache{Enabled: true, Paths: fixture.paths}
	}
	b.settings = fakeSettings{settings: settings}
	for iteration, phase := range []string{"cold", "offline_warm"} {
		t.Run(phase, func(t *testing.T) {
			driver.offline = iteration == 1
			firstCall := len(commander.creates)
			commands := fixture.cold
			if driver.offline {
				commands = fixture.warm
			}
			commands = "test ! -e node_modules\ntest ! -e out\nrm -rf node_modules out\n" +
				"printf 'PIPEWRIGHT_LANGUAGE_FIXTURE=%s\\n' '" + fixture.identity + "'\n" + commands +
				"\ntest -s out/result.txt\ncat out/result.txt\n" +
				"echo PIPEWRIGHT_LANGUAGE_ARTIFACT_OK=" + fixture.name
			artifactPaths := "app/out/result.txt"
			if fixture.binary {
				artifactPaths += "\napp/out/cache-probe"
			}
			job := scriptJobWithConfig("Real "+fixture.name+" cache", fixture.image, commands, map[string]any{
				"workDir": "app", "memory": "512m", "cpu": "1", "timeoutSeconds": 360,
				"artifactPath": artifactPaths,
			})
			job.ID = "language"
			if fixture.name == "go" {
				job.Type = "build_backend"
			} else {
				job.Type = "build_frontend"
			}
			if mode == "grouped_explicit" {
				job.Config["cachePaths"] = strings.Join(fixture.paths, "\n")
			}
			stage := scriptStage(job)
			if mode == "dag_inherited" {
				after := scriptJobWithConfig("After language build", fixture.image, "true", map[string]any{
					"cachePaths": "", "memory": "128m", "cpu": "0.5", "timeoutSeconds": 30,
				})
				after.ID, after.Needs = "after", []string{"language"}
				stage.Jobs = append(stage.Jobs, after)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
			defer cancel()
			report := newPushReport()
			err := NewStageExecutor(b, nil)(ctx, &run.Run{ID: fixture.name + "-" + phase,
				ProjectID: "p1", Trigger: run.Trigger{Branch: "language-cache-acceptance"}}, stage, report)
			if err != nil {
				logs := strings.Split(strings.Join(report.logs, "\n"), "\n")
				if len(logs) > 80 {
					logs = logs[len(logs)-80:]
				}
				t.Fatalf("real %s %s: %v; cache errors=%v\n%s", fixture.name, phase, err, cache.errs, strings.Join(logs, "\n"))
			}
			if len(cache.errs) != 0 {
				t.Fatalf("cache failures must not be hidden by best-effort production handling: %v", cache.errs)
			}
			if len(cache.hits) != iteration+1 || cache.hits[iteration] != driver.offline || len(cache.saved) != iteration+1 {
				t.Fatalf("expected cold miss then saved-byte warm hit: hits=%v saves=%d", cache.hits, len(cache.saved))
			}
			if cache.restoreKeys[iteration] != cache.saveKeys[iteration] || (iteration == 1 && cache.restoreKeys[1] != cache.saveKeys[0]) {
				t.Fatalf("fixture changed DeriveKey during install/build: restore=%v save=%v", cache.restoreKeys, cache.saveKeys)
			}
			wantCalls := 1
			if mode == "dag_inherited" {
				wantCalls = 2
			}
			if err := validateCacheAcceptanceCreates(commander.creates[firstCall:], driver.offline, wantCalls); err != nil {
				t.Fatal(err)
			}
			runtimePrefix := "language: PIPEWRIGHT_NODE_RUNTIME=v"
			if fixture.name == "go" {
				runtimePrefix = "language: PIPEWRIGHT_GO_RUNTIME=go version go"
			}
			runtimeSeen := false
			for _, line := range report.logs {
				runtimeSeen = runtimeSeen || strings.HasPrefix(line, runtimePrefix)
			}
			if !runtimeSeen {
				t.Fatal("actual runtime version output missing")
			}
			for _, line := range []string{"PIPEWRIGHT_LANGUAGE_FIXTURE=" + fixture.identity,
				strings.TrimSuffix(fixture.expectedText, "\n"), "PIPEWRIGHT_LANGUAGE_ARTIFACT_OK=" + fixture.name} {
				if !languageCacheReported(report, line) {
					t.Fatalf("actual container output missing %q", line)
				}
			}
			if driver.offline && !languageCacheReported(report, "PIPEWRIGHT_EMPTY_CACHE_OFFLINE_FAILED="+fixture.name) {
				t.Fatal("empty-cache offline negative control did not run")
			}
			if fixture.name == "go" && driver.offline && strings.Contains(strings.Join(report.logs, "\n"), "/compile ") {
				t.Fatal("warm Go build recompiled instead of reusing the restored build cache")
			}
			verifyLanguageArtifacts(t, artStore, report.arts, fixture)
			for _, line := range report.logs {
				if strings.HasPrefix(line, "language: PIPEWRIGHT_") {
					t.Log(line)
				}
			}
			t.Logf("image=%s copy-workspace %s cacheHit=%t saved=%v restored=%v",
				fixture.image, phase, cache.hits[iteration], cache.saved[iteration], cache.restored[iteration])
		})
		if t.Failed() {
			return
		}
	}
	wantClones := 2
	if mode == "dag_inherited" {
		wantClones = 4
	}
	if len(cloner.workspaces) != wantClones {
		t.Fatalf("expected %d fresh fixture clones, got %v", wantClones, cloner.workspaces)
	}
	seen := make(map[string]bool)
	for _, ws := range cloner.workspaces {
		if seen[ws] {
			t.Fatal("fixture reused a workspace instead of a fresh clone")
		}
		seen[ws] = true
	}
}

func languageCacheReported(report *pushTestReport, line string) bool {
	for _, actual := range report.logs {
		if actual == "language: "+line {
			return true
		}
	}
	return false
}

func verifyLanguageArtifacts(t *testing.T, store *artifactstore.Store, artifacts []run.Artifact, fixture realLanguageFixture) {
	t.Helper()
	want := 1
	if fixture.binary {
		want = 2
	}
	if len(artifacts) != want {
		t.Fatalf("expected %d stored artifacts, got %+v", want, artifacts)
	}
	seen := make(map[string]bool)
	for _, art := range artifacts {
		path, _ := art.Metadata["workspacePath"].(string)
		path = filepath.ToSlash(path)
		if art.Metadata["stored"] != true || art.SizeBytes <= 0 || seen[path] {
			t.Fatalf("artifact missing stored bytes or duplicated: %+v", art)
		}
		seen[path] = true
		rc, err := store.Open(art.Reference)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || int64(len(data)) != art.SizeBytes {
			t.Fatalf("read stored artifact %s: bytes=%d err=%v", path, len(data), err)
		}
		switch path {
		case "app/out/result.txt":
			if string(data) != fixture.expectedText {
				t.Fatalf("wrong dependency-derived artifact contents: %q", data)
			}
		case "app/out/cache-probe":
			if !fixture.binary || !bytes.HasPrefix(data, []byte("\x7fELF")) || !bytes.Contains(data, []byte(fixture.identity)) {
				t.Fatal("Go artifact is not the real fixture ELF executable")
			}
		default:
			t.Fatalf("unexpected artifact path %q", path)
		}
	}
}

// The wrapper changes only this acceptance test's warm containers, including the DAG tail.
type languageCacheNetworkDriver struct {
	*shellDriver
	offline bool
}

func (d *languageCacheNetworkDriver) RunToolchain(ctx context.Context, image, hostDir, workdir string, env, cmd []string, res pipeline.Resource, onLine func(string, string)) (int, error) {
	if d.offline {
		res.Network = "none"
	}
	return d.shellDriver.RunToolchain(ctx, image, hostDir, workdir, env, cmd, res, onLine)
}

// Observe the argv actually sent to Docker, not the driver's input resource struct.
type cacheAcceptanceCommander struct {
	Commander
	creates [][]string
}

func (c *cacheAcceptanceCommander) Stream(ctx context.Context, name string, args []string, stdin string, onLine func(string, string)) (int, error) {
	if name == "docker" && len(args) > 0 && args[0] == "create" {
		c.creates = append(c.creates, append([]string(nil), args...))
	}
	return c.Commander.Stream(ctx, name, args, stdin, onLine)
}

func validateCacheAcceptanceCreates(creates [][]string, offline bool, want int) error {
	if len(creates) != want {
		return fmt.Errorf("expected %d actual Docker create calls, got %d", want, len(creates))
	}
	for i, args := range creates {
		memory, cpu := "512m", "1"
		if i > 0 {
			memory, cpu = "128m", "0.5"
		}
		expected := map[string]string{"--memory": memory, "--cpus": cpu}
		if offline {
			expected["--network"] = "none"
		}
		for flag, value := range expected {
			found := false
			for j := 1; j+1 < len(args); j++ {
				found = found || (args[j] == flag && args[j+1] == value)
			}
			if !found {
				return fmt.Errorf("Docker create %d did not receive %s %s", i, flag, value)
			}
		}
	}
	return nil
}

func TestCacheAcceptanceDockerFlags(t *testing.T) {
	main := []string{"create", "--memory", "512m", "--cpus", "1", "--network", "none", "image", "true"}
	tail := []string{"create", "--memory", "128m", "--cpus", "0.5", "--network", "none", "image", "true"}
	if err := validateCacheAcceptanceCreates([][]string{main, tail}, true, 2); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--memory", "--cpus", "--network"} {
		for _, target := range []int{0, 1} {
			changed := [][]string{append([]string(nil), main...), append([]string(nil), tail...)}
			for i, arg := range changed[target] {
				if arg == flag {
					changed[target] = append(changed[target][:i], changed[target][i+2:]...)
					break
				}
			}
			if err := validateCacheAcceptanceCreates(changed, true, 2); err == nil {
				t.Fatalf("missing %s in Docker create %d escaped validation", flag, target)
			}
		}
	}
}

type languageFixtureCloner struct {
	fixture    realLanguageFixture
	workspaces []string
}

func (c *languageFixtureCloner) Clone(_ context.Context, _, _, _, _, _, ws string) (*CloneResolved, error) {
	c.workspaces = append(c.workspaces, ws)
	if err := languageUncachedOutputsAbsent(ws); err != nil {
		return nil, err
	}
	for _, path := range c.fixture.paths {
		if _, err := os.Lstat(filepath.Join(ws, filepath.FromSlash(path))); !os.IsNotExist(err) {
			return nil, fmt.Errorf("fresh clone already contains cache %s: %v", path, err)
		}
	}
	for path, data := range c.fixture.files {
		full := filepath.Join(ws, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			return nil, err
		}
	}
	return &CloneResolved{CommitShort: "langcache"}, nil
}

func languageUncachedOutputsAbsent(ws string) error {
	for _, path := range []string{"app/node_modules", "app/out"} {
		if _, err := os.Lstat(filepath.Join(ws, filepath.FromSlash(path))); !os.IsNotExist(err) {
			return fmt.Errorf("uncached install/output already exists: %s: %v", path, err)
		}
	}
	return nil
}

type languageCachePayload struct {
	Files  int
	Bytes  int64
	SHA256 string
}

type realLanguageCache struct {
	store                 *buildcache.Store
	fixture               realLanguageFixture
	hits                  []bool
	restoreKeys, saveKeys []string
	saved, restored       []map[string]languageCachePayload
	errs                  []error
}

func (c *realLanguageCache) Restore(ctx context.Context, key string, paths []string, ws string) (bool, error) {
	hit, err := c.store.Restore(ctx, key, paths, ws)
	c.hits, c.restoreKeys = append(c.hits, hit), append(c.restoreKeys, key)
	payload := make(map[string]languageCachePayload)
	if err == nil && !reflect.DeepEqual(paths, c.fixture.paths) {
		err = fmt.Errorf("wrong restored cache paths: %v", paths)
	}
	if err == nil {
		err = languageUncachedOutputsAbsent(ws)
	}
	if err == nil && hit {
		payload, err = languageCacheSnapshot(ws, paths)
		if err == nil && (len(c.saved) == 0 || !reflect.DeepEqual(payload, c.saved[len(c.saved)-1])) {
			err = fmt.Errorf("restored dependency bytes differ from saved payload: %v", payload)
		}
	}
	c.restored = append(c.restored, payload)
	if err != nil {
		c.errs = append(c.errs, err)
	}
	return hit, err
}

func (c *realLanguageCache) Save(ctx context.Context, key string, paths []string, ws string) error {
	c.saveKeys = append(c.saveKeys, key)
	err := c.save(ctx, key, paths, ws)
	if err != nil {
		c.errs = append(c.errs, err)
	}
	return err
}

func (c *realLanguageCache) save(ctx context.Context, key string, paths []string, ws string) error {
	if !reflect.DeepEqual(paths, c.fixture.paths) {
		return fmt.Errorf("wrong saved cache paths: %v", paths)
	}
	for path, expected := range c.fixture.files {
		data, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(path)))
		if err != nil || string(data) != expected {
			return fmt.Errorf("install/build changed pinned fixture %s: %v", path, err)
		}
	}
	if err := verifyLanguageDependencyBytes(ws, c.fixture.name); err != nil {
		return err
	}
	payload, err := languageCacheSnapshot(ws, paths)
	if err != nil {
		return err
	}
	if err := c.store.Save(ctx, key, paths, ws); err != nil {
		return err
	}
	if !c.store.Has(key) {
		return fmt.Errorf("real cache archive was not persisted")
	}
	c.saved = append(c.saved, payload)
	return nil
}

func languageCacheSnapshot(ws string, paths []string) (map[string]languageCachePayload, error) {
	result := make(map[string]languageCachePayload)
	for _, path := range paths {
		var payload languageCachePayload
		hash := sha256.New()
		root := filepath.Join(ws, filepath.FromSlash(path))
		err := filepath.WalkDir(root, func(full string, entry os.DirEntry, err error) error {
			if err != nil || !entry.Type().IsRegular() {
				return err
			}
			file, err := os.Open(full)
			if err != nil {
				return err
			}
			fileHash := sha256.New()
			size, err := io.Copy(fileHash, file)
			_ = file.Close()
			if err != nil || size == 0 {
				return err
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			payload.Files++
			payload.Bytes += size
			_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%x\n", filepath.ToSlash(rel), size, fileHash.Sum(nil))
			return nil
		})
		if err != nil || payload.Files == 0 || payload.Bytes == 0 {
			return nil, fmt.Errorf("cache path %s has no real payload: files=%d bytes=%d err=%v", path, payload.Files, payload.Bytes, err)
		}
		payload.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
		result[path] = payload
	}
	return result, nil
}

func verifyLanguageDependencyBytes(ws, language string) error {
	if language == "go" {
		if _, err := languageCacheSnapshot(ws, []string{
			".gomodcache/cache/download/github.com/dustin/go-humanize/@v/v1.0.1.zip",
			".gomodcache/github.com/dustin/go-humanize@v1.0.1",
		}); err != nil {
			return err
		}
		found := false
		err := filepath.WalkDir(filepath.Join(ws, ".gocache"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() && strings.HasSuffix(path, "-d") {
				info, statErr := entry.Info()
				if statErr != nil {
					return statErr
				}
				found = found || info.Size() > 0
			}
			return err
		})
		if err != nil || !found {
			return fmt.Errorf("Go compiled cache payload missing: %v", err)
		}
		return nil
	}
	root := filepath.Join(ws, "app", "node_modules", "is-number")
	manifest, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return err
	}
	var pkg struct{ Name, Version string }
	if err := json.Unmarshal(manifest, &pkg); err != nil || pkg.Name != "is-number" || pkg.Version != "7.0.0" {
		return fmt.Errorf("wrong installed public dependency: %+v err=%v", pkg, err)
	}
	source, err := os.ReadFile(filepath.Join(root, "index.js"))
	if err != nil || len(source) == 0 {
		return fmt.Errorf("installed dependency source absent: %v", err)
	}
	cachePath := ".pnpm-store/v3/files"
	if language == "npm" {
		cachePath = ".npm/_cacache/content-v2"
	}
	found := false
	err = filepath.WalkDir(filepath.Join(ws, filepath.FromSlash(cachePath)), func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() || found {
			return err
		}
		if language == "npm" {
			found = languageNPMArchiveContains(path, source)
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			found = bytes.Equal(data, source)
		}
		return err
	})
	if err != nil || !found {
		return fmt.Errorf("actual is-number source bytes missing from %s dependency cache: %v", language, err)
	}
	return nil
}

func languageNPMArchiveContains(path string, source []byte) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return false // npm's content cache also contains JSON metadata.
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if err != nil {
			return false
		}
		if header.Name == "package/index.js" && header.Size == int64(len(source)) {
			data, err := io.ReadAll(io.LimitReader(archive, int64(len(source))+1))
			return err == nil && bytes.Equal(data, source)
		}
	}
}

func realNodeCacheFixture(manager, image string) realLanguageFixture {
	version, cacheRoot, cli := "10.9.2", ".npm", "npm/bin/npm-cli.js"
	if manager == "pnpm" {
		version, cacheRoot, cli = "9.15.5", ".pnpm-store", "pnpm/bin/pnpm.cjs"
	}
	identity := "pipewright-language-cache-v1 " + manager + " is-number@7.0.0"
	expected := identity + " 42=true nope=false\n"
	manifest, _ := json.Marshal(map[string]any{
		"name": "pipewright-language-cache-acceptance", "version": "1.0.0", "private": true,
		"packageManager": manager + "@" + version, "dependencies": map[string]string{"is-number": "7.0.0"},
	})
	message, _ := json.Marshal(expected)
	fixture := realLanguageFixture{
		name: manager, image: image, identity: identity, expectedText: expected, paths: []string{cacheRoot},
		vars: []pipeline.BuildVar{
			{Key: "NODE_OPTIONS", Value: "--max-old-space-size=256"},
			{Key: "NPM_CONFIG_CACHE", Value: "/workspace/" + cacheRoot + "/.bootstrap"},
			{Key: "NPM_CONFIG_REGISTRY", Value: "https://registry.npmjs.org"},
			{Key: "NPM_CONFIG_USERCONFIG", Value: "/dev/null"},
			{Key: "NPM_CONFIG_UPDATE_NOTIFIER", Value: "false"},
			{Key: "XDG_CACHE_HOME", Value: "/workspace/" + cacheRoot + "/.metadata"},
			{Key: "CI", Value: "true"},
		},
		files: map[string]string{
			"app/package.json": string(manifest) + "\n",
			"app/build.cjs": `const fs = require('node:fs');
const isNumber = require('is-number');
const dependency = require('is-number/package.json');
if (dependency.version !== '7.0.0' || !isNumber(42) || isNumber('nope')) throw new Error('wrong real dependency');
fs.mkdirSync('out', { recursive: true });
fs.writeFileSync('out/result.txt', ` + string(message) + `);
`,
		},
	}
	tool := "/workspace/" + cacheRoot + "/.tool"
	invoke := "node " + tool + "/node_modules/" + cli
	versions := "node -e 'console.log(\"PIPEWRIGHT_NODE_RUNTIME=\" + process.version)'\n" +
		"manager_version=$(" + invoke + " --version)\ntest \"$manager_version\" = '" + version + "'\n" +
		"printf 'PIPEWRIGHT_PACKAGE_MANAGER=%s@%s\\n' '" + manager + "' \"$manager_version\"\n"
	install := invoke + " install --ignore-scripts --no-audit --no-fund --package-lock=false --cache /workspace/.npm"
	negative := invoke + " install --ignore-scripts --no-audit --no-fund --package-lock=false --offline --cache /workspace/.empty-npm"
	failure := "grep -F ENOTCACHED /workspace/.negative.log"
	if manager == "pnpm" {
		install = invoke + " install --ignore-scripts --lockfile=false --store-dir /workspace/.pnpm-store"
		negative = "XDG_CACHE_HOME=/workspace/.empty-pnpm-metadata " + invoke +
			" install --ignore-scripts --lockfile=false --offline --store-dir /workspace/.empty-pnpm"
		failure = "grep -E 'ERR_PNPM_(NO_OFFLINE_META|NO_OFFLINE_TARBALL)' /workspace/.negative.log"
	}
	// No lockfile is written: DeriveKey sees identical generated fixtures before and after install.
	fixture.cold = "npm install --prefix " + tool + " --ignore-scripts --no-audit --no-fund --package-lock=false " + manager + "@" + version + "\n" +
		versions + install + "\nnode build.cjs\n"
	fixture.warm = versions +
		"if " + negative + " > /workspace/.negative.log 2>&1; then cat /workspace/.negative.log; echo unexpected-empty-cache-success; exit 1; fi\n" +
		failure + "\necho PIPEWRIGHT_EMPTY_CACHE_OFFLINE_FAILED=" + manager + "\nrm -rf node_modules out\n" +
		install + " --offline\nnode build.cjs\n"
	return fixture
}

func realGoCacheFixture(image string) realLanguageFixture {
	identity := "pipewright-language-cache-v1 go go-humanize@v1.0.1"
	proxy := os.Getenv("PIPEWRIGHT_TEST_GO_PROXY")
	if proxy == "" {
		proxy = "https://proxy.golang.org"
	}
	fixture := realLanguageFixture{
		name: "go", image: image, identity: identity, expectedText: identity + " 1,234,567\n", binary: true,
		paths: []string{".gomodcache", ".gocache"},
		vars: []pipeline.BuildVar{
			{Key: "GOMODCACHE", Value: "/workspace/.gomodcache"},
			{Key: "GOCACHE", Value: "/workspace/.gocache"},
			{Key: "GOPATH", Value: "/workspace/.gopath"},
			{Key: "GOPROXY", Value: proxy},
			{Key: "GOTOOLCHAIN", Value: "local"},
			{Key: "GOENV", Value: "off"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "GOMAXPROCS", Value: "1"},
			{Key: "GOMEMLIMIT", Value: "384MiB"},
		},
		files: map[string]string{
			"app/go.mod": "module example.com/pipewright-language-cache-acceptance\n\ngo 1.22.0\n\nrequire github.com/dustin/go-humanize v1.0.1\n",
			"app/go.sum": "github.com/dustin/go-humanize v1.0.1 h1:GzkhY7T5VNhEkwH0PVJgjz+fX1rhBrR7pRT3mDkpeCY=\n" +
				"github.com/dustin/go-humanize v1.0.1/go.mod h1:Mu1zIs6XwVuF/gI1OepvI0qD18qycQx+mFykh5fBlto=\n",
			"app/main.go": `package main
import (
    "fmt"
    "github.com/dustin/go-humanize"
)
func main() { fmt.Printf("` + identity + ` %s\n", humanize.Comma(1234567)) }
`,
		},
	}
	versions := "printf 'PIPEWRIGHT_GO_RUNTIME=%s\\n' \"$(go version)\"\n" +
		"test \"$(go env GOMODCACHE)\" = /workspace/.gomodcache\n" +
		"test \"$(go env GOCACHE)\" = /workspace/.gocache\n"
	build := "mkdir -p out\ngo build -x -mod=readonly -trimpath -p=1 -o out/cache-probe .\n" +
		"test -s out/cache-probe\n./out/cache-probe > out/result.txt\n"
	fixture.cold = versions + build
	fixture.warm = "export GOPROXY=off GOSUMDB=off\n" + versions +
		"if GOMODCACHE=/workspace/.empty-gomodcache GOCACHE=/workspace/.empty-gocache go build -mod=readonly -trimpath -p=1 -o /workspace/.negative-output . > /workspace/.negative.log 2>&1; then cat /workspace/.negative.log; echo unexpected-empty-cache-success; exit 1; fi\n" +
		"grep -F 'module lookup disabled by GOPROXY=off' /workspace/.negative.log\n" +
		"test ! -e /workspace/.negative-output\necho PIPEWRIGHT_EMPTY_CACHE_OFFLINE_FAILED=go\nrm -rf out\n" + build
	return fixture
}
