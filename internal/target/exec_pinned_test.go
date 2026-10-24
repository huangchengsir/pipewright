package target

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/vault"
)

type pinnedCapture struct {
	limitedCapture
	uploadCalls int
	remaining   time.Duration
}

func (d *pinnedCapture) RunLimited(ctx context.Context, addr string, cfg SSHConfig, cmd []string, l ExecutionLimits) (*LimitedResult, error) {
	deadline, _ := ctx.Deadline()
	d.remaining = time.Until(deadline)
	return d.limitedCapture.RunLimited(ctx, addr, cfg, cmd, l)
}
func (d *pinnedCapture) RunWithStdin(ctx context.Context, addr string, cfg SSHConfig, cmd []string, in io.Reader) (*ExecResult, error) {
	d.uploadCalls++
	return d.capturingDialer.RunWithStdin(ctx, addr, cfg, cmd, in)
}

func (d *pinnedCapture) RunLimitedWithStdin(ctx context.Context, addr string, cfg SSHConfig, cmd []string, in io.Reader, limits ExecutionLimits) (*LimitedResult, error) {
	result, err := d.RunWithStdin(ctx, addr, cfg, cmd, in)
	if result == nil {
		return nil, err
	}
	return &LimitedResult{Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode}, err
}

func TestPinnedUploadTransportDrainsExcessOutputWithinBudget(t *testing.T) {
	addr, reached, serverDone := stalledLimitedSSH(t, "output", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result *LimitedResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := (sshDialer{}).RunLimitedWithStdin(ctx, addr, SSHConfig{User: "fixture", Password: "fixture-only"}, []string{"fixture-upload"}, strings.NewReader("artifact"), ExecutionLimits{Bytes: 1024, Lines: 20})
		done <- outcome{result, err}
	}()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("excess output did not drain")
	}
	cancel()
	select {
	case out := <-done:
		if out.result == nil || !out.result.Truncated || len(out.result.Stdout)+len(out.result.Stderr) > 1024 || !errors.Is(out.err, context.Canceled) {
			t.Fatal(out.result, out.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled upload did not return")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("upload transport did not close")
	}
}
func TestPinnedBuildDeadlineAndUploadFreezeConnection(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	c, err := v.Create(vault.CreateInput{Name: "pinned", Type: vault.TypeSSHPassword, Secret: leakMarker})
	if err != nil {
		t.Fatal(err)
	}
	d := &pinnedCapture{}
	d.res = &ExecResult{}
	s := New(db, v, d)
	srv, err := s.Create(ctx, CreateInput{Name: "pinned", Host: "::1", User: "deploy", CredentialID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	p := s.(PinnedExecutor)
	snap, err := p.Capture(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	buildctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err = p.ExecPinned(buildctx, snap, []string{"docker", "build", "."}, ExecutionLimits{}); err != nil {
		t.Fatal(err)
	}
	if d.remaining < time.Minute || d.remaining > 2*time.Minute || d.gotAddr != "[::1]:22" {
		t.Fatal(d.remaining, d.gotAddr)
	}
	if err = p.UploadPinned(ctx, snap, strings.NewReader("artifact"), "/srv/app/file.tar"); err != nil {
		t.Fatal(err)
	}
	if d.uploadCalls != 1 || string(d.gotStdin) != "artifact" {
		t.Fatal(d.uploadCalls)
	}
	rotated := "replacement-secret"
	if _, err = v.Update(c.ID, vault.UpdateInput{Secret: &rotated}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ExecPinned(ctx, snap, []string{"docker", "run"}, ExecutionLimits{}); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
	if err = p.UploadPinned(ctx, snap, strings.NewReader("other"), "/srv/app/file.tar"); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
	if d.calls != 1 || d.uploadCalls != 1 {
		t.Fatal("stale identity dispatched")
	}
	fresh, err := p.Capture(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	host := "changed.example"
	if _, err = s.Update(ctx, srv.ID, UpdateInput{Host: &host}); err != nil {
		t.Fatal(err)
	}
	if err = p.UploadPinned(ctx, fresh, strings.NewReader("other"), "/srv/app/file.tar"); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
}
func TestPinnedUploadFailureDoesNotLeakStderr(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	id := newSSHCred(t, v, leakMarker)
	d := &pinnedCapture{}
	d.res = &ExecResult{ExitCode: 1, Stderr: leakMarker}
	s := New(db, v, d)
	srv, err := s.Create(ctx, CreateInput{Name: "pinned", Host: "127.0.0.1", User: "deploy", CredentialID: id})
	if err != nil {
		t.Fatal(err)
	}
	p := s.(PinnedExecutor)
	snap, err := p.Capture(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = p.UploadPinned(ctx, snap, strings.NewReader("artifact"), "/srv/file")
	if err == nil || strings.Contains(err.Error(), leakMarker) {
		t.Fatal(err)
	}
	if err = p.UploadPinned(ctx, snap, nil, "/srv/file"); err == nil {
		t.Fatal("nil payload accepted")
	}
	if err = p.UploadPinned(ctx, snap, strings.NewReader("x"), "/srv/file\nother"); err == nil {
		t.Fatal("invalid path accepted")
	}
}
