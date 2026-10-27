package selfupdate

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/version"
	"net/http"
	"testing"
)

func TestRestartAtEveryTaskStageRequiresExplicitRecovery(t *testing.T) {
	for _, state := range []string{"accepted", "checking", "downloading", "verifying", "extracting", "interrupted", "ready_blocked", "installing", "restarting", "restart_required", "unknown"} {
		t.Run(state, func(t *testing.T) {
			h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
			task := submit(t, h)
			wait(t, h.s, task.ID, "interrupted")
			h.s.Close()
			if e := h.s.transition(task, state, "seeded-test-boundary"); e != nil {
				t.Fatal(e)
			}
			next, e := New(h.db, h.opts)
			if e != nil {
				t.Fatal(e)
			}
			h.s = next
			h.s.Start(context.Background())
			expected := "interrupted"
			if state == "ready_blocked" {
				expected = state
			}
			if state == "installing" || state == "restarting" || state == "restart_required" || state == "unknown" {
				expected = "unknown"
			}
			got, e := h.s.Get(context.Background(), task.ID)
			if e != nil || got.State != expected || h.installs.Load() != 0 {
				t.Fatal(got, e, h.installs.Load())
			}
			var n int
			if e = h.db.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&n); e != nil || n != 1 {
				t.Fatal(n, e)
			}
		})
	}
}
func TestExactVersionWrongPlatformDoesNotConfirm(t *testing.T) {
	h := fixture(t, nil)
	task := submit(t, h)
	wait(t, h.s, task.ID, "restarting")
	h.s.Close()
	h.opts.Current = func() version.Info { return version.Info{Version: "2.0.0", Platform: "linux/arm64"} }
	var e error
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	got, e := h.s.Get(context.Background(), task.ID)
	if e != nil || got.State != "unknown" || h.installs.Load() != 1 {
		t.Fatal(got, e, h.installs.Load())
	}
}
