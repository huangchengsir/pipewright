package deploy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

func localDockerTestConfig() map[string]string {
	return map[string]string{
		"localDocker": "true", "containerName": "blogback", "releaseBase": "/srv/blogback",
		"ports": "8082:8082", "localDockerPrepareCommand": "tar xf deployctx.tar",
		"localDockerRunArgs": `["--env","TZ=Asia/Shanghai","--volume","/docker/Blogback:/log","--restart","always"]`,
	}
}

func TestLocalDockerConfigRejectsIdentityOverridesAndAmbiguousModes(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"localDocker", "TRUE"}, {"restartCommand", "docker run other"}, {"containerName", " bad"},
		{"localDockerBuildContext", "../other"}, {"localDockerBuildContext", "https://repo.invalid/a"},
		{"localDockerDockerfile", "/tmp/Dockerfile"}, {"localDockerBuildArgs", `["--iidfile=/tmp/forged"]`},
		{"localDockerDockerfile", "../Dockerfile"}, {"localDockerDockerfile", "docker/../../Dockerfile"},
		{"localDockerBuildContext", "backend/../other"},
		{"localDockerBuildArgs", `null`}, {"localDockerRunArgs", `["--name","other"]`},
		{"localDockerRunArgs", `["--cidfile=/tmp/forged"]`}, {"localDockerRunArgs", `["--pull=always"]`},
		{"localDockerRunArgs", `["--rm"]`}, {"localDockerRunArgs", `["-d"]`},
		{"localDockerRunArgs", `["app:mutable"]`}, {"localDockerRunArgs", `["--env","OK=1","app:mutable"]`},
		{"localDockerRunArgs", `["--env"]`}, {"localDockerRunArgs", `["--platform=linux/arm64"]`},
		{"runArgs", "-e SECRET=do-not-echo"}, {"strategy", "blue_green"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			cfg := localDockerTestConfig()
			cfg[tc.key] = tc.value
			if err := ValidateLocalDockerConfig(run.Artifact{Type: run.ArtifactArchive}, cfg, "rolling"); !errors.Is(err, ErrInvalidLocalDockerConfig) {
				t.Fatal(err)
			}
		})
	}
	for _, kind := range []string{run.ArtifactImage, run.ArtifactJar, "command"} {
		if err := ValidateLocalDockerConfig(run.Artifact{Type: kind}, localDockerTestConfig(), ""); !errors.Is(err, ErrInvalidLocalDockerConfig) {
			t.Fatal(kind, err)
		}
	}
	if err := ValidateLocalDockerConfig(run.Artifact{Type: run.ArtifactArchive}, localDockerTestConfig(), StrategyBlueGreen); !errors.Is(err, ErrInvalidLocalDockerConfig) {
		t.Fatal(err)
	}
	for _, kind := range []string{run.ArtifactArchive, run.ArtifactDist} {
		for _, strategy := range []string{StrategyRolling, StrategyCanary, StrategyInteractive} {
			if err := ValidateLocalDockerConfig(run.Artifact{Type: kind}, localDockerTestConfig(), strategy); err != nil {
				t.Fatal(kind, strategy, err)
			}
		}
	}
	if err := ValidateLocalDockerConfig(run.Artifact{Type: run.ArtifactJar}, map[string]string{"restartCommand": "legacy script"}, StrategyBlueGreen); err != nil {
		t.Fatal("legacy behavior changed", err)
	}
}

