package httpapi

import (
	"context"
	"errors"
	"github.com/coder/websocket"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/selfupdate"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type heldUpgradeSession struct {
	started chan struct{}
	exit    chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (s *heldUpgradeSession) Read(p []byte) (int, error) {
	close(s.started)
	<-s.exit
	return 0, io.EOF
}
func (s *heldUpgradeSession) Write(p []byte) (int, error) { return len(p), nil }
func (s *heldUpgradeSession) Resize(int, int) error       { return nil }
func (s *heldUpgradeSession) Close() error                { s.once.Do(func() { close(s.closed) }); return nil }
func TestUpgradeFenceWaitsForWebSocketOutputReaderExit(t *testing.T) {
	st := testStoreAuth(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "exe")
	os.WriteFile(exe, []byte("old"), 0600)
	updater, e := selfupdate.New(st.DB, selfupdate.Options{Executable: exe, Directory: filepath.Join(dir, "work")})
	if e != nil {
		t.Fatal(e)
	}
	defer updater.Close()
	session := &heldUpgradeSession{started: make(chan struct{}), exit: make(chan struct{}), closed: make(chan struct{})}
	finished := make(chan struct{})
	srv := httptest.NewServer(updateAdmissionMiddleware(updater)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		pumpTerminal(ctx, cancel, conn, session)
	})))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, e := websocket.Dial(ctx, "ws"+srv.URL[4:]+"/api/servers/test/terminal/ws-v2", nil)
	if e != nil {
		t.Fatal(e)
	}
	<-session.started
	conn.CloseNow()
	<-session.closed
	if e = maintenance.Fence(ctx, st.DB, "install"); !errors.Is(e, maintenance.ErrBlocked) {
		t.Fatalf("reader still live but fence passed: %v", e)
	}
	close(session.exit)
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The middleware lease is released after the handler joins both directions.
	until := time.Now().Add(time.Second)
	for {
		e = maintenance.Fence(ctx, st.DB, "install")
		if e == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	maintenance.Unfence(ctx, st.DB, "install")
}
