package build

// Physical Docker and Git acceptance, with explicit test-only failure injection.
// No fabricated container results and no changed frozen source/config. The
// shared retryHarness manually performs worker completion/settlement; these
// tests do not claim physical OS restart, real WorkerPool dispatch or SSE.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var nativeNodeMarker = regexp.MustCompile("# NR_NODE:([a-z0-9_-]+)")

type nativeRetryDriver struct {
	*shellDriver
	mu       sync.Mutex
	fail     map[string]bool
	counts   map[string]int
	images   []string
	builtIDs map[string]string
	builds   int
	pushes   int
}

func (d *nativeRetryDriver) Push(ctx context.Context, ref string, line func(string, string)) (int, error) {
	d.mu.Lock()
	d.pushes++
	d.mu.Unlock()
	return d.shellDriver.Push(ctx, ref, line)
}

func (d *nativeRetryDriver) RunToolchain(ctx context.Context, image, host, work string, env, cmd []string, res pipeline.Resource, line func(string, string)) (int, error) {
	key := ""
	if len(cmd) > 2 {
		if m := nativeNodeMarker.FindStringSubmatch(cmd[2]); len(m) == 2 {
			key = m[1]
		}
	}
	// build_image's dist adapter invokes the real default npm command. The
	// fixture's package.json supplies only an instrumentation identity, never a
	// replacement command/result; stdout must still contain the actual marker.
	if key == "" {
		var pkg struct {
			NativeNode string `json:"pipewrightNativeNode"`
		}
		if data, err := os.ReadFile(filepath.Join(host, "package.json")); err == nil && json.Unmarshal(data, &pkg) == nil && nativeNodeMarker.MatchString("# NR_NODE:"+pkg.NativeNode) {
			key = pkg.NativeNode
		}
	}
	if len(cmd) > 0 && cmd[0] == "mvn" {
		key = "jarbuild"
		env = append(append([]string(nil), env...), "MAVEN_ARGS=-o")
	}
	for _, v := range env {
		if strings.HasPrefix(v, "MATRIX_OS=") {
			key += "/" + strings.TrimPrefix(v, "MATRIX_OS=")
		}
	}
	d.mu.Lock()
	fail := d.fail[key]
	d.mu.Unlock()
	flag := "0"
	if fail {
		flag = "1"
	}
	env = append(append([]string(nil), env...), "PW_NATIVE_FAIL="+flag)
	counted := false
	return d.shellDriver.RunToolchain(ctx, image, host, work, env, cmd, res, func(stream, text string) {
		if stream == "stdout" && (text == "NR_EXEC:"+strings.Split(key, "/")[0] || key == "jarbuild" && strings.Contains(text, "Building jar:")) && !counted {
			d.mu.Lock()
			d.counts[key]++
			d.mu.Unlock()
			counted = true
		}
		if line != nil {
			line(stream, text)
		}
	})
}
func (d *nativeRetryDriver) Build(ctx context.Context, dir, df, tag string, args, secrets []string, line func(string, string)) (int, error) {
	code, err := d.shellDriver.Build(ctx, dir, df, tag, args, secrets, line)
	var identity runretry.ImageRef
	if code == 0 && err == nil {
		identity, _ = d.ImageIdentity(ctx, tag)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.builds++
	if code == 0 && err == nil {
		d.images = append(d.images, tag)
		d.builtIDs[tag] = identity.ImageID
	}
	return code, err
}
func (d *nativeRetryDriver) count(key string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counts[key]
}
func (d *nativeRetryDriver) failing(key string, value bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail[key] = value
}

type nativeRetryCloner struct {
	repo, sha string
	mu        sync.Mutex
	fail      bool
	count     int
}

func (c *nativeRetryCloner) Clone(ctx context.Context, _, _, _, branch, commit, dest string) (*CloneResolved, error) {
	c.mu.Lock()
	fail := c.fail
	c.count++
	c.mu.Unlock()
	if commit != "" && commit != c.sha {
		return nil, ErrCloneFailed
	}
	repo := c.repo
	if fail {
		// Failure still comes from an actual Git clone of a missing local path.
		// Restoring reachability changes the environment, not the frozen spec.
		repo = filepath.Join(c.repo, "missing-repository")
	}
	return (&Cloner{allowInsecure: true}).Clone(ctx, repo, "", "", branch, commit, dest)
}
func nativeRetryReady(t *testing.T) string {
	t.Helper()
	if os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_NATIVE") != "1" {
		t.Skip("requires current scoped isolated105 acceptance")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("native retry acceptance requires Linux")
	}
	image := os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_IMAGE")
	if !regexp.MustCompile("^sha256:[0-9a-f]{64}$").MatchString(image) {
		t.Fatal("requires exact cached toolchain image ID")
	}
	for _, tool := range []string{"docker", "sh", "tar", "sha256sum", "base64", "scp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("enabled native gate requires %s: %v", tool, err)
		}
	}
	nativeRequireImage(t, image)
	return image
}
func nativeRequireImage(t *testing.T, image string) {
	t.Helper()
	if !regexp.MustCompile("^sha256:[0-9a-f]{64}$").MatchString(image) {
		t.Fatal("native image must be an exact cached sha256 ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, stderr, code, err := (execCommander{}).Run(ctx, "docker", []string{"image", "inspect", "--format", "{{.Id}}", image}, "")
	if err != nil || code != 0 || strings.TrimSpace(out) != image {
		t.Fatalf("required cached image unavailable: exit=%d err=%v diagnostic=%s", code, err, stderr)
	}
}
func nativeJob(image, id, body string, needs ...string) pipeline.Job {
	commands := "# NR_NODE:" + id + "\nprintf 'NR_EXEC:" + id + "\\n'\nif [ \"$PW_NATIVE_FAIL\" = 1 ]; then exit 23; fi\n" + body
	return pipeline.Job{ID: id, Name: id, Type: "script", Needs: needs, Config: map[string]any{"image": image, "commands": commands, "timeoutSeconds": 60, "memory": "192m", "cpu": "0.5"}}
}
func nativeGitFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"source.txt":       "original-committed-source\n",
		"Dockerfile":       "FROM scratch\nCOPY source.txt /source.txt\nLABEL io.pipewright.acceptance.run=pipewright-node-retry-20261028\n",
		"package.json":     `{"name":"native-retry-fixture","version":"1.0.0","private":true,"pipewrightNativeNode":"filebuild","scripts":{"build":"node build.cjs"}}`,
		".npmrc":           "audit=false\nfund=false\noffline=true\nupdate-notifier=false\n",
		"build.cjs":        `const fs=require('node:fs');console.log('NR_EXEC:filebuild');if(process.env.PW_NATIVE_FAIL==='1')process.exit(23);fs.mkdirSync('dist',{recursive:true});fs.writeFileSync('dist/index.html','native-build-image-dist');`,
		"pom.xml":          `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>invalid.acceptance</groupId><artifactId>native-retry</artifactId><version>1.0</version><packaging>jar</packaging></project>`,
		"Dockerfile.local": "FROM " + os.Getenv("PIPEWRIGHT_TEST_NODE_RETRY_IMAGE") + "\nCOPY source.txt /native-source.txt\nLABEL io.pipewright.acceptance.fixture=" + uuid.NewString() + "\nCMD [\"node\",\"-e\",\"require('http').createServer((q,s)=>s.end(require('fs').readFileSync('/native-source.txt'))).listen(8080)\"]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wt.Add("."); err != nil {
		t.Fatal(err)
	}
	sha, err := wt.Commit("isolated retry fixture", &gogit.CommitOptions{Author: &object.Signature{Name: "Pipewright acceptance", Email: "acceptance@example.invalid", When: time.Unix(1700000000, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	return dir, sha.String()
}
func nativeHarness(t *testing.T, spec pipeline.Spec, modify ...func(*runretry.Snapshot)) (*retryHarness, *nativeRetryDriver, *nativeRetryCloner) {
	t.Helper()
	nativeRetryReady(t)
	repo, sha := nativeGitFixture(t)
	h := newRetryHarness(t, spec, func(s *runretry.Snapshot) {
		s.CommitSHA = sha
		s.RequestedRef = "master"
		s.Trigger.Branch = "master"
		s.Project["name"] = "node-retry-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		s.Settings = map[string]any{}
		for _, fn := range modify {
			fn(s)
		}
	})
	owned := &nativeLabelCommander{Commander: execCommander{}}
	driver := &nativeRetryDriver{shellDriver: &shellDriver{bin: "docker", cmdr: owned, copyWorkspace: true, workspaceReconcileAfter: time.Now().Add(24 * time.Hour)}, fail: map[string]bool{}, counts: map[string]int{}, builtIDs: map[string]string{}}
	cloner := &nativeRetryCloner{repo: repo, sha: sha}
	h.b.driver = driver
	h.b.cloner = cloner
	runs := run.New(h.store.DB)
	// Construction installs the inherited-artifact resolver. This pool is never
	// started; execution and worker completion are explicit in retryHarness.
	_ = run.NewWorkerPool(runs, run.WithRetryRuntime(h.s))
	h.executor = func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
		return NewStageExecutor(h.b, nil)(ctx, r, stage, &nativePersistReporter{StageReporter: rep, runs: runs, id: r.ID})
	}
	t.Cleanup(func() {
		driver.mu.Lock()
		tags := append([]string(nil), driver.images...)
		ids := map[string]string{}
		for ref, id := range driver.builtIDs {
			ids[ref] = id
		}
		driver.mu.Unlock()
		for _, tag := range tags {
			if !strings.HasPrefix(tag, "pipewright/node-retry-") {
				t.Errorf("unexpected created image ref")
				continue
			}
			nativeRemoveImageTag(t, tag, ids[tag])
		}
	})
	return h, driver, cloner
}
func nativeRemoveImageTag(t *testing.T, ref, wantID string) {
	t.Helper()
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(wantID) {
		t.Error("owned image cleanup lacks original identity")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, stderr, code, err := (execCommander{}).Run(ctx, "docker", []string{"image", "inspect", "--format", "{{.Id}}", ref}, "")
	if code == 1 && strings.Contains(stderr, "No such image: "+ref) {
		return
	}
	if err != nil || code != 0 || strings.TrimSpace(out) != wantID {
		t.Error("owned image tag drifted; preserved for manual review")
		return
	}
	_, _, code, err = (execCommander{}).Run(ctx, "docker", []string{"image", "rm", ref}, "")
	if err != nil || code != 0 {
		t.Errorf("owned image lifecycle removal failed: code=%d err=%v", code, err)
	}
}

type nativeLabelCommander struct{ Commander }

func (c *nativeLabelCommander) owned(args []string) []string {
	at := 0
	if len(args) > 0 && (args[0] == "create" || args[0] == "run" || args[0] == "build") {
		at = 1
	}
	if len(args) > 1 && args[0] == "network" && args[1] == "create" {
		at = 2
	}
	if at == 0 {
		return args
	}
	result := append([]string(nil), args[:at]...)
	result = append(result, "--label", "io.pipewright.acceptance.run=pipewright-node-retry-20261028")
	if at == 1 && (args[0] == "create" || args[0] == "run") {
		result = append(result, "--pull=never")
		hasNetwork := false
		for _, arg := range args {
			if arg == "--network" || strings.HasPrefix(arg, "--network=") {
				hasNetwork = true
			}
		}
		if !hasNetwork {
			result = append(result, "--network", "none")
		}
	}
	if at == 1 && args[0] == "build" {
		result = append(result, "--pull=false", "--network=none")
	}
	return append(result, args[at:]...)
}
func (c *nativeLabelCommander) Run(ctx context.Context, name string, args []string, stdin string) (string, string, int, error) {
	return c.Commander.Run(ctx, name, c.owned(args), stdin)
}
func (c *nativeLabelCommander) Stream(ctx context.Context, name string, args []string, stdin string, line func(string, string)) (int, error) {
	return c.Commander.Stream(ctx, name, c.owned(args), stdin, line)
}
func nativeAssertCounts(t *testing.T, d *nativeRetryDriver, want map[string]int) {
	t.Helper()
	for key, count := range want {
		if got := d.count(key); got != count {
			t.Errorf("real container marker %s: got %d want %d", key, got, count)
		}
	}
}
func nativeExecute(t *testing.T, h *retryHarness, id string, wantFailure bool) {
	t.Helper()
	defer nativeLogLedger(t, h, id)
	err := h.execute(t, id)
	if (err != nil) != wantFailure {
		t.Fatalf("actual attempt failure=%t want=%t err=%v logs=%v", err != nil, wantFailure, err, h.sink.logs)
	}
	if h.settleErr != nil {
		t.Fatalf("actual resources not drained: %v", h.settleErr)
	}
	drained, drainErr := h.s.ResourcesDrained(context.Background(), id)
	if drainErr != nil || !drained {
		t.Fatalf("actual resource proof not drained after completion: drained=%t err=%v", drained, drainErr)
	}
}

// Deliberately limited diagnostic fields. No encrypted ref, credentials, raw
// ResourceRef, output/env value, arbitrary scripts or snapshot is logged.
func nativeLogLedger(t *testing.T, h *retryHarness, id string) {
	t.Helper()
	type state struct{ ID, Node, Status string }
	var resources, targets, activities, cleanup []state
	rows, err := h.store.DB.Query("SELECT id,node_key,state FROM run_retry_resources WHERE attempt_id=? ORDER BY id LIMIT 16", id)
	if err == nil {
		for rows.Next() {
			var r state
			if rows.Scan(&r.ID, &r.Node, &r.Status) == nil {
				resources = append(resources, r)
			}
		}
		rows.Close()
	}
	rows, err = h.store.DB.Query("SELECT id,node_key,state FROM run_retry_external_targets WHERE attempt_id=? ORDER BY id LIMIT 16", id)
	if err == nil {
		for rows.Next() {
			var r state
			if rows.Scan(&r.ID, &r.Node, &r.Status) == nil {
				targets = append(targets, r)
			}
		}
		rows.Close()
	}
	rows, err = h.store.DB.Query("SELECT a.activity_id,a.phase,CASE WHEN a.released_at IS NOT NULL THEN 'released' WHEN EXISTS(SELECT 1 FROM run_retry_reader_exits e WHERE e.activity_id=a.activity_id AND e.attempt_id=a.run_id AND e.owner_epoch=a.owner_epoch) THEN 'held_reader_exit_matched' ELSE 'held_reader_exit_unverified' END FROM project_activity a WHERE a.run_id=? ORDER BY a.activity_id LIMIT 16", id)
	if err == nil {
		for rows.Next() {
			var r state
			if rows.Scan(&r.ID, &r.Node, &r.Status) == nil {
				activities = append(activities, r)
			}
		}
		rows.Close()
	}
	current, err := h.s.Context(context.Background(), id)
	if err == nil {
		for _, node := range current.Graph.Nodes {
			if node.ControlKey == "cleanup" {
				var status string
				var drained int
				if h.store.DB.QueryRow("SELECT status,resources_drained FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", id, node.Key).Scan(&status, &drained) == nil {
					cleanup = append(cleanup, state{ID: node.Key, Node: status, Status: fmt.Sprint(drained)})
				}
			}
		}
	}
	data, _ := json.Marshal(struct{ Resources, Targets, Activities, Cleanup []state }{resources, targets, activities, cleanup})
	t.Logf("native ledger attempt=%s %s", id, data)
}

func TestNativeRecoveryActualScriptsAndSource(t *testing.T) {
	image := nativeRetryReady(t)
	for _, kind := range []string{"script", "custom", "build_frontend", "build_backend", "templated"} {
		t.Run(kind, func(t *testing.T) {
			a := nativeJob(image, "produce", `test "$(cat source.txt)" = original-committed-source
node -e 'const fs=require("node:fs"),c=require("node:crypto"),t=c.randomBytes(24).toString("hex");fs.writeFileSync("out.txt",c.createHash("sha256").update(t).digest("hex"));fs.writeFileSync(process.env.PIPEWRIGHT_ENV,"OUTPUT_TOKEN="+t+"\n")'`)
			a.Config["artifacts"] = `[{"path":"out.txt","name":"payload"}]`
			b := nativeJob(image, "consume", `test "$(cat source.txt)" = original-committed-source
node -e 'const fs=require("node:fs"),c=require("node:crypto"),t=process.env.OUTPUT_TOKEN;if(!/^[a-f0-9]{48}$/.test(t)||c.createHash("sha256").update(t).digest("hex")!==fs.readFileSync("out.txt","utf8"))process.exit(4)'
printf 'NR_RESTORED_SECRET=%s\n' "$OUTPUT_TOKEN"`, "produce")
			b.Type = kind
			if kind == "templated" {
				b.Config["expected"] = "payload"
				b.Config["commandTemplate"] = b.Config["commands"].(string) + "\ntest '{{expected}}' = payload"
			}
			h, d, c := nativeHarness(t, retrySpec(a, b, nativeJob(image, "after", "true", "consume")))
			d.failing("consume", true)
			nativeExecute(t, h, "root", true)
			if err := os.WriteFile(filepath.Join(c.repo, "source.txt"), []byte("moved-branch\n"), 0600); err != nil {
				t.Fatal(err)
			}
			repo, err := gogit.PlainOpen(c.repo)
			if err != nil {
				t.Fatal(err)
			}
			wt, err := repo.Worktree()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = wt.Add("source.txt"); err != nil {
				t.Fatal(err)
			}
			if _, err = wt.Commit("advance branch", &gogit.CommitOptions{Author: &object.Signature{Name: "Acceptance", Email: "acceptance@example.invalid", When: time.Unix(1700000010, 0)}}); err != nil {
				t.Fatal(err)
			}
			d.failing("consume", false)
			child := h.submit(t, "root", []string{h.node(t, "root", "stage", "consume").Key}, nil)
			nativeExecute(t, h, child, false)
			nativeAssertCounts(t, d, map[string]int{"produce": 1, "consume": 2, "after": 1})
			masked := false
			for _, line := range h.sink.logs {
				if strings.Contains(line, "NR_RESTORED_SECRET=[MASKED]") {
					masked = true
				}
				if regexp.MustCompile(`NR_RESTORED_SECRET=[a-f0-9]{48}`).MatchString(line) {
					t.Fatal("restored dynamic secret leaked")
				}
			}
			if !masked {
				t.Fatal("real restored secret echo was not masked")
			}
			t.Logf("real Git checkout frozen at %s; original producer once, child consumed exact file/env", c.sha)
		})
	}
}
func TestNativeRecoveryActualDAGMatrixAndLocalImage(t *testing.T) {
	image := nativeRetryReady(t)
	t.Run("matrix_collision_rejected_before_capture", func(t *testing.T) {
		h, d, _ := nativeHarness(t, retrySpec(nativeJob(image, "work", "true")))
		ctx, err := h.s.Prepare(context.Background(), "root")
		if err != nil {
			t.Fatal(err)
		}
		ex, _ := runretry.ExecutionFrom(ctx)
		snapshot := ex.Snapshot()
		snapshot.Compiled = nil
		snapshot.Spec.Stages[0].Matrix = map[string][]string{"os": {"linux/arm", "linux-arm"}}
		if _, err = h.store.DB.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('collision','p1','queued','old')"); err != nil {
			t.Fatal(err)
		}
		if err = h.s.SaveManifest(context.Background(), "collision", snapshot); !errors.Is(err, runretry.ErrInvalid) {
			t.Fatal("sanitized collision accepted", err)
		}
		var captures, nodes int
		if err = h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_manifests WHERE root_id='collision'").Scan(&captures); err != nil {
			t.Fatal(err)
		}
		if err = h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id='collision'").Scan(&nodes); err != nil {
			t.Fatal(err)
		}
		if captures != 0 || nodes != 0 || d.count("work") != 0 {
			t.Fatal("collision created execution evidence or dispatched", captures, nodes)
		}
	})
	t.Run("two_failures_join", func(t *testing.T) {
		a := nativeJob(image, "a", "printf payload > out.txt")
		a.Config["artifacts"] = `[{"path":"out.txt"}]`
		h, d, _ := nativeHarness(t, retrySpec(a, nativeJob(image, "b", "test -f out.txt", "a"), nativeJob(image, "c", "test -f out.txt", "a"), nativeJob(image, "join", "true", "b", "c")))
		d.failing("b", true)
		d.failing("c", true)
		nativeExecute(t, h, "root", true)
		d.failing("b", false)
		one := h.submit(t, "root", []string{h.node(t, "root", "stage", "b").Key}, nil)
		nativeExecute(t, h, one, true)
		if d.count("join") != 0 {
			t.Fatal("join ran before all required branches")
		}
		d.failing("c", false)
		two := h.submit(t, one, []string{h.node(t, one, "stage", "c").Key}, nil)
		nativeExecute(t, h, two, false)
		nativeAssertCounts(t, d, map[string]int{"a": 1, "b": 2, "c": 2, "join": 1})
	})
	t.Run("matrix_raw_axes", func(t *testing.T) {
		a := nativeJob(image, "a", "printf payload > out.txt")
		a.Config["artifacts"] = `[{"path":"out.txt"}]`
		spec := retrySpec(a, nativeJob(image, "b", "test -f out.txt", "a"))
		spec.Stages[0].Matrix = map[string][]string{"os": {"linux/arm", "linux-arm64"}}
		h, d, _ := nativeHarness(t, spec)
		d.failing("b/linux/arm", true)
		nativeExecute(t, h, "root", true)
		current, err := h.s.Context(context.Background(), "root")
		if err != nil {
			t.Fatal(err)
		}
		key := ""
		for _, n := range current.Graph.Nodes {
			if n.JobID == "b" && n.Axes["os"] == "linux/arm" {
				key = n.Key
			}
		}
		if key == "" {
			t.Fatal("raw matrix key unavailable")
		}
		d.failing("b/linux/arm", false)
		child := h.submit(t, "root", []string{key}, nil)
		nativeExecute(t, h, child, false)
		nativeAssertCounts(t, d, map[string]int{"a/linux/arm": 1, "a/linux-arm64": 1, "b/linux/arm": 2, "b/linux-arm64": 1})
	})
	t.Run("local_image_no_registry", func(t *testing.T) {
		build := pipeline.Job{ID: "image", Name: "image", Type: "build_image", Config: map[string]any{"artifactType": "image", "dockerfilePath": "Dockerfile"}}
		h, d, _ := nativeHarness(t, retrySpec(build, nativeJob(image, "work", "true", "image"), nativeJob(image, "after", "true", "work")))
		d.failing("work", true)
		nativeExecute(t, h, "root", true)
		d.mu.Lock()
		if len(d.images) != 1 {
			d.mu.Unlock()
			t.Fatal("real local image missing")
		}
		tag := d.images[0]
		d.mu.Unlock()
		before, err := d.ImageIdentity(context.Background(), tag)
		if err != nil {
			t.Fatal(err)
		}
		if before.ImageID == "" || before.ConfigSHA == "" || before.RuntimeID == "" {
			t.Fatal("local image identity proof incomplete")
		}
		d.failing("work", false)
		child := h.submit(t, "root", []string{h.node(t, "root", "stage", "work").Key}, nil)
		nativeExecute(t, h, child, false)
		after, err := d.ImageIdentity(context.Background(), tag)
		if err != nil || after.ImageID != before.ImageID || after.ConfigSHA != before.ConfigSHA || after.RuntimeID != before.RuntimeID {
			t.Fatal("local identity changed", err)
		}
		if d.builds != 1 {
			t.Fatalf("real image built %d times", d.builds)
		}
		if d.pushes != 0 {
			t.Fatal("local identity reuse unexpectedly pushed to a registry")
		}
		nativeAssertCounts(t, d, map[string]int{"work": 2, "after": 1})
		t.Logf("actual local Docker image %s reused by runtime/config identity without a configured registry or push; daemon-supplied digest is optional metadata", before.ImageID)
	})
}
func TestNativeRecoveryActualServicesPost(t *testing.T) {
	image := nativeRetryReady(t)
	service := os.Getenv("PIPEWRIGHT_TEST_MATRIX_SERVICE_IMAGE")
	if !regexp.MustCompile("^sha256:[0-9a-f]{64}$").MatchString(service) {
		t.Fatal("requires exact owned fixture service image")
	}
	nativeRequireImage(t, service)
	seedJS := `node -e 'fetch("http://fixture:8080/seed",{method:"POST"}).then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(2))'`
	workJS := `node -e 'fetch("http://fixture:8080/state").then(r=>r.json()).then(x=>{if(x.seeds!==1)process.exit(3)}).catch(()=>process.exit(2))'`
	spec := retrySpec(nativeJob(image, "seed", seedJS), nativeJob(image, "work", workJS, "seed"))
	spec.Stages[0].Services = []pipeline.ServiceSpec{{Name: "fixture", Image: service}}
	for _, v := range []struct{ condition, id string }{{"always", "always"}, {"on_failure", "failure"}, {"on_success", "success"}} {
		job := nativeJob(image, v.id, "true")
		spec.Stages[0].Post = append(spec.Stages[0].Post, pipeline.PostStep{Condition: v.condition, Image: image, Commands: []string{job.Config["commands"].(string)}})
	}
	h, d, _ := nativeHarness(t, spec)
	d.failing("work", true)
	nativeExecute(t, h, "root", true)
	work := h.node(t, "root", "stage", "work")
	seed := h.node(t, "root", "stage", "seed")
	var seedStatus, seedReason string
	if err := h.store.DB.QueryRow("SELECT status,reason_code FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", seed.Key).Scan(&seedStatus, &seedReason); err != nil {
		t.Fatal(err)
	}
	if seedStatus != "success" || d.count("seed") != 1 {
		nativeLogLedger(t, h, "root")
		t.Fatalf("Services fixture seed did not succeed: status=%s reason=%s actualSeedMarkers=%d actualWorkMarkers=%d; no retry plan attempted", seedStatus, seedReason, d.count("seed"), d.count("work"))
	}
	plan, err := h.s.Plan(context.Background(), "root", []string{work.Key})
	if err != nil || plan.CanSubmit || len(plan.RebuildGroups) == 0 {
		t.Fatalf("empty fresh service incorrectly allowed: %v %+v", err, plan)
	}
	d.failing("work", false)
	child := h.submit(t, "root", []string{work.Key}, []string{seed.Key})
	nativeExecute(t, h, child, false)
	nativeAssertCounts(t, d, map[string]int{"seed": 2, "work": 2, "always": 2, "failure": 1, "success": 1})
	t.Log("fresh service seeded once per actual attempt; lifecycle exit independently verified")
}

func nativeControlKey(t *testing.T, h *retryHarness, attempt, stage, control string) string {
	t.Helper()
	current, err := h.s.Context(context.Background(), attempt)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range current.Graph.Nodes {
		if node.ExecutionStageID == stage && node.ControlKey == control {
			return node.Key
		}
	}
	t.Fatal("actual captured control missing", stage, control)
	return ""
}

func TestNativeRecoveryActualStageDAGAndAllowFailure(t *testing.T) {
	image := nativeRetryReady(t)
	for _, allow := range []bool{false, true} {
		name := "stage_join"
		if allow {
			name = "allow_failure_reexecutes_descendants"
		}
		t.Run(name, func(t *testing.T) {
			spec := pipeline.Spec{Stages: []pipeline.Stage{
				{ID: "root-stage", Name: "root-stage", Kind: "build", Jobs: []pipeline.Job{nativeJob(image, "root", "true")}},
				{ID: "left", Name: "left", Kind: "build", Needs: []string{"root-stage"}, AllowFailure: allow, Jobs: []pipeline.Job{nativeJob(image, "left", "true")}},
				{ID: "right", Name: "right", Kind: "build", Needs: []string{"root-stage"}, Jobs: []pipeline.Job{nativeJob(image, "right", "true")}},
				{ID: "join", Name: "join", Kind: "build", Needs: []string{"left", "right"}, Jobs: []pipeline.Job{nativeJob(image, "join", "true")}},
			}}
			h, d, _ := nativeHarness(t, spec)
			d.failing("left", true)
			nativeExecute(t, h, "root", true)
			priorJoin := 0
			if allow {
				priorJoin = 1
			}
			nativeAssertCounts(t, d, map[string]int{"root": 1, "left": 1, "right": 1, "join": priorJoin})
			d.failing("left", false)
			child := h.submit(t, "root", []string{h.node(t, "root", "left", "left").Key}, nil)
			nativeExecute(t, h, child, false)
			nativeAssertCounts(t, d, map[string]int{"root": 1, "left": 2, "right": 1, "join": priorJoin + 1})
		})
	}
}

func TestNativeRecoveryActualSourceNodeAndPostFailure(t *testing.T) {
	image := nativeRetryReady(t)
	t.Run("source_checkout_failure", func(t *testing.T) {
		source := pipeline.Job{ID: "source", Name: "source", Type: "git_source"}
		h, d, c := nativeHarness(t, retrySpec(source, nativeJob(image, "work", `test "$(cat source.txt)" = original-committed-source`, "source")))
		c.mu.Lock()
		c.fail = true
		c.mu.Unlock()
		nativeExecute(t, h, "root", true)
		if d.count("work") != 0 {
			t.Fatal("consumer ran before failed actual checkout")
		}
		c.mu.Lock()
		c.fail = false
		c.mu.Unlock()
		child := h.submit(t, "root", []string{h.node(t, "root", "stage", "source").Key}, nil)
		nativeExecute(t, h, child, false)
		nativeAssertCounts(t, d, map[string]int{"work": 1})
		c.mu.Lock()
		clones := c.count
		c.mu.Unlock()
		if clones < 3 {
			t.Fatal("source failure and restored checkouts not physically attempted", clones)
		}
	})
	t.Run("post_failure_keeps_business_success", func(t *testing.T) {
		spec := retrySpec(nativeJob(image, "work", "true"))
		post := nativeJob(image, "post", "true")
		spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: image, Commands: []string{post.Config["commands"].(string)}}}
		h, d, _ := nativeHarness(t, spec)
		d.failing("post", true)
		nativeExecute(t, h, "root", false)
		key := nativeControlKey(t, h, "root", "stage", "post:0")
		var status string
		if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", "root", key).Scan(&status); err != nil || status != "failed" {
			t.Fatal("actual post failure not separately persisted", status, err)
		}
		d.failing("post", false)
		child := h.submit(t, "root", []string{key}, nil)
		nativeExecute(t, h, child, false)
		nativeAssertCounts(t, d, map[string]int{"work": 1, "post": 2})
	})
}
