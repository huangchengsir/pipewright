package build

// A real loopback SSH transport executes the product's actual argv on this
// isolated Linux test node. It never connects to business SSH or host accounts.
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nativeSSHFixture(t *testing.T) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	rawSecret := make([]byte, 24)
	if _, err = rand.Read(rawSecret); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(rawSecret)
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, raw []byte) (*ssh.Permissions, error) {
		if c.User() != "fixture" || string(raw) != password {
			return nil, errors.New("isolated fixture authentication rejected")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var wg sync.WaitGroup
	stop, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[raw] = true
			mu.Unlock()
			wg.Add(1)
			go func(raw net.Conn) {
				defer wg.Done()
				defer raw.Close()
				defer func() { mu.Lock(); delete(connections, raw); mu.Unlock() }()
				conn, channels, requests, err := ssh.NewServerConn(raw, config)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					if channel.ChannelType() != "session" {
						_ = channel.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					stream, requests, err := channel.Accept()
					if err != nil {
						continue
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer stream.Close()
						for request := range requests {
							if request.Type != "exec" {
								_ = request.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if ssh.Unmarshal(request.Payload, &payload) != nil || len(payload.Command) > 128<<10 {
								_ = request.Reply(false, nil)
								return
							}
							_ = request.Reply(true, nil)
							ctx, done := context.WithTimeout(stop, 60*time.Second)
							cmd := exec.CommandContext(ctx, "/bin/sh", "-c", payload.Command)
							cmd.Dir = dir
							// The local Docker fixture uses an already inspected full local
							// base ID. The legacy backend avoids a BuildKit frontend/base
							// metadata pull; if unavailable this enabled gate must fail.
							cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "LANG=C.UTF-8", "DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONFIG=" + os.Getenv("DOCKER_CONFIG"), "DOCKER_BUILDKIT=0"}
							cmd.Stdin = stream
							cmd.Stdout = stream
							cmd.Stderr = stream.Stderr()
							err := cmd.Run()
							done()
							code := uint32(0)
							if err != nil {
								code = 1
								var ee *exec.ExitError
								if errors.As(err, &ee) && ee.ExitCode() >= 0 {
									code = uint32(ee.ExitCode())
								}
							}
							if strings.Contains(payload.Command, "NR_LOST_RECEIPT") {
								_ = conn.Close()
								return
							}
							_, _ = stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
							return
						}
					}()
				}
			}(raw)
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		mu.Lock()
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().(*net.TCPAddr).Port, password, dir
}
func nativeInstallDeployer(t *testing.T, h *retryHarness) (target.Service, run.Service) {
	t.Helper()
	var key [32]byte
	key[0] = 17
	v := vault.New(h.store.DB, &key)
	targets := target.New(h.store.DB, v, nil)
	runs := run.New(h.store.DB)
	_ = run.NewWorkerPool(runs, run.WithRetryRuntime(h.s))
	h.b.deployer = deploy.New(targets, runs, deploy.WithBatchDB(h.store.DB), deploy.WithArtifactStore(h.b.artStore), deploy.WithRetryLedger(h.s), deploy.WithAttemptLedger(deployattempt.New(h.store.DB)))
	h.executor = func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
		return NewStageExecutor(h.b, nil)(ctx, r, stage, &nativePersistReporter{StageReporter: rep, runs: runs, id: r.ID})
	}
	return targets, runs
}

type nativePersistReporter struct {
	dagrun.StageReporter
	runs run.Service
	id   string
}

