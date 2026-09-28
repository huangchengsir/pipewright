package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

func shellForUploadTest(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("bash"); err == nil {
		return shell
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(`C:\Program Files\Git\bin\bash.exe`); err == nil {
			return `C:\Program Files\Git\bin\bash.exe`
		}
	}
	t.Skip("bash is needed to run the remote POSIX upload script")
	return ""
}

type interruptedUploadTarget struct {
	*stubTarget
	failSecond       bool
	chunkZeroUploads int
	chunkOneUploads  int
}

func (s *interruptedUploadTarget) Upload(ctx context.Context, serverID string, content io.Reader, remotePath string) error {
	if strings.HasSuffix(remotePath, "-0.tmp") {
		s.chunkZeroUploads++
	}
	if strings.HasSuffix(remotePath, "-1.tmp") {
		s.chunkOneUploads++
		if s.failSecond {
			return target.ErrUnreachable
		}
	}
	return s.stubTarget.Upload(ctx, serverID, content, remotePath)
}

func TestUploadArtifactResumesOnlyMissingChunk(t *testing.T) {
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("reliable-upload"), (uploadChunkSize+1024)/len("reliable-upload")+1)
	key, _, err := store.Put(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	remote := &interruptedUploadTarget{stubTarget: &stubTarget{}, failSecond: true}
	svc := New(remote, nil, WithArtifactStore(store)).(*service)
	a := run.Artifact{Reference: key}
	ctx, err := withUploadPolicy(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.uploadArtifact(ctx, "test-server", a, "/srv/app", "/srv/app/releases/new.jar"); err == nil {
		t.Fatal("interrupted second chunk unexpectedly succeeded")
	}
	if remote.chunkZeroUploads != 1 || remote.chunkOneUploads != 3 {
		t.Fatalf("first attempt uploads: chunk0=%d chunk1=%d", remote.chunkZeroUploads, remote.chunkOneUploads)
	}
	remote.failSecond = false
	if err := svc.uploadArtifact(ctx, "test-server", a, "/srv/app", "/srv/app/releases/new.jar"); err != nil {
		t.Fatalf("resumed upload: %v", err)
	}
	if remote.chunkZeroUploads != 1 || remote.chunkOneUploads != 4 {
		t.Fatalf("resume retransmitted verified data: chunk0=%d chunk1=%d", remote.chunkZeroUploads, remote.chunkOneUploads)
	}
	if !bytes.Equal(remote.uploads["/srv/app/releases/new.jar"], payload) {
		t.Fatal("final assembled bytes differ")
	}
}

type delayedUploadTarget struct{ *stubTarget }

func (s *delayedUploadTarget) Upload(ctx context.Context, serverID string, content io.Reader, remotePath string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1200 * time.Millisecond):
	}
	return s.stubTarget.Upload(ctx, serverID, content, remotePath)
}

func TestUploadArtifactOutlivesCommandBudget(t *testing.T) {
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store.Put(bytes.NewReader([]byte("slow-but-moving")))
	if err != nil {
		t.Fatal(err)
	}
	remote := &delayedUploadTarget{stubTarget: &stubTarget{}}
	svc := New(remote, nil, WithArtifactStore(store)).(*service)
	ctx, err := withUploadPolicy(context.Background(), map[string]string{"commandTimeoutSeconds": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.uploadArtifact(ctx, "test-server", run.Artifact{Reference: key}, "/srv/app", "/srv/app/releases/slow.jar"); err != nil {
		t.Fatalf("upload inherited 1-second command timeout: %v", err)
	}
}

func uploadScriptResult(t *testing.T, shell, action, dir, owner string, args ...string) int {
	t.Helper()
	cmdArgs := append([]string{"-c", uploadScript, "pw-upload", action, dir, owner}, args...)
	cmd := exec.Command(shell, cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		t.Logf("remote script %s exit %d: %s", action, exit.ExitCode(), out)
		return exit.ExitCode()
	}
	t.Fatalf("remote script %s could not start: %v (%s)", action, err, out)
	return -1
}

func TestUploadScriptResumeAndValidate(t *testing.T) {
	shell := shellForUploadTest(t)
	baseDir, err := os.MkdirTemp(".", "upload-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(baseDir) })
	basePath, err := filepath.Abs(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.ToSlash(basePath)
	toNative := func(remote string) string { return filepath.FromSlash(remote) }
	if runtime.GOOS == "windows" {
		base = "/" + strings.ToLower(base[:1]) + base[2:]
		toNative = func(remote string) string {
			return strings.ToUpper(remote[1:2]) + ":" + filepath.FromSlash(remote[2:])
		}
	}
	dir := base + "/.pipewright-uploads/test-artifact"
	first, second := []byte("first chunk"), []byte("second chunk")
	all := append(append([]byte{}, first...), second...)
	digest := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
	run := func(want int, action, owner string, args ...string) {
		t.Helper()
		if got := uploadScriptResult(t, shell, action, dir, owner, args...); got != want {
			t.Fatalf("%s: exit %d, want %d", action, got, want)
		}
	}
	chunkArgs := func(i int, body []byte) []string {
		return []string{strconv.Itoa(i), strconv.Itoa(len(body)), digest(body)}
	}
	owner1, owner2 := "first-owner", "second-owner"
	run(0, "acquire", owner1, "128")
	if err := os.WriteFile(toNative(dir+"/"+owner1+"-0.tmp"), first, 0o600); err != nil {
		t.Fatal(err)
	}
	run(0, "commit", owner1, chunkArgs(0, first)...)
	run(0, "release", owner1)
	staleGate := toNative(dir + "/.gate")
	if err := os.Mkdir(staleGate, 0o700); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-12 * time.Minute)
	if err := os.Chtimes(staleGate, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	run(0, "acquire", owner2, "128")
	run(0, "probe", owner2, chunkArgs(0, first)...)
	if err := os.WriteFile(toNative(dir+"/"+owner2+"-1.tmp"), second, 0o600); err != nil {
		t.Fatal(err)
	}
	run(0, "commit", owner2, chunkArgs(1, second)...)
	dest := base + "/releases/new/app.jar"
	run(0, "assemble", owner2, "2", strconv.Itoa(len(all)), digest(all), dest)
	got, err := os.ReadFile(toNative(dest))
	if err != nil || string(got) != string(all) {
		t.Fatalf("assembled payload = %q, %v", got, err)
	}
	run(0, "discard", owner2)
	if err := os.WriteFile(toNative(dir+"/chunk-0"), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(0, "acquire", "third-owner", "128")
	run(77, "probe", "third-owner", chunkArgs(0, first)...)
	run(78, "assemble", "third-owner", "1", strconv.Itoa(len(first)), digest(first), base+"/releases/invalid.jar")
	if _, err := os.Stat(toNative(base + "/releases/invalid.jar")); !os.IsNotExist(err) {
		t.Fatalf("invalid payload became visible: %v", err)
	}
}
