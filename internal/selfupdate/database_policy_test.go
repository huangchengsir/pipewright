package selfupdate

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/version"
)

func TestPublicUpdateRequiresCompatibleActiveDatabase(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := &Service{db: st.DB, ctx: ctx, instance: "policy-fixture", opts: Options{TotalTimeout: time.Second, Capabilities: func() (version.DeploymentMode, bool) { return version.ModeBinary, true }, Install: func(string, string) error { t.Fatal("private DM installed public binary"); return nil }}}
		_, can := s.Capabilities()
		if st.Dialect != store.DM {
			if !can || s.RequiresPrivateBuild() {
				t.Fatal("ordinary database upgrade disabled")
			}
			return
		}
		if can || !s.RequiresPrivateBuild() {
			t.Fatal("active DM was not detected")
		}
		if _, e := s.Submit(ctx, Request{RequestID: "private-update", Target: "2.0.0"}); !errors.Is(e, ErrPrivateBuild) {
			t.Fatal(e)
		}
		if _, e := s.Resume(ctx, "old-task", "resume-private"); !errors.Is(e, ErrPrivateBuild) {
			t.Fatal(e)
		}
		var count int
		if e := st.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&count); e != nil || count != 0 {
			t.Fatal("rejected update changed tasks", count, e)
		}
		// Even a persisted task reaching the worker cannot install a public build.
		task := Task{ID: "old-task", RequestID: "old-request", Target: "2.0.0", State: "accepted", Source: "github"}
		raw, e := encoded(&task)
		if e != nil {
			t.Fatal(e)
		}
		_, e = st.DB.Exec("INSERT INTO selfupdate_tasks(id,instance_id,request_id,target_tag,status,active_key,record_json,created_at) VALUES(?,?,?,?,?,?,?,?)", task.ID, s.instance, task.RequestID, task.Target, task.State, s.instance, raw, 1)
		if e != nil {
			t.Fatal(e)
		}
		s.work(task)
		got, e := s.Get(ctx, task.ID)
		if e != nil || got.State != "failed" || got.Reason != "private_driver_build" {
			t.Fatal("worker did not refuse public build", e)
		}
	})
}

func TestMigrationAdmissionRejectsPublicUpdateAndResume(t *testing.T) {
	h := fixture(t, func(_ *harness, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	ctx := context.Background()
	if e := maintenance.Drain(ctx, h.db, "migration"); e != nil {
		t.Fatal(e)
	}
	if _, e := h.s.Submit(ctx, Request{RequestID: "during-migration", Target: "2.0.0"}); !errors.Is(e, configstate.ErrMaintenance) {
		t.Fatal(e)
	}
	var n int
	if e := h.db.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&n); e != nil || n != 0 {
		t.Fatal("migration admitted update", n, e)
	}
	if e := maintenance.Unfence(ctx, h.db, "migration"); e != nil {
		t.Fatal(e)
	}
	task, e := h.s.Submit(ctx, Request{RequestID: "before-migration", Target: "2.0.0"})
	if e != nil {
		t.Fatal(e)
	}
	wait(t, h.s, task.ID, "interrupted")
	if _, e = h.db.Exec("UPDATE selfupdate_control SET task_id=? WHERE id=1", "migration"); e != nil {
		t.Fatal(e)
	}
	if _, e = h.s.Resume(ctx, task.ID, "resume-during-migration"); !errors.Is(e, configstate.ErrMaintenance) {
		t.Fatal(e)
	}
	if h.installs.Load() != 0 {
		t.Fatal("fenced update installed")
	}
}

func TestMigrationAndUpdateAdmissionAreAtomic(t *testing.T) {
	h := fixture(t, func(_ *harness, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	start := make(chan struct{})
	drain := make(chan error, 1)
	submit := make(chan error, 1)
	go func() { <-start; drain <- maintenance.Drain(context.Background(), h.db, "migration") }()
	go func() {
		<-start
		_, e := h.s.Submit(context.Background(), Request{RequestID: "racing-update", Target: "2.0.0"})
		submit <- e
	}()
	close(start)
	a, b := <-drain, <-submit
	if a == nil && b == nil {
		t.Fatal("migration and update both admitted")
	}
	if a != nil && b != nil {
		t.Fatal("neither independent contender admitted", a, b)
	}
}