func TestLocalDockerConfigFitsPinnedCommandArgumentBudget(t *testing.T) {
	for _, count := range []int{122, 123} {
		cfg := localDockerTestConfig()
		args := make([]string, count)
		for i := range args {
			args[i] = "KEY_" + strconv.Itoa(i) + "=value"
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		cfg["localDockerBuildArgs"] = string(raw)
		err = ValidateLocalDockerConfig(run.Artifact{Type: run.ArtifactArchive}, cfg, "")
		if count == 122 && err != nil || count == 123 && !errors.Is(err, ErrInvalidLocalDockerConfig) {
			t.Fatal(count, err)
		}
	}
	for _, size := range []int{30 << 10, (30 << 10) + 1} {
		cfg := localDockerTestConfig()
		cfg["localDockerPrepareCommand"] = strings.Repeat("#", size)
		err := ValidateLocalDockerConfig(run.Artifact{Type: run.ArtifactArchive}, cfg, "")
		if size == 30<<10 && err != nil || size > 30<<10 && !errors.Is(err, ErrInvalidLocalDockerConfig) {
			t.Fatal(size, err)
		}
	}
}

type localDockerFixture struct {
	db       *sql.DB
	svc      Service
	targets  *stubTarget
	ledger   *deployattempt.Store
	observer *imageObserverFixture
	in       DeployInput
	imageID  string
	builtID  string
	actualID string
	cid      string
	old      string
	phase    string
	log      strings.Builder
	pinned   *localDockerTestTarget
}

type localDockerTestTarget struct {
	*stubTarget
	truncate func([]string) bool
}

func (p localDockerTestTarget) Capture(ctx context.Context, id string) (target.ConnectionSnapshot, error) {
	srv, err := p.Get(ctx, id)
	if err != nil {
		return target.ConnectionSnapshot{}, err
	}
	return target.ConnectionSnapshot{Server: *srv, CredentialVersion: strings.Repeat("f", 64)}, nil
}

func (p localDockerTestTarget) ExecPinned(ctx context.Context, approved target.ConnectionSnapshot, cmd []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
	current, err := p.Capture(ctx, approved.Server.ID)
	if err != nil || current.Fingerprint() != approved.Fingerprint() {
		return nil, target.ErrTargetChanged
	}
	out, err := p.Exec(ctx, approved.Server.ID, cmd)
	if err != nil || out == nil {
		return nil, err
	}
	return &target.LimitedResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode, Truncated: p.truncate != nil && p.truncate(cmd)}, nil
}

func (p localDockerTestTarget) ExecLimited(ctx context.Context, approved target.ConnectionSnapshot, cmd []string, limits target.ExecutionLimits) (*target.LimitedResult, error) {
	return p.ExecPinned(ctx, approved, cmd, limits)
}

func (p localDockerTestTarget) UploadPinned(ctx context.Context, approved target.ConnectionSnapshot, content io.Reader, remotePath string) error {
	current, err := p.Capture(ctx, approved.Server.ID)
	if err != nil || current.Fingerprint() != approved.Fingerprint() {
		return target.ErrTargetChanged
	}
	return p.Upload(ctx, approved.Server.ID, content, remotePath)
}

func newLocalDockerFixture(t *testing.T, format string) *localDockerFixture {
	t.Helper()
	db := testDB(t)
	runs := run.New(db)
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store.Put(bytes.NewBufferString("real stored build context bytes"))
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "unused:placeholder")
	artifact, err := runs.AddArtifact(context.Background(), run.Artifact{RunID: runID, Type: run.ArtifactArchive, Name: "blogback", Reference: key, Metadata: map[string]any{"stored": true, "format": format, "filename": "deployctx.tar"}})
	if err != nil {
		t.Fatal(err)
	}
	artifactID := artifact.ID
	f := &localDockerFixture{db: db, targets: &stubTarget{}, ledger: deployattempt.New(db), observer: &imageObserverFixture{}, imageID: "sha256:" + strings.Repeat("a", 64), actualID: "sha256:" + strings.Repeat("a", 64), cid: strings.Repeat("b", 64), old: strings.Repeat("c", 64)}
	srv := seedServer(t, f.targets, "test-node")
	f.in = DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}, Config: localDockerTestConfig()}
	f.pinned = &localDockerTestTarget{stubTarget: f.targets}
	f.svc = New(f.pinned, runs, WithArtifactStore(store), WithAttemptLedger(f.ledger), WithImageObserver(f.observer))
	f.targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		if len(cmd) >= 7 && cmd[0] == "sh" && cmd[2] == uploadScript {
			return f.targets.uploadCommand(cmd), nil
		}
		out := &target.ExecResult{}
		phase := ""
		switch {
		case len(cmd) >= 3 && cmd[0] == "sh" && strings.Contains(cmd[2], f.in.Config["localDockerPrepareCommand"]) && cmd[2] != localDockerCWD:
			phase = "prepare"
		case len(cmd) > 5 && cmd[0] == "sh" && cmd[2] == localDockerCWD:
			phase = "build"
		case cmd[0] == "cat":
			phase, out.Stdout = "iid", f.imageID+"\n"
		case hasPrefixTokens(cmd, "docker", "image", "inspect"):
			phase = "image_inspect"
			image := cmd[len(cmd)-1]
			if f.builtID != "" && image == f.imageID {
				image = f.builtID
			}
			out.Stdout = `{"id":"` + image + `","os":"linux","architecture":"amd64","variant":null}`
		case hasPrefixTokens(cmd, "docker", "container", "inspect") && cmd[len(cmd)-1] == "blogback":
			phase = "previous"
			if f.old == "" {
				out.ExitCode, out.Stderr = 1, "Error response from daemon: No such container: blogback\n"
			} else {
				out.Stdout = `"` + f.old + `"`
			}
		case hasPrefixTokens(cmd, "docker", "container", "rm"):
			phase = "remove"
		case hasPrefixTokens(cmd, "docker", "run"):
			phase, out.Stdout = "start", f.cid+"\n"
			attempts, err := f.ledger.ListByRun(context.Background(), runID)
			if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 || attempts[0].Targets[0].Outcome != deployattempt.ActivationStarted {
				t.Fatalf("activation was not durably ordered before docker run: %+v %v", attempts, err)
			}
			if !deployattempt.ValidLocalExpected(attempts[0].Targets[0].ExpectedImage) {
				t.Fatal("frozen local identity missing before activation")
			}
		case hasPrefixTokens(cmd, "docker", "container", "inspect"):
			phase = "container_inspect"
			out.Stdout = `{"containerId":"` + f.cid + `","configId":"` + f.actualID + `","createdAt":"2026-10-24T00:00:00Z","startedAt":"2026-10-24T00:00:01Z"}`
		}
		if f.phase == phase && phase != "" {
			return nil, errors.New("transport failed with secret-do-not-log")
		}
		return out, nil
	}
	return f
}

