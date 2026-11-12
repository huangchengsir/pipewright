package dbmigration

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func probeRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	source, e := store.Open(sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	key := [32]byte{43}
	control, e := AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	endpoint, e := Describe(context.Background(), source, sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	r := NewRuntime(control, ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: sourcePath}, Endpoint: endpoint}, source, &key, false, nil)
	t.Cleanup(func() { r.Close(); control.Close(); source.Close() })
	return r, dir
}
func TestProbeConnectionSQLiteNoMutation(t *testing.T) {
	r, dir := probeRuntime(t)
	target := Connection{Driver: "sqlite", DSN: filepath.Join(dir, "new.db")}
	before, _ := r.Control.State()
	var changesBefore int
	if e := r.Source.DB.QueryRow("SELECT total_changes()").Scan(&changesBefore); e != nil {
		t.Fatal(e)
	}
	result, e := r.ProbeConnection(context.Background(), target)
	if e != nil || result.Kind != "path" {
		t.Fatalf("probe: %+v %v", result, e)
	}
	if _, e := os.Stat(target.DSN); !os.IsNotExist(e) {
		t.Fatal("probe created a target file")
	}
	after, _ := r.Control.State()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("probe changed control state")
	}
	var changesAfter int
	if e := r.Source.DB.QueryRow("SELECT total_changes()").Scan(&changesAfter); e != nil || changesAfter != changesBefore {
		t.Fatal("probe wrote to source", e)
	}
	for _, path := range []string{r.Current.Connection.DSN, filepath.Join(dir, "missing", "new.db"), dir} {
		if _, e := r.ProbeConnection(context.Background(), Connection{Driver: "sqlite", DSN: path}); !errors.Is(e, ErrProbePath) {
			t.Fatalf("invalid path accepted: %v", e)
		}
	}
}
func TestProbeConnectionPreparedAndMaintenance(t *testing.T) {
	r, dir := probeRuntime(t)
	target := Connection{Driver: "sqlite", DSN: filepath.Join(dir, "new.db")}
	prepared, e := r.Control.Plan(r.Current, ActiveConfig{Connection: target, Endpoint: Endpoint{Dialect: "sqlite", FilePath: target.DSN}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.ProbeConnection(context.Background(), target); e != nil {
		t.Fatal(e)
	}
	after, _ := r.Control.State()
	if !reflect.DeepEqual(prepared, after) {
		t.Fatal("prepared task changed")
	}
	r.Recovery = true
	if _, e = r.ProbeConnection(context.Background(), target); !errors.Is(e, ErrInvalidState) {
		t.Fatal(e)
	}
	r.Recovery = false
	if _, e = r.Source.DB.Exec("UPDATE selfupdate_control SET task_id=? WHERE id=1", "other"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ProbeConnection(context.Background(), target); e == nil {
		t.Fatal("maintenance probe allowed")
	}
}
func TestProbeConnectionDeadlineIncludesRuntimeQueue(t *testing.T) {
	r, dir := probeRuntime(t)
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := r.ProbeConnection(ctx, Connection{Driver: "sqlite", DSN: filepath.Join(dir, "new.db")}); !errors.Is(e, ErrProbeTimeout) {
		t.Fatal(e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("runtime queue ignored deadline")
	}
}
func TestProbeConnectionBlackholeHandshakeCancellation(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres", "sqlserver"} {
		t.Run(driver, func(t *testing.T) {
			r, _ := probeRuntime(t)
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, e := listener.Accept()
				if e == nil {
					accepted <- c
				}
			}()
			address := listener.Addr().(*net.TCPAddr)
			target, e := BuildConnection(ConnectionFields{Driver: driver, Host: "127.0.0.1", Port: address.Port, Database: "probe", Username: "probe", Password: "probe-secret"})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, e = r.ProbeConnection(ctx, target)
			if !errors.Is(e, ErrProbeTimeout) {
				t.Fatalf("expected safe timeout, got %v", e)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("handshake ignored cancellation")
			}
			select {
			case c := <-accepted:
				c.Close()
			case <-time.After(time.Second):
				t.Fatal("probe did not reach TCP peer")
			}
		})
	}
}

func TestProbeConnectionDeadlineIncludesControlQueue(t *testing.T) {
	r, dir := probeRuntime(t)
	r.Control.mu.Lock()
	defer r.Control.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := r.ProbeConnection(ctx, Connection{Driver: "sqlite", DSN: filepath.Join(dir, "new.db")}); !errors.Is(e, ErrProbeTimeout) {
		t.Fatal(e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("control queue ignored deadline")
	}
}

func TestProbeConnectionRejectsEveryActiveMigrationPhase(t *testing.T) {
	r, dir := probeRuntime(t)
	target := Connection{Driver: "sqlite", DSN: filepath.Join(dir, "new.db")}
	prepared, e := r.Control.Plan(r.Current, ActiveConfig{Connection: target, Endpoint: Endpoint{Dialect: "sqlite", FilePath: target.DSN}})
	if e != nil {
		t.Fatal(e)
	}
	for _, phase := range []Phase{Quiescing, Frozen, Copying, Verified, ActivationIntent, AwaitingRestart} {
		t.Run(string(phase), func(t *testing.T) {
			state := *prepared
			state.Phase = phase
			if e := r.Control.write("state", state); e != nil {
				t.Fatal(e)
			}
			if _, e := r.ProbeConnection(context.Background(), target); !errors.Is(e, ErrInvalidState) {
				t.Fatalf("active phase permitted: %v", e)
			}
			after, e := r.Control.State()
			if e != nil || !reflect.DeepEqual(&state, after) {
				t.Fatal("active task changed", e)
			}
		})
	}
}
func TestProbeConnectionAlreadyCanceledDoesNotTouchTarget(t *testing.T) {
	r, dir := probeRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := filepath.Join(dir, "new.db")
	if _, e := r.ProbeConnection(ctx, Connection{Driver: "sqlite", DSN: target}); !errors.Is(e, ErrProbeTimeout) {
		t.Fatal(e)
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("cancelled probe created target")
	}
}

func TestProbeConnectionSourceFenceBlackholeReleasesRuntimeAndPreservesSharedPool(t *testing.T) {
	r, dir := probeRuntime(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			accepted <- conn
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	source, err := BuildConnection(ConnectionFields{Driver: "sqlserver", Host: "127.0.0.1", Port: address.Port, Database: "source", Username: "probe", Password: "private-marker"})
	if err != nil {
		t.Fatal(err)
	}
	// The configured source endpoint is unresponsive; the existing shared pool
	// remains live and must not be borrowed or closed to interrupt this probe.
	r.Current.Connection = source
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	target := filepath.Join(dir, "not-created.db")
	if _, err := r.ProbeConnection(ctx, Connection{Driver: "sqlite", DSN: target}); !errors.Is(err, ErrProbeTimeout) {
		t.Fatal("source fence escaped timeout", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("source fence exceeded request budget")
	}
	if !r.mu.TryLock() {
		t.Fatal("source timeout retained runtime lock")
	}
	r.mu.Unlock()
	var one int
	if err := r.Source.DB.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("shared source pool was closed", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("source failure reached target path")
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("source endpoint not probed")
	}
}

func TestProbeConnectionSQLServerSourceFenceLockedLive(t *testing.T) {
	if os.Getenv(storetest.SQLServerDSNEnv) == "" {
		t.Skip("dedicated SQL Server fixture DSN required")
	}
	// storetest owns a uniquely named database and drops only that fixture.
	source := storetest.OpenForDialect(t, store.SQLServer)
	connection := nonemptyConnection(t, source)
	dir := t.TempDir()
	key := [32]byte{71}
	control, err := AcquireControl(dir, &key)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	runtime := NewRuntime(control, ActiveConfig{Connection: connection}, source, &key, false, nil)
	defer runtime.Close()
	tx, err := source.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal("source lock transaction failed")
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE selfupdate_control WITH (ROWLOCK) SET task_id=task_id WHERE id=1"); err != nil {
		t.Fatal("source fence row lock failed")
	}
	target := filepath.Join(dir, "not-created.db")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = runtime.ProbeConnection(ctx, Connection{Driver: "sqlite", DSN: target})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrProbeTimeout) {
		t.Fatal("locked source did not time out safely", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("source fence exceeded deadline: %v", elapsed)
	}
	if !runtime.mu.TryLock() {
		t.Fatal("source fence timeout retained runtime lock")
	}
	runtime.mu.Unlock()
	var one int
	verifyCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := source.DB.QueryRowContext(verifyCtx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("shared source connection pool became unavailable")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("source fence failure reached target")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("fixture source lock release failed")
	}
	var task string
	if err := source.DB.QueryRowContext(verifyCtx, "SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&task); err != nil || task != "" {
		t.Fatal("source fence mutated")
	}
	t.Logf("dedicated source fence timed out in %s; shared source and runtime lock remain usable", elapsed)
}
