package runretry

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestSourceSnapshotWithoutContextVersion(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		data, err := json.Marshal(f.snap)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("ciContextMode")) {
			t.Fatalf("unexpected version field: %s", data)
		}
		if err := f.s.SaveManifest(context.Background(), "root", f.snap); err != nil {
			t.Fatal(err)
		}
		ctx, err := f.s.Prepare(context.Background(), "root")
		if err != nil {
			t.Fatal(err)
		}
		execution, ok := ExecutionFrom(ctx)
		if !ok || execution.Snapshot().CommitSHA != f.snap.CommitSHA {
			t.Fatalf("source commit not retained")
		}
	})
}
