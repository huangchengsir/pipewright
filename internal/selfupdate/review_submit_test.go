package selfupdate

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestSubmitInsertFailureIsNotDefinitiveConflict(t *testing.T) {
	storetest.SkipIfMySQL(t)
	h := fixture(t, nil)
	if _, e := h.db.Exec("CREATE TRIGGER reject_task_insert BEFORE INSERT ON selfupdate_tasks BEGIN SELECT RAISE(ABORT,'synthetic storage failure'); END"); e != nil {
		t.Fatal(e)
	}
	_, e := h.s.Submit(context.Background(), Request{RequestID: "storage-failure", Target: "2.0.0"})
	if !errors.Is(e, ErrUnavailable) || errors.Is(e, ErrConflict) {
		t.Fatalf("unconfirmed storage failure misreported as definitive rejection: %v", e)
	}
	var n int
	if e = h.db.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestExplicitRetryStartsOnlyLocalAcceptedTaskWithoutWorker(t *testing.T) {
	var available atomic.Bool
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			w.WriteHeader(503)
			return
		}
		h.serve(w, r)
	})
	task := submit(t, h)
	task = wait(t, h.s, task.ID, "interrupted")
	h.s.wg.Wait()
	// A failed first phase write can leave exactly this accepted, no-worker state.
	if e := h.s.transition(task, "accepted", ""); e != nil {
		t.Fatal(e)
	}
	available.Store(true)
	same, e := h.s.Submit(context.Background(), Request{RequestID: task.RequestID, Target: task.Target})
	if e != nil || same.ID != task.ID {
		t.Fatal(same, e)
	}
	wait(t, h.s, task.ID, "restarting")
	if h.installs.Load() != 1 {
		t.Fatal("local accepted task was stranded or duplicated", h.installs.Load())
	}
}
