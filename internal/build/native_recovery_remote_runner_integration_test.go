package build

// Root uses the existing explicitly settled harness; only the child uses a real
// WorkerPool. All remote
// execution/upload/capture is performed by target.Service over actual SSH.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
)

// A nonnil lookup selects the public executor entry point. Its deliberately
// different answer must never override the captured RunnerRefs serverId.
type nativeDifferentRunnerLookup struct{}

func (nativeDifferentRunnerLookup) RunnerFor(context.Context, string) (string, bool) {
	return "mutable-runner-must-not-be-used", true
}

type nativeRemoteContainerEvidence struct{ ID, Name, Owner, NodeKey string }
type nativeRemoteRunnerObserver struct {
	target.Service
	mu                              sync.Mutex
	serverID, image, scope, cidRoot string
	fail                            map[string]bool
	counts                          map[string]int
	containers                      map[string]nativeRemoteContainerEvidence
	diagnostics                     []string
}

// Embedding target.Service does not expose the concrete service's optional
// Capture method. Forward it explicitly so production remoteBinding performs
// its real connection snapshot and no test-provided identity is substituted.
func (p *nativeRemoteRunnerObserver) Capture(ctx context.Context, id string) (target.ConnectionSnapshot, error) {
	v, ok := p.Service.(interface {
		Capture(context.Context, string) (target.ConnectionSnapshot, error)
	})
	if !ok {
		return target.ConnectionSnapshot{}, runretry.ErrUnavailable
	}
	return v.Capture(ctx, id)
}
func (p *nativeRemoteRunnerObserver) failing(key string, v bool) {
	p.mu.Lock()
	p.fail[key] = v
	p.mu.Unlock()
}
func (p *nativeRemoteRunnerObserver) snapshot() (map[string]int, map[string]nativeRemoteContainerEvidence, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	counts := map[string]int{}
	for k, v := range p.counts {
		counts[k] = v
	}
	containers := map[string]nativeRemoteContainerEvidence{}
	for k, v := range p.containers {
		containers[k] = v
	}
	return counts, containers, append([]string(nil), p.diagnostics...)
}
func (p *nativeRemoteRunnerObserver) diagnostic(message string) {
	p.mu.Lock()
	p.diagnostics = append(p.diagnostics, message)
	p.mu.Unlock()
}

