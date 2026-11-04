package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestKubernetesMaintenanceAdmissionAndCleanup(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		o := f.operation(t, "run-maintenance")
		l, e := f.s.registerObserver(ctx, "probe", "", "observer-owner", "engine-test", f.a)
		if e != nil {
			t.Fatal(e)
		}
		if e = maintenance.Fence(ctx, st.DB, "upgrade"); !errors.Is(e, maintenance.ErrBlocked) {
			t.Fatal("live observer did not block upgrade", e)
		}
		l, e = f.s.RecordCleanupBlocked(ctx, l.OperationID, l.Revision)
		if e != nil {
			t.Fatal(e)
		}
		if e = maintenance.Fence(ctx, st.DB, "upgrade"); !errors.Is(e, maintenance.ErrBlocked) {
			t.Fatal("unknown cleanup did not block upgrade", e)
		}
		_, e = f.s.RecordCleanup(ctx, l.OperationID, l.Revision, CleanupEvidence{ContainerName: l.ContainerName, EngineID: l.EngineID, OwnerEpoch: l.OwnerEpoch, CreationNotRequested: true, WriterExited: true, ContainerAbsent: true})
		if e != nil {
			t.Fatal(e)
		}
		if e = maintenance.Fence(ctx, st.DB, "upgrade"); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.registerObserver(ctx, "probe", "", "later-owner", "engine-test", f.a); !errors.Is(e, configstate.ErrMaintenance) {
			t.Fatal("probe admitted during install", e)
		}
		if _, e = f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test"); !errors.Is(e, configstate.ErrMaintenance) {
			t.Fatal("writer admitted during install", e)
		}
		if e = maintenance.Unfence(ctx, st.DB, "upgrade"); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test"); e != nil {
			t.Fatal(e)
		}
		cleanupRuntime(t, f, o)
		if _, e = f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, "uid-held"); e != nil {
			t.Fatal(e)
		}
		if e = maintenance.Fence(ctx, st.DB, "upgrade"); !errors.Is(e, maintenance.ErrBlocked) {
			t.Fatal("unsettled workload did not block upgrade", e)
		}
	})
}