func hasPrefixTokens(cmd []string, prefix ...string) bool {
	if len(cmd) < len(prefix) {
		return false
	}
	for i := range prefix {
		if cmd[i] != prefix[i] {
			return false
		}
	}
	return true
}

func (f *localDockerFixture) execute(t *testing.T) TargetResult {
	t.Helper()
	ctx := WithCmdLog(context.Background(), func(_, text string) { f.log.WriteString(text + "\n") })
	results, err := f.svc.Deploy(ctx, f.in)
	if err != nil || len(results) != 1 {
		t.Fatalf("Deploy: %+v %v", results, err)
	}
	return results[0]
}

func TestLocalDockerBuildFreezesConfigBeforeActivatingSameImage(t *testing.T) {
	for _, format := range []string{"file", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			f := newLocalDockerFixture(t, format)
			result := f.execute(t)
			if result.Status != run.TargetSuccess {
				t.Fatal(result)
			}
			attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
			if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 {
				t.Fatal(attempts, err)
			}
			tr := attempts[0].Targets[0]
			if tr.ExpectedImage.Digest != f.imageID || tr.ExpectedImage.IdentitySource != deployattempt.IdentityScriptPreActivation || tr.ExpectedImage.Repository != "" || tr.Outcome != deployattempt.Succeeded || len(tr.Receipts) != 3 || tr.Receipts[1].ActualImage.ContainerID != f.cid || tr.Receipts[1].ActualImage.ConfigID != f.imageID || tr.Receipts[1].ActualImage.Platform.Architecture != "amd64" {
				t.Fatalf("wrong receipt %+v", tr)
			}
			start := runCmd(f.targets.calls)
			if start[len(start)-1] != f.imageID || !hasCmd(f.targets.calls, "docker", "container", "rm", "-f", f.old) || !strings.Contains(strings.Join(start, " "), "--pull=never") || hasCmd(f.targets.calls, "docker", "pull") {
				t.Fatal(f.targets.calls)
			}
			if len(f.observer.events) != 3 || f.observer.events[1] != "before" || f.observer.events[2] != "after:"+f.cid {
				t.Fatal(f.observer.events)
			}
			if strings.Contains(f.log.String(), f.in.Config["localDockerPrepareCommand"]) || strings.Contains(f.log.String(), "TZ=Asia") {
				t.Fatal("script or env echoed", f.log.String())
			}
		})
	}
}

