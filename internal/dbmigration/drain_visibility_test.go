package dbmigration

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestDrainSeesLeaseReleasedByIndependentConnection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.db")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	other, e := store.Connect(store.OpenConfig{Driver: "sqlite", DSN: path})
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	endpoint, e := Describe(ctx, db, path)
	if e != nil {
		t.Fatal(e)
	}
	key := [32]byte{12}
	c, e := AcquireControl(dir, &key)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	source := ActiveConfig{Connection: Connection{Driver: "sqlite", DSN: path}, Endpoint: endpoint}
	target := ActiveConfig{Connection: Connection{Driver: "postgres", DSN: "synthetic"}, Endpoint: Endpoint{Dialect: "postgres", Database: "synthetic", Schema: "public"}}
	state, e := c.Plan(source, target)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.DB.Exec("INSERT INTO selfupdate_leases(id,kind,created_at) VALUES(?,?,?)", "external", "synthetic", 1); e != nil {
		t.Fatal(e)
	}
	restarted := make(chan struct{}, 1)
	r := NewRuntime(c, source, db, &key, false, func() error { restarted <- struct{}{}; return nil })
	defer r.Close()
	if _, e = r.Start(ctx, state.TaskID, state.Revision); e != nil {
		t.Fatal(e)
	}
	if release, e := maintenance.Enter(ctx, db.DB, "denied-config"); e == nil {
		release()
		t.Fatal("admitted while draining")
	}
	if _, e = other.DB.Exec("DELETE FROM selfupdate_leases WHERE id=?", "external"); e != nil {
		t.Fatal(e)
	}
	if blockers, e := r.Blockers(ctx); e != nil || len(blockers) != 0 {
		t.Fatal("retained old read snapshot", blockers, e)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("independent release did not restart")
	}
}
