package runretry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestScriptEntrypointSnapshotContractAndLegacyCompatibility(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		old := f.ex.Snapshot()
		clear, err := old.ClearScriptEntrypoint()
		if err != nil || clear {
			t.Fatal("legacy contract changed", clear, err)
		}
		b, err := json.Marshal(old)
		if err != nil || bytes.Contains(b, []byte("scriptEntrypointMode")) {
			t.Fatal("legacy wire representation changed", err)
		}
		if f.nodes["b"].Key != "0560fb7b35d5e5f6e5aa18443d7e50ac8401e49aced4e6ed50816ec357ebfde7" || SchemaVersion != 1 {
			t.Fatal("historical node identity changed")
		}
		before := f.plan(t)
		if err = f.s.SaveManifest(ctx, "root", f.snap); err != nil {
			t.Fatal("legacy idempotent save changed", err)
		}
		restarted := New(f.db, f.s.sealer)
		after, err := restarted.Plan(ctx, "root", before.TargetNodeIDs)
		if err != nil || after.PlanFingerprint != before.PlanFingerprint || after.ManifestFingerprint != before.ManifestFingerprint {
			t.Fatal("legacy plan changed after load", err)
		}
		changed := old
		changed.ScriptEntrypointMode = ScriptEntrypointClearV1
		if err = f.s.SaveManifest(ctx, "root", changed); !errors.Is(err, ErrConflict) {
			t.Fatal("existing manifest policy silently upgraded", err)
		}
		if _, err = f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('fresh','project','running','old')"); err != nil {
			t.Fatal(err)
		}
		fresh := f.snap
		fresh.ScriptEntrypointMode = ScriptEntrypointClearV1
		if err = f.s.SaveManifest(ctx, "fresh", fresh); err != nil {
			t.Fatal(err)
		}
		prepared, err := restarted.Prepare(ctx, "fresh")
		if err != nil {
			t.Fatal(err)
		}
		ex, ok := ExecutionFrom(prepared)
		if !ok {
			t.Fatal("new captured execution missing")
		}
		clear, err = ex.Snapshot().ClearScriptEntrypoint()
		if err != nil || !clear {
			t.Fatal("new contract lost on restart", clear, err)
		}
		unsupported := fresh
		unsupported.ScriptEntrypointMode = "future-v9"
		if err = f.s.SaveManifest(ctx, "fresh", unsupported); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown mode admitted", err)
		}
		// Model data written by a future executor without mutating the global
		// envelope/node schema. An older executor must not reinterpret it.
		unsupported.Compiled = ex.Snapshot().Compiled
		cipher, err := f.s.seal("manifest", "fresh", "", unsupported)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE run_retry_manifests SET ciphertext=? WHERE root_id='fresh'", cipher); err != nil {
			t.Fatal(err)
		}
		if _, err = restarted.Prepare(ctx, "fresh"); !errors.Is(err, ErrUnavailable) {
			t.Fatal("future policy executed", err)
		}
		plan, err := restarted.Plan(ctx, "fresh", before.TargetNodeIDs)
		if err != nil || plan.CanSubmit || len(plan.BlockingReasons) != 1 || plan.BlockingReasons[0].Code != "snapshot_unavailable" {
			t.Fatal("future policy not blocked", plan, err)
		}
	})
}