// The observer never fabricates an SSH result, exit code, upload or capture.
// Only actual Docker run argv receives test-only cached/no-network guards,
// a UUID-scoped label/cidfile, and the controlled failure environment variable.
func (p *nativeRemoteRunnerObserver) Exec(ctx context.Context, id string, cmd []string) (*target.ExecResult, error) {
	if id != p.serverID {
		return nil, runretry.ErrInvalid
	}
	key := ""
	if len(cmd) > 2 && cmd[0] == "docker" && cmd[1] == "run" {
		for _, arg := range cmd {
			if match := nativeNodeMarker.FindStringSubmatch(arg); len(match) == 2 {
				key = match[1]
			}
		}
	}
	if key == "" {
		result, err := p.Service.Exec(ctx, id, cmd)
		workspaceSetup := len(cmd) > 3 && (cmd[0] == "mkdir" || cmd[0] == "sh" && cmd[3] == "pw-remote-workspace-create")
		if workspaceSetup && (err != nil || result == nil || result.ExitCode != 0) {
			code := -1
			if result != nil {
				code = result.ExitCode
			}
			p.diagnostic(fmt.Sprintf("remote workspace setup failed: exit=%d transportError=%t", code, err != nil))
		}
		return result, err
	}
	if key != "produce" && key != "consume" && key != "after" {
		return nil, runretry.ErrInvalid
	}
	imageSeen := false
	name, owner := "", ""
	for i, arg := range cmd {
		imageSeen = imageSeen || arg == p.image
		if arg == "--cidfile" || strings.HasPrefix(arg, "--cidfile=") {
			return nil, runretry.ErrInvalid
		}
		if arg == "--name" && i+1 < len(cmd) {
			name = cmd[i+1]
		}
		if arg == "--label" && i+1 < len(cmd) && strings.HasPrefix(cmd[i+1], serviceOwnerLabel+"=") {
			owner = strings.TrimPrefix(cmd[i+1], serviceOwnerLabel+"=")
		}
	}
	node, ok := runretry.NodeFrom(ctx)
	if !ok || name == "" || owner == "" || !imageSeen {
		return nil, runretry.ErrInvalid
	}
	p.mu.Lock()
	fail := p.fail[key]
	p.mu.Unlock()
	flag := "0"
	if fail {
		flag = "1"
	}
	cidfile := filepath.Join(p.cidRoot, "native-runner-"+uuid.NewString()+".cid")
	actual := append([]string(nil), cmd[:2]...)
	actual = append(actual, "--pull=never", "--label", "io.pipewright.acceptance.fixture="+p.scope, "--cidfile", cidfile, "-e", "PW_NATIVE_FAIL="+flag)
	serviceNetwork := false
	for i, arg := range cmd {
		if arg == "--network" && i+1 < len(cmd) {
			serviceNetwork = true
		}
	}
	if !serviceNetwork {
		actual = append(actual, "--network=none")
	}
	actual = append(actual, cmd[2:]...)
	result, err := p.Service.Exec(ctx, id, actual)
	if result != nil {
		seen := 0
		for _, line := range strings.Split(result.Stdout, "\n") {
			if strings.TrimSuffix(line, "\r") == "NR_EXEC:"+key {
				seen++
			}
		}
		p.mu.Lock()
		p.counts[key] += seen
		p.mu.Unlock()
		if seen != 1 {
			p.diagnostic("actual remote stdout did not contain exactly one expected marker")
		}
	}
	// The actual Docker-generated cidfile outlives --rm, so independently prove
	// the exact full container identity without turning short-lived containers
	// into persistent fixtures or changing product cleanup policy.
	probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	proof, proofErr := p.Service.Exec(probe, id, []string{"cat", "--", cidfile})
	if proofErr != nil || proof == nil || proof.ExitCode != 0 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(strings.TrimSpace(proof.Stdout)) {
		p.diagnostic("real Docker cidfile evidence unavailable")
	} else {
		p.mu.Lock()
		p.containers[name] = nativeRemoteContainerEvidence{ID: strings.TrimSpace(proof.Stdout), Name: name, Owner: owner, NodeKey: node.Key}
		p.mu.Unlock()
		removed, removeErr := p.Service.Exec(probe, id, []string{"rm", "--", cidfile})
		if removeErr != nil || removed == nil || removed.ExitCode != 0 {
			p.diagnostic("exact own cidfile lifecycle removal failed")
		}
	}
	cancel()
	return result, err
}