func TestLocalDockerBuildSelectsDockerfileInsideDeclaredContext(t *testing.T) {
	for _, tc := range []struct{ name, context, dockerfile, wantFile string }{
		{"default Dockerfile in nested context", "backend", "", "backend/Dockerfile"},
		{"custom Dockerfile inside nested context", "services/api", "docker/Dockerfile.production", "services/api/docker/Dockerfile.production"},
		{"context and file paths retain spaces", "backend service", "docker files/Dockerfile.production", "backend service/docker files/Dockerfile.production"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.in.Config["localDockerBuildContext"] = tc.context
			f.in.Config["localDockerDockerfile"] = tc.dockerfile
			original := f.targets.execFn
			selectedFile, selectedContext := "", ""
			// Both release root and the requested context may contain valid,
			// different Dockerfiles. A coherent receipt for the wrong root image
			// must not conceal that the configured build input was ignored.
			wrongRootImage := "sha256:" + strings.Repeat("e", 64)
			f.targets.execFn = func(server string, cmd []string) (*target.ExecResult, error) {
				if len(cmd) > 5 && cmd[0] == "sh" && cmd[2] == localDockerCWD {
					selectedContext = cmd[len(cmd)-1]
					for i := 5; i+1 < len(cmd); i++ {
						if cmd[i] == "-f" {
							selectedFile = cmd[i+1]
							break
						}
					}
					if selectedFile == tc.wantFile {
						f.builtID, f.actualID = f.imageID, f.imageID
					} else {
						f.builtID, f.actualID = wrongRootImage, wrongRootImage
					}
				}
				return original(server, cmd)
			}
			result := f.execute(t)
			if result.Status != run.TargetSuccess {
				t.Fatal(result)
			}
			attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
			if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 {
				t.Fatal(attempts, err)
			}
			if selectedFile != tc.wantFile || selectedContext != tc.context || attempts[0].Targets[0].ExpectedImage.Digest != f.imageID {
				t.Fatalf("selected Dockerfile=%q context=%q fixed image=%q; want file=%q context=%q image=%q", selectedFile, selectedContext, attempts[0].Targets[0].ExpectedImage.Digest, tc.wantFile, tc.context, f.imageID)
			}
		})
	}
}

func TestLocalDockerPreparationStopsBeforeScriptWhenWorkingDirectoryIsMissing(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		shell = shellForUploadTest(t)
	}
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing directory executes preparation there", true: "missing directory prevents script side effect"}[missing], func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.in.Config["localDockerPrepareCommand"] = `printf 'preparation-ran' > unexpected-marker`
			parent := t.TempDir()
			release := filepath.Join(parent, "release")
			if !missing {
				if err := os.Mkdir(release, 0700); err != nil {
					t.Fatal(err)
				}
			}
			original := f.targets.execFn
			preparationCalls := 0
			f.targets.execFn = func(server string, cmd []string) (*target.ExecResult, error) {
				if len(cmd) >= 5 && cmd[0] == "sh" && cmd[1] == "-c" && cmd[3] == "pw-local-docker" && cmd[2] != localDockerCWD {
					preparationCalls++
					// Execute the exact production shell program. Only the remote
					// directory argument is replaced by an isolated local fixture.
					command := exec.Command(shell, "-c", cmd[2], cmd[3], filepath.ToSlash(release))
					command.Dir = parent
					out, runErr := command.CombinedOutput()
					code := 0
					if runErr != nil {
						var exited *exec.ExitError
						if !errors.As(runErr, &exited) {
							t.Fatal(runErr)
						}
						code = exited.ExitCode()
					}
					return &target.ExecResult{ExitCode: code, Stderr: string(out)}, nil
				}
				return original(server, cmd)
			}
			result := f.execute(t)
			if preparationCalls != 1 {
				t.Fatal("preparation script was not exercised by real shell", preparationCalls)
			}
			if _, err := os.Stat(filepath.Join(parent, "unexpected-marker")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preparation ran outside the requested directory: %v", err)
			}
			if missing {
				if result.Status != run.TargetFailed || len(runCmd(f.targets.calls)) != 0 || hasCmd(f.targets.calls, "docker", "container", "rm") {
					t.Fatal("failed directory lookup continued into activation", result, f.targets.calls)
				}
			} else {
				data, err := os.ReadFile(filepath.Join(release, "unexpected-marker"))
				if result.Status != run.TargetSuccess || err != nil || string(data) != "preparation-ran" {
					t.Fatal("existing-directory preparation failed", result, err)
				}
			}
		})
	}
}

func TestLocalDockerPreparationFailuresPreserveOldContainer(t *testing.T) {
	for _, phase := range []string{"prepare", "build", "iid", "image_inspect", "previous"} {
		t.Run(phase, func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.phase = phase
			result := f.execute(t)
			if result.Status != run.TargetFailed || hasCmd(f.targets.calls, "docker", "container", "rm") || hasCmd(f.targets.calls, "docker", "run") {
				t.Fatal(result, f.targets.calls)
			}
			if strings.Contains(result.Message+f.log.String(), "secret-do-not-log") {
				t.Fatal("transport error leaked")
			}
		})
	}
}

