package target

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"sync"
	"testing"
)

func TestOutputBudgetCountsUnterminatedStreams(t *testing.T) {
	b := &outputBudget{remaining: 65536, maxLines: 1}
	_, _ = (budgetWriter{budget: b}).Write([]byte("one"))
	_, _ = (budgetWriter{budget: b, stderr: true}).Write([]byte("two"))
	result := b.result()
	if result.Stdout != "one" || result.Stderr != "" || !result.Truncated {
		t.Fatalf("%+v", result)
	}
}

type limitedCapture struct {
	capturingDialer
	calls int
}

func (d *limitedCapture) RunLimited(_ context.Context, addr string, cfg SSHConfig, cmd []string, _ ExecutionLimits) (*LimitedResult, error) {
	d.calls++
	d.gotAddr, d.gotCfg, d.gotCmd = addr, cfg, cmd
	return &LimitedResult{Stdout: "ok"}, nil
}

func TestExecLimitedFreezesAddressAndCredential(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	cred, err := v.Create(vault.CreateInput{Name: "password", Type: vault.TypeSSHPassword, Secret: "old-secret"})
	if err != nil {
		t.Fatal(err)
	}
	d := &limitedCapture{}
	svc := New(db, v, d)
	server, err := svc.Create(context.Background(), CreateInput{Name: "test", Host: "::1", User: "deploy", CredentialID: cred.ID})
	if err != nil {
		t.Fatal(err)
	}
	executor := svc.(LimitedExecutor)
	snapshot, err := executor.Capture(context.Background(), server.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.ExecLimited(context.Background(), snapshot, []string{"uname", "-s"}, ExecutionLimits{})
	if err != nil || d.calls != 1 || d.gotAddr != "[::1]:22" || d.gotCfg.Password != "old-secret" {
		t.Fatal(err, d.calls, d.gotAddr)
	}
	rotated := "new-secret"
	if _, err := v.Update(cred.ID, vault.UpdateInput{Secret: &rotated}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecLimited(context.Background(), snapshot, []string{"uname", "-s"}, ExecutionLimits{}); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
	if d.calls != 1 {
		t.Fatal("dispatched stale credential")
	}
	fresh, err := executor.Capture(context.Background(), server.ID)
	if err != nil {
		t.Fatal(err)
	}
	host := "other-host"
	if _, err := svc.Update(context.Background(), server.ID, UpdateInput{Host: &host}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecLimited(context.Background(), fresh, []string{"uname", "-s"}, ExecutionLimits{}); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
	if d.calls != 1 {
		t.Fatal("dispatched changed target")
	}
}

func TestOutputBudgetSharedBytesAndDrain(t *testing.T) {
	b := &outputBudget{remaining: 6, maxLines: 1000}
	out, err := (budgetWriter{budget: b}).Write([]byte("1234"))
	if out != 4 || err != nil {
		t.Fatal(out, err)
	}
	out, err = (budgetWriter{budget: b, stderr: true}).Write([]byte("abcdef"))
	if out != 6 || err != nil {
		t.Fatal(out, err)
	}
	result := b.result()
	if result.Stdout != "1234" || result.Stderr != "ab" || !result.Truncated {
		t.Fatalf("%+v", result)
	}
}

func TestOutputBudgetCombinedLines(t *testing.T) {
	b := &outputBudget{remaining: 65536, maxLines: 2}
	_, _ = (budgetWriter{budget: b}).Write([]byte("first\n"))
	_, _ = (budgetWriter{budget: b, stderr: true}).Write([]byte("second\nthird\n"))
	_, _ = (budgetWriter{budget: b}).Write([]byte("ignored"))
	res := b.result()
	if res.Stdout != "first\n" || res.Stderr != "second\n" || !res.Truncated {
		t.Fatalf("%+v", res)
	}
}

func TestOutputBudgetConcurrentAndUTF8(t *testing.T) {
	b := &outputBudget{remaining: 128, maxLines: 1000}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(stderr bool) {
			defer wg.Done()
			_, _ = (budgetWriter{budget: b, stderr: stderr}).Write([]byte(strings.Repeat("内容", 50)))
		}(i%2 == 0)
	}
	wg.Wait()
	result := b.result()
	if len(result.Stdout)+len(result.Stderr) > 128 || !result.Truncated {
		t.Fatalf("%+v", result)
	}
}

func TestExecutionLimitsRejectUnbounded(t *testing.T) {
	for _, l := range []ExecutionLimits{{Bytes: -1}, {Bytes: 65537}, {Lines: -1}, {Lines: 1001}} {
		if _, err := normalizeExecutionLimits(l); err == nil {
			t.Fatal(l)
		}
	}
	l, err := normalizeExecutionLimits(ExecutionLimits{})
	if err != nil || l.Bytes != 65536 || l.Lines != 1000 {
		t.Fatal(l, err)
	}
}

func TestConnectionSnapshotFingerprint(t *testing.T) {
	a := ConnectionSnapshot{Server: Server{ID: "s", Host: "127.0.0.1", Port: 22, User: "user", CredentialID: "c"}, CredentialVersion: "first"}
	if a.Fingerprint() != a.Fingerprint() {
		t.Fatal("unstable")
	}
	b := a
	b.CredentialVersion = "rotated"
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("rotation did not invalidate snapshot")
	}
	b = a
	b.Server.Host = "other"
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("host change did not invalidate snapshot")
	}
}
