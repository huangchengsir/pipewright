package selfupdate

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/version"
	"testing"
)

func TestCompletionAndFenceReleaseRollbackTogether(t *testing.T) {
	storetest.SkipIfMySQL(t) // SQLite RAISE trigger injects a failed second transaction write.
	h := fixture(t, nil)
	task := submit(t, h)
	wait(t, h.s, task.ID, "restarting")
	h.s.Close()
	h.opts.Current = func() version.Info { return version.Info{Version: "2.0.0", Platform: "linux/amd64"} }
	// Fail the second write; the completed task must roll back with the fence.
	_, e := h.db.Exec("CREATE TRIGGER reject_fence_release BEFORE UPDATE OF task_id ON selfupdate_control WHEN NEW.task_id='' BEGIN SELECT RAISE(FAIL,'injected fence release failure'); END")
	if e != nil {
		t.Fatal(e)
	}
	second, e := New(h.db, h.opts)
	if e == nil {
		second.Close()
		t.Fatal("failure was hidden")
	}
	old, e := h.s.Get(context.Background(), task.ID)
	if e != nil || old.State != "restarting" {
		t.Fatal(old, e)
	}
	var fence string
	if e = h.db.QueryRow("SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence); e != nil || fence != task.ID {
		t.Fatal(fence, e)
	}
	if _, e = h.db.Exec("DROP TRIGGER reject_fence_release"); e != nil {
		t.Fatal(e)
	}
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	h.s.Start(context.Background())
	wait(t, h.s, task.ID, "completed")
	if e = h.db.QueryRow("SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence); e != nil || fence != "" {
		t.Fatal(fence, e)
	}
	if h.installs.Load() != 1 {
		t.Fatal("installer replayed", h.installs.Load())
	}
}