func TestLocalDockerUnknownActivationNeverReplaysOrRollsBack(t *testing.T) {
	for _, phase := range []string{"remove", "start", "container_inspect"} {
		t.Run(phase, func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.phase = phase
			result := f.execute(t)
			attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
			if result.Status != run.TargetFailed || err != nil || attempts[0].Targets[0].Outcome != deployattempt.OutcomeUnknown {
				t.Fatal(result, attempts, err)
			}
			runs := 0
			for _, cmd := range f.targets.calls {
				if hasPrefixTokens(cmd, "docker", "run") {
					runs++
				}
			}
			if runs > 1 {
				t.Fatal("replayed activation", f.targets.calls)
			}
		})
	}
}

func TestLocalDockerMismatchRetainsActualReceiptAndFails(t *testing.T) {
	f := newLocalDockerFixture(t, "file")
	f.actualID = "sha256:" + strings.Repeat("d", 64)
	result := f.execute(t)
	attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
	if result.Status != run.TargetFailed || err != nil {
		t.Fatal(result, err)
	}
	tr := attempts[0].Targets[0]
	if tr.Outcome != deployattempt.DeployFailed || tr.ExpectedImage.Digest == f.actualID || tr.Receipts[1].ActualImage.ConfigID != f.actualID || tr.Receipts[1].ActualImage.Platform.OS != "linux" || tr.Receipts[2].Reason != "image_config_id_mismatch" {
		t.Fatal(tr)
	}
}

func TestLocalDockerBuildResultIsNotPresumedToBeConfigID(t *testing.T) {
	f := newLocalDockerFixture(t, "file")
	f.imageID = "sha256:" + strings.Repeat("e", 64) // immutable build result
	f.builtID = "sha256:" + strings.Repeat("a", 64) // independently inspected .Id
	if result := f.execute(t); result.Status != run.TargetSuccess {
		t.Fatal(result)
	}
	attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
	if err != nil || attempts[0].Targets[0].ExpectedImage.Digest != f.builtID || runCmd(f.targets.calls)[len(runCmd(f.targets.calls))-1] != f.builtID {
		t.Fatal(attempts, err, f.targets.calls)
	}
}

func TestLocalDockerStageAndCanaryKeepExplicitLocalDeploymentPath(t *testing.T) {
	for _, strategy := range []string{StrategyRolling, StrategyCanary} {
		t.Run(strategy, func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.in.Config["artifactType"] = run.ArtifactArchive
			results, err := f.svc.DeployForStage(context.Background(), f.in.RunID, f.in.ServerIDs, f.in.Config, strategy)
			if err != nil || len(results) != 1 || results[0].Status != run.TargetSuccess {
				t.Fatal(results, err)
			}
			start := runCmd(f.targets.calls)
			if start[len(start)-1] != f.imageID {
				t.Fatal("stage did not use frozen local image", start)
			}
		})
	}
}

func TestLocalDockerHealthFailureDoesNotReplayOrLeakHealthSecrets(t *testing.T) {
	f := newLocalDockerFixture(t, "file")
	f.in.HealthCheck = &HealthCheck{Type: HealthCheckCommand, Command: []string{"health-check", "do-not-log-health-token"}, Retries: 1}
	original := f.targets.execFn
	f.targets.execFn = func(id string, cmd []string) (*target.ExecResult, error) {
		if cmd[0] == "health-check" {
			return &target.ExecResult{ExitCode: 1, Stdout: "do-not-log-health-output", Stderr: "do-not-log-health-stderr"}, nil
		}
		return original(id, cmd)
	}
	result := f.execute(t)
	attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
	if result.Status != run.TargetFailed || err != nil || attempts[0].Targets[0].Outcome != deployattempt.DeployFailed || attempts[0].Targets[0].Receipts[2].HealthGate != "failed" {
		t.Fatal(result, attempts, err)
	}
	if strings.Contains(result.Message+f.log.String(), "do-not-log-health") {
		t.Fatal("health credentials or raw output leaked", result.Message, f.log.String())
	}
	starts := 0
	for _, cmd := range f.targets.calls {
		if hasPrefixTokens(cmd, "docker", "run") {
			starts++
		}
	}
	if starts != 1 {
		t.Fatal("health failure replayed or rolled back deployment", starts)
	}
}

