package dbmigration

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

// Exercise an actual MySQL handshake stall, not a mocked query or a dial error.
func TestPreflightCancelsBlackholeHandshakeAndReleasesRuntime(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, err := store.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	endpoint, err := Describe(context.Background(), source, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := ResolvePaths(dir, sourcePath, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	key := [32]byte{23}
	control, err := AcquireControl(dir, &key)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	active := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: sourcePath}, Endpoint: endpoint, Paths: paths}
	runtime := NewRuntime(control, active, source, &key, false, nil)
	defer runtime.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runtime.Preflight(ctx, Connection{Driver: "mysql", DSN: "fixture@tcp(" + listener.Addr().String() + ")/fixture"}, true)
		done <- err
	}()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("MySQL handshake did not reach listener")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrConnection) {
			t.Fatalf("safe connection error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled handshake retained runtime lock")
	}
	// This second serialized operation must finish after the failed request.
	go func() { _, err := runtime.Preflight(context.Background(), active.Connection, true); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("subsequent preflight: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime lock not released")
	}
	state, err := control.State()
	if err != nil || state != nil {
		t.Fatal("cancelled request persisted a task", err)
	}
}