func TestNativeRecoveryActualRemoteRunnerFilesEnvAndDrain(t *testing.T) {
	image := nativeRetryReady(t)
	a := nativeJob(image, "produce", `test "$(cat source.txt)" = original-committed-source
node -e 'const fs=require("node:fs"),c=require("node:crypto"),t=c.randomBytes(24).toString("hex");fs.writeFileSync("out.txt",c.createHash("sha256").update(t).digest("hex"));fs.writeFileSync(process.env.PIPEWRIGHT_ENV,"OUTPUT_TOKEN="+t+"\n")'`)
	a.Config["artifacts"] = `[{"path":"out.txt","name":"remote-payload"}]`
	b := nativeJob(image, "consume", `test "$(cat source.txt)" = original-committed-source
node -e 'const fs=require("node:fs"),c=require("node:crypto"),t=process.env.OUTPUT_TOKEN;if(!/^[a-f0-9]{48}$/.test(t)||c.createHash("sha256").update(t).digest("hex")!==fs.readFileSync("out.txt","utf8"))process.exit(4)'
printf 'NR_RESTORED_SECRET=%s\n' "$OUTPUT_TOKEN"`, "produce")
	h, localDriver, cloner := nativeHarness(t, retrySpec(a, b, nativeJob(image, "after", "true", "consume")), func(s *runretry.Snapshot) { s.RunnerRefs = map[string]string{"serverId": "native-runner"} })
	port, password, dir := nativeSSHFixture(t)
	targets, runs := nativeInstallDeployer(t, h)
	nativeAddServer(t, h, targets, "native-runner", port, password)
	observer := &nativeRemoteRunnerObserver{Service: targets, serverID: "native-runner", image: image, scope: "pw-native-remote-" + uuid.NewString(), cidRoot: dir, fail: map[string]bool{}, counts: map[string]int{}, containers: map[string]nativeRemoteContainerEvidence{}}
	runretry.WithResourceVerifier(func(ctx context.Context, ref runretry.ResourceRef) (bool, error) {
		if ref.ServerID != "" {
			return VerifyRemoteResource(ctx, observer, ref)
		}
		return h.b.VerifyResource(ctx, ref)
	})(h.s)
	stageExec := NewStageExecutorWithRunner(h.b, nil, nativeDifferentRunnerLookup{}, observer)
	h.executor = func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
		return stageExec(ctx, r, stage, &nativePersistReporter{StageReporter: rep, runs: runs, id: r.ID})
	}
	observer.failing("consume", true)
	nativeExecute(t, h, "root", true)
	rootContext, err := h.s.Context(context.Background(), "root")
	if err != nil || rootContext.CommitSHA != cloner.sha {
		t.Fatal("root remote execution did not bind the full original SHA", err)
	}
	producer := h.node(t, "root", "stage", "produce")
	consumer := h.node(t, "root", "stage", "consume")
	var producerStatus, producerReason string
	if err = h.store.DB.QueryRow("SELECT status,reason_code FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", producer.Key).Scan(&producerStatus, &producerReason); err != nil {
		t.Fatal(err)
	}
	if producerStatus != "success" {
		counts, _, diagnostics := observer.snapshot()
		t.Fatalf("remote producer status=%s reason=%s actualMarkers=%v workspaceDiagnostics=%v", producerStatus, producerReason, counts, diagnostics)
	}
	var producerBefore []byte
	if err = h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=? AND status='success' AND reusable=1", producer.Key).Scan(&producerBefore); err != nil || len(producerBefore) == 0 {
		t.Fatal("remote producer checkpoint not reusable", err)
	}
	nativeRemoteAssertDrained(t, h, observer, "root")
	counts, _, diagnostics := observer.snapshot()
	if len(diagnostics) > 0 {
		t.Fatal(diagnostics)
	}
	if counts["produce"] != 1 || counts["consume"] != 1 || counts["after"] != 0 {
		t.Fatal("root actual SSH/Docker marker counts differ", counts)
	}
	artifacts, err := runs.ListArtifacts(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	var original run.Artifact
	for _, artifact := range artifacts {
		if artifact.Metadata["logicalNodeKey"] == producer.Key {
			if original.ID != "" {
				t.Fatal("ambiguous remote producer artifact")
			}
			original = artifact
		}
	}
	if original.ID == "" || original.RunID != "root" {
		t.Fatal("original remote producer artifact missing")
	}
	readOriginal := func() []byte {
		t.Helper()
		reader, e := h.b.artStore.Open(original.Reference)
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(io.LimitReader(reader, 128))
		closeErr := reader.Close()
		if e != nil || closeErr != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).Match(data) {
			t.Fatal("remote file bytes were not truly archived")
		}
		return data
	}
	fileBefore := readOriginal()
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
		return nil, errors.New("mutable source must not execute")
	})
	pool := run.NewWorkerPool(runs, run.WithRunner(dagrun.New(loader, dagrun.WithStageExecutor(stageExec))), run.WithRetryRuntime(h.s), run.WithLogMasker(mask.NewMasker()))
	creator, dispatch := run.RetryAdapters(runs)
	if creator == nil || dispatch == nil {
		t.Fatal("real retry adapters unavailable")
	}
	runretry.WithChildCreator(creator)(h.s)
	runretry.WithDispatch(dispatch)(h.s)
	runretry.WithRejectedChild(run.RejectRetryQueued(runs))(h.s)
	h.b.artifactLister = runs.ListArtifacts
	child := ""
	pool.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pool.Stop(ctx)
		if child != "" {
			nativeLogLedger(t, h, child)
		}
	})
	observer.failing("consume", false)
	child = h.submit(t, "root", []string{consumer.Key}, nil)
	deadline := time.Now().Add(45 * time.Second)
	settled := false
	for time.Now().Before(deadline) {
		current, e := runs.Get(context.Background(), child)
		if e != nil {
			t.Fatal(e)
		}
		if run.IsTerminal(current.Status) {
			if current.Status != run.StatusSuccess {
				t.Fatal("real remote child execution failed; inspect safe node/ledger diagnostics")
			}
			drained, e := h.s.ResourcesDrained(context.Background(), child)
			if e != nil {
				t.Fatal(e)
			}
			var activities int
			if e = h.store.DB.QueryRow("SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", child).Scan(&activities); e != nil {
				t.Fatal(e)
			}
			if drained && activities == 0 {
				settled = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !settled {
		t.Fatal("remote child worker/resource/reader settlement did not complete")
	}
	nativeRemoteAssertDrained(t, h, observer, child)
	counts, _, diagnostics = observer.snapshot()
	if len(diagnostics) > 0 {
		t.Fatal(diagnostics)
	}
	if counts["produce"] != 1 || counts["consume"] != 2 || counts["after"] != 1 {
		t.Fatal("actual remote reexecution counts differ", counts)
	}
	if localDriver.count("produce") != 0 || localDriver.count("consume") != 0 || localDriver.count("after") != 0 {
		t.Fatal("local toolchain unexpectedly replaced remote adapter")
	}
	childContext, e := h.s.Context(context.Background(), child)
	if e != nil || childContext.CommitSHA != cloner.sha {
		t.Fatal("remote retry did not retain pinned source", e)
	}
	var producerAfter []byte
	if err = h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", producer.Key).Scan(&producerAfter); err != nil || !bytes.Equal(producerBefore, producerAfter) {
		t.Fatal("remote child rewrote original producer")
	}
	if !bytes.Equal(fileBefore, readOriginal()) {
		t.Fatal("remote child changed original CAS bytes")
	}
	var source, reference string
	if err = h.store.DB.QueryRow("SELECT run_id,reference FROM run_artifacts WHERE id=?", original.ID).Scan(&source, &reference); err != nil || source != "root" || reference != original.Reference {
		t.Fatal("remote original artifact source changed")
	}
	rows, err := h.store.DB.Query("SELECT text FROM run_logs WHERE run_id=?", child)
	if err != nil {
		t.Fatal(err)
	}
	masked := false
	rawSecret := regexp.MustCompile(`NR_RESTORED_SECRET=[a-f0-9]{48}`)
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		masked = masked || text == "NR_RESTORED_SECRET=[MASKED]"
		if rawSecret.MatchString(text) {
			_ = rows.Close()
			t.Fatal("remote restored dynamic secret leaked")
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || !masked {
		t.Fatal("remote restored env was not independently echoed and masked", err)
	}
	var exits int
	if err = h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_reader_exits WHERE attempt_id=?", child).Scan(&exits); err != nil || exits < 1 {
		t.Fatal("real child reader-exit evidence missing")
	}
}

func nativeRemoteAssertDrained(t *testing.T, h *retryHarness, p *nativeRemoteRunnerObserver, attempt string) {
	t.Helper()
	records, err := h.s.Resources(context.Background(), attempt)
	if err != nil {
		t.Fatal(err)
	}
	_, containers, diagnostics := p.snapshot()
	if len(diagnostics) > 0 {
		t.Fatal(diagnostics)
	}
	directories, tools := 0, 0
	for _, record := range records {
		ref := record.Ref
		if record.State != "drained" || ref.RootID != "root" || ref.AttemptID != attempt || ref.NodeKey == "" || ref.ServerID != p.serverID || ref.ConnectionFingerprint == "" || ref.RuntimeID == "" || ref.Owner == "" || ref.ProcessOwner == "" {
			t.Fatal("remote resource ownership/drain proof incomplete")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		absent, e := VerifyRemoteResource(ctx, p, ref)
		cancel()
		if e != nil || !absent {
			t.Fatal("remote precise resource not proven absent", e)
		}
		switch ref.Kind {
		case "remote_directory":
			directories++
			if !strings.HasPrefix(ref.Path, "/tmp/pipewright-remote/") || strings.Count(strings.TrimPrefix(ref.Path, "/tmp/pipewright-remote/"), "/") != 0 {
				t.Fatal("unexpected remote directory scope")
			}
			out, e := p.Service.Exec(context.Background(), p.serverID, []string{"sh", "-c", `[ ! -e "$1" ] && [ ! -L "$1" ]`, "native-exact-directory-probe", ref.Path})
			if e != nil || out == nil || out.ExitCode != 0 {
				t.Fatal("independent exact remote directory absence missing", e)
			}
		case "container":
			tools++
			actual, ok := containers[ref.Name]
			if !ok || actual.Owner != ref.Owner || actual.NodeKey != ref.NodeKey || actual.Name != ref.Name || len(actual.ID) != 64 {
				t.Fatal("actual Docker cidfile identity does not match persisted owner/node/name")
			}
			exact := ref
			exact.ID = actual.ID
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			absent, e := VerifyRemoteResource(ctx, p, exact)
			cancel()
			if e != nil || !absent {
				t.Fatal("independent full remote CID absence proof missing", e)
			}
		default:
			t.Fatal("unexpected resource kind in minimal remote fixture", ref.Kind)
		}
	}
	if directories < 1 || tools < 1 {
		t.Fatal("remote executor did not persist both directory and Docker resource evidence")
	}
}