func TestLocalDockerLedgerFailuresNeverReplayActivation(t *testing.T) {
	for _, phase := range []string{"begin", "target", deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		t.Run(phase, func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.svc.(*service).attempts = failingAttemptWriter{Writer: f.ledger, fail: phase}
			if phase == "begin" {
				_, err := f.svc.Deploy(context.Background(), f.in)
				if err == nil || len(f.targets.calls) != 0 {
					t.Fatal(err, f.targets.calls)
				}
				return
			}
			if result := f.execute(t); result.Status != run.TargetFailed {
				t.Fatal(result)
			}
			starts := 0
			for _, cmd := range f.targets.calls {
				if hasPrefixTokens(cmd, "docker", "run") {
					starts++
				}
			}
			if starts > 1 || (phase == "target" || phase == deployattempt.ActivationStarted) && starts != 0 {
				t.Fatal("activation replayed or started before persistence", f.targets.calls)
			}
		})
	}
}

func TestLocalDockerPinnedConnectionChangeStopsBeforeRemovingOldContainer(t *testing.T) {
	f := newLocalDockerFixture(t, "file")
	original := f.targets.execFn
	f.targets.execFn = func(id string, cmd []string) (*target.ExecResult, error) {
		out, err := original(id, cmd)
		if hasPrefixTokens(cmd, "docker", "image", "inspect") {
			f.targets.servers[id].Host = "other-host.invalid"
		}
		return out, err
	}
	if result := f.execute(t); result.Status != run.TargetFailed || hasCmd(f.targets.calls, "docker", "container", "rm") || hasCmd(f.targets.calls, "docker", "run") {
		t.Fatal(result, f.targets.calls)
	}
	attempts, err := f.ledger.ListByRun(context.Background(), f.in.RunID)
	if err != nil || attempts[0].Targets[0].ConnectionFingerprint == "" {
		t.Fatal(attempts, err)
	}
}

func TestLocalDockerBuildOutputMayBeDrainedButIdentityCannotBeTruncated(t *testing.T) {
	for _, phase := range []string{"build", "identity"} {
		t.Run(phase, func(t *testing.T) {
			f := newLocalDockerFixture(t, "file")
			f.pinned.truncate = func(cmd []string) bool {
				if phase == "build" {
					return len(cmd) > 3 && cmd[0] == "sh" && cmd[2] == localDockerCWD
				}
				return cmd[0] == "cat"
			}
			result := f.execute(t)
			if phase == "build" && result.Status != run.TargetSuccess || phase == "identity" && (result.Status != run.TargetFailed || hasCmd(f.targets.calls, "docker", "run")) {
				t.Fatal(result, f.targets.calls)
			}
		})
	}
}

func TestLocalDockerMissingContainerIsFirstDeployOnlyWithExactError(t *testing.T) {
	f := newLocalDockerFixture(t, "file")
	f.old = ""
	if result := f.execute(t); result.Status != run.TargetSuccess || hasCmd(f.targets.calls, "docker", "container", "rm") {
		t.Fatal(result, f.targets.calls)
	}
}

func TestLocalDockerRejectsUnstoredArtifactBeforeAttemptOrSSH(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	srv := seedServer(t, targets, "test")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactArchive, "placeholder")
	ledger := deployattempt.New(db)
	svc := New(targets, runs, WithAttemptLedger(ledger))
	_, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}, Config: localDockerTestConfig()})
	if !errors.Is(err, ErrInvalidLocalDockerConfig) || len(targets.calls) != 0 {
		t.Fatal(err, targets.calls)
	}
	attempts, err := ledger.ListByRun(context.Background(), runID)
	if err != nil || len(attempts) != 0 {
		t.Fatal(attempts, err)
	}
}

func TestLocalDockerObservationResolvesOnlyExplicitPendingStoredIdentity(t *testing.T) {
	a := run.Artifact{Type: run.ArtifactArchive, Metadata: map[string]any{"stored": true, "format": "file"}}
	name, expected, err := ObservationImageTarget(a, localDockerTestConfig())
	if err != nil || name != "blogback" || expected.Digest != "" || expected.Reference != "" || expected.Repository != "" || expected.IdentitySource != deployattempt.IdentityScriptPreActivation {
		t.Fatal(name, expected, err)
	}
	for _, text := range []string{`{"id":"a","id":"b"}`, `{"id":"a"} {}`, strings.Repeat("a", 2049)} {
		if decodeLocalProjection(text, new(map[string]json.RawMessage)) {
			t.Fatal("ambiguous projection accepted", text)
		}
	}
}