func (r *nativePersistReporter) EmitArtifact(ctx context.Context, a run.Artifact) error {
	a.RunID = r.id
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	if node, ok := runretry.NodeFrom(ctx); ok {
		a.Metadata["logicalNodeKey"] = node.Key
	}
	if _, err := r.runs.AddArtifact(ctx, a); err != nil {
		return err
	}
	return r.StageReporter.EmitArtifact(ctx, a)
}
func (r *nativePersistReporter) JobReporter(id string) dagrun.StageReporter {
	return &nativePersistReporter{StageReporter: r.StageReporter.JobReporter(id), runs: r.runs, id: r.id}
}
func nativeLineCount(t *testing.T, p string) int {
	t.Helper()
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}
func nativeWriteScript(t *testing.T, dir, name string, fail bool) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := "#!/bin/sh\nprintf 'executed\\n' >> '" + p + ".count'\n"
	if fail {
		body += "if [ -f '" + p + ".fail' ]; then exit 23; fi\n"
	}
	if err := os.WriteFile(p, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func nativeAddServer(t *testing.T, h *retryHarness, service target.Service, id string, port int, password string) {
	t.Helper()
	var key [32]byte
	key[0] = 17
	v := vault.New(h.store.DB, &key)
	cred, err := v.Create(vault.CreateInput{Name: "native-" + id, Type: vault.TypeSSHPassword, Secret: password})
	if err != nil {
		t.Fatal(err)
	}
	server, err := service.Create(context.Background(), target.CreateInput{Name: id, Host: "127.0.0.1", Port: port, User: "fixture", CredentialID: cred.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.DB.Exec("UPDATE servers SET id=? WHERE id=?", id, server.ID); err != nil {
		t.Fatal(err)
	}
}
func TestNativeRecoveryActualSSHPartialAndNotify(t *testing.T) {
	image := nativeRetryReady(t)
	portA, passwordA, dirA := nativeSSHFixture(t)
	portB, passwordB, dirB := nativeSSHFixture(t)
	a := nativeWriteScript(t, dirA, "deploy.sh", false)
	b := nativeWriteScript(t, dirB, "deploy.sh", true)
	if err := os.WriteFile(b+".fail", []byte("test-condition"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "build", Name: "build", Kind: "build", Jobs: []pipeline.Job{nativeJob(image, "build", "true")}},
		{ID: "deploy", Name: "deploy", Kind: "deploy", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "deploy", Name: "deploy", Type: "deploy_ssh", Config: map[string]any{"artifactType": "command", "restartCommand": "./deploy.sh"}}}},
		{ID: "notify", Name: "notify", Kind: "notify", Needs: []string{"deploy"}, Jobs: []pipeline.Job{{ID: "notify", Name: "notify", Type: "notify", Config: map[string]any{"channel": "native-channel"}}}},
		{ID: "after", Name: "after", Kind: "build", Needs: []string{"notify"}, Jobs: []pipeline.Job{nativeJob(image, "after", "true")}},
	}}
	h, d, _ := nativeHarness(t, spec, func(s *runretry.Snapshot) {
		s.Settings["environments"] = []map[string]any{{"name": "test", "targetServerIds": []string{"native-a", "native-b"}}}
	})
	targets, _ := nativeInstallDeployer(t, h)
	nativeAddServer(t, h, targets, "native-a", portA, passwordA)
	nativeAddServer(t, h, targets, "native-b", portB, passwordB)
	var delivered atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
		w.WriteHeader(204)
	}))
	defer server.Close()
	h.b.notifier = notify.New(h.store.DB, nil, server.Client())
	if _, err := h.b.notifier.Create(context.Background(), notify.CreateInput{Name: "native-channel", Type: notify.TypeWebhook, Enabled: true, Config: notify.Config{URL: server.URL}}); err != nil {
		t.Fatal(err)
	}
	nativeExecute(t, h, "root", true)
	if nativeLineCount(t, a+".count") != 1 || nativeLineCount(t, b+".count") != 1 || delivered.Load() != 0 {
		t.Fatal("initial physical effects incorrect")
	}
	var original []byte
	if err := h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_external_targets WHERE attempt_id='root' AND server_id='native-a'").Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b + ".fail"); err != nil {
		t.Fatal(err)
	}
	first := h.submit(t, "root", []string{h.node(t, "root", "deploy", "deploy").Key}, nil)
	nativeExecute(t, h, first, false)
	if nativeLineCount(t, a+".count") != 1 || nativeLineCount(t, b+".count") != 2 || delivered.Load() != 1 {
		t.Fatal("successful target repeated or failed target not retried")
	}
	nativeAssertCounts(t, d, map[string]int{"build": 1, "after": 1})
	var unchanged []byte
	if err := h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_external_targets WHERE attempt_id='root' AND server_id='native-a'").Scan(&unchanged); err != nil || string(unchanged) != string(original) {
		t.Fatal("parent target evidence changed", err)
	}
	t.Log("real authenticated SSH: success target once, failed target twice; actual successful webhook and downstream continue")
}
func TestNativeRecoveryActualNotifyUnknownIsNeverRedispatched(t *testing.T) {
	image := nativeRetryReady(t)
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	spec := pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "build", Name: "build", Kind: "build", Jobs: []pipeline.Job{nativeJob(image, "build", "true")}},
		{ID: "notify", Name: "notify", Kind: "notify", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "notify", Name: "notify", Type: "notify", Config: map[string]any{"channel": "native-rejected"}}}},
		{ID: "after", Name: "after", Kind: "build", Needs: []string{"notify"}, Jobs: []pipeline.Job{nativeJob(image, "after", "true")}},
	}}
	h, d, _ := nativeHarness(t, spec)
	h.b.notifier = notify.New(h.store.DB, nil, server.Client())
	if _, err := h.b.notifier.Create(context.Background(), notify.CreateInput{Name: "native-rejected", Type: notify.TypeWebhook, Enabled: true, Config: notify.Config{URL: server.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := h.execute(t, "root"); err != nil {
		t.Fatal("best-effort notification changed business result", err)
	}
	if !errors.Is(h.settleErr, runretry.ErrUnknown) {
		t.Fatal("unknown actual delivery was released", h.settleErr)
	}
	if received.Load() != 1 {
		t.Fatalf("actual rejected request count=%d", received.Load())
	}
	nativeAssertCounts(t, d, map[string]int{"build": 1, "after": 1})
	plan, err := h.s.Plan(context.Background(), "root", []string{h.node(t, "root", "notify", "notify").Key})
	if err != nil || plan.CanSubmit {
		t.Fatalf("503 delivery blindly replayable: %v %+v", err, plan)
	}
	if received.Load() != 1 {
		t.Fatal("read-only plan repeated actual request")
	}
	t.Log("real HTTP503 remains outcome_unknown, original best-effort business chain continues, read-only plan never resends")
}
func TestNativeRecoveryActualSSHFileDeployment(t *testing.T) {
	image := nativeRetryReady(t)
	for _, kind := range []string{"deploy_ssh", "deploy_frontend"} {
		t.Run(kind, func(t *testing.T) {
			port, password, dir := nativeSSHFixture(t)
			script := nativeWriteScript(t, dir, "deploy.sh", true)
			if err := os.WriteFile(script+".fail", []byte("test-condition"), 0600); err != nil {
				t.Fatal(err)
			}
			a := nativeJob(image, "file", "mkdir -p dist\nprintf exact-native-bytes > dist/index.html")
			a.Config["artifacts"] = `[{"path":"dist","name":"dist"}]`
			base := filepath.Join(dir, "application")
			spec := pipeline.Spec{Stages: []pipeline.Stage{
				{ID: "build", Name: "build", Kind: "build", Jobs: []pipeline.Job{a}},
				{ID: "deploy", Name: "deploy", Kind: "deploy", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "deploy", Name: "deploy", Type: kind, Config: map[string]any{"serverId": "native-a", "artifactType": "dist", "deployPath": base, "restartCommand": nativeShellQuote(script)}}}},
				{ID: "after", Name: "after", Kind: "build", Needs: []string{"deploy"}, Jobs: []pipeline.Job{nativeJob(image, "after", "true")}},
			}}
			h, d, _ := nativeHarness(t, spec)
			targets, _ := nativeInstallDeployer(t, h)
			nativeAddServer(t, h, targets, "native-a", port, password)
			nativeExecute(t, h, "root", true)
			if err := os.Remove(script + ".fail"); err != nil {
				t.Fatal(err)
			}
			child := h.submit(t, "root", []string{h.node(t, "root", "deploy", "deploy").Key}, nil)
			nativeExecute(t, h, child, false)
			got, err := os.ReadFile(filepath.Join(base, "current", "index.html"))
			if err != nil || string(got) != "exact-native-bytes" {
				t.Fatal("real uploaded archive bytes differ", err)
			}
			nativeAssertCounts(t, d, map[string]int{"file": 1, "after": 1})
			t.Log("real SSH upload/hash/archive/release switch; original file producer once, failed deployment resumes")
		})
	}
}
func nativeShellQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'"
}
func TestNativeRecoveryActualSSHLostReceipt(t *testing.T) {
	nativeRetryReady(t)
	port, password, dir := nativeSSHFixture(t)
	script := nativeWriteScript(t, dir, "NR_LOST_RECEIPT.sh", false)
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Name: "deploy", Kind: "deploy", Jobs: []pipeline.Job{{ID: "deploy", Name: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverId": "native-a", "artifactType": "command", "restartCommand": "./NR_LOST_RECEIPT.sh"}}}}}}
	h, _, _ := nativeHarness(t, spec)
	targets, _ := nativeInstallDeployer(t, h)
	nativeAddServer(t, h, targets, "native-a", port, password)
	defer nativeLogLedger(t, h, "root")
	if err := h.execute(t, "root", true); err == nil {
		t.Fatal("lost actual SSH receipt reported success")
	}
	if !errors.Is(h.settleErr, runretry.ErrBlocked) && !errors.Is(h.settleErr, runretry.ErrUnknown) {
		t.Fatal("lost receipt did not retain settlement guard", h.settleErr)
	}
	if nativeLineCount(t, script+".count") != 1 {
		t.Fatal("physical command did not execute once")
	}
	plan, err := h.s.Plan(context.Background(), "root", []string{h.node(t, "root", "deploy", "deploy").Key})
	if err != nil || plan.CanSubmit {
		t.Fatalf("lost receipt blindly replayable: %v %+v", err, plan)
	}
	if nativeLineCount(t, script+".count") != 1 {
		t.Fatal("read-only plan redispatched")
	}
	t.Log("actual remote process exited and SSH completion acknowledgement lost; recovery safely blocked without replay")
}

// This probe isolates target/activity settlement from all Docker workspaces.
// Root can select it alone if a physical fanout/deploy case remains blocked.
func TestNativeRecoveryActualSSHCommandSettlement(t *testing.T) {
	nativeRetryReady(t)
	port, password, dir := nativeSSHFixture(t)
	script := nativeWriteScript(t, dir, "settle.sh", true)
	if err := os.WriteFile(script+".fail", []byte("current-fixture-condition"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Name: "deploy", Kind: "deploy", Jobs: []pipeline.Job{{ID: "deploy", Name: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverId": "native-a", "artifactType": "command", "restartCommand": "./settle.sh"}}}}}}
	h, _, _ := nativeHarness(t, spec)
	targets, _ := nativeInstallDeployer(t, h)
	nativeAddServer(t, h, targets, "native-a", port, password)
	nativeExecute(t, h, "root", true)
	if nativeLineCount(t, script+".count") != 1 {
		t.Fatal("initial actual command did not execute once")
	}
	if err := os.Remove(script + ".fail"); err != nil {
		t.Fatal(err)
	}
	child := h.submit(t, "root", []string{h.node(t, "root", "deploy", "deploy").Key}, nil)
	nativeExecute(t, h, child, false)
	if nativeLineCount(t, script+".count") != 2 {
		t.Fatal("actual known failed command did not resume exactly once")
	}
	var resources, unknown, held int
	if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_resources").Scan(&resources); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_external_targets WHERE state IN ('running','outcome_unknown')").Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM project_activity WHERE released_at IS NULL").Scan(&held); err != nil {
		t.Fatal(err)
	}
	if resources != 0 || unknown != 0 || held != 0 {
		t.Fatal("target-only probe did not settle with durable proofs", resources, unknown, held)
	}
}
