package runretry

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubernetes"
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

func TestSourceSnapshotRetainsKubernetesAuthorizationAndCommit(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		snap := f.snap
		snap.Project = map[string]any{"defaultBranch": "release/default"}
		snap.Kubernetes = map[string]kubernetes.Authorization{"deploy/kube": {
			Target:             kubernetes.Target{ConnectionID: "connection", CredentialID: "credential", Context: "isolated", Namespace: "app", Deployment: "backend", Container: "app", Executor: kubernetes.Executor{Kind: "local"}},
			ConnectionRevision: 7, CredentialRevision: 8, GrantID: "grant", GrantRevision: 9, RequireApproval: true,
			ClusterKey: "cluster-fingerprint", ProfileFingerprint: "profile-fingerprint", Fingerprint: "authorization-fingerprint",
		}}
		// Both a JSON round-trip and encrypted storage must retain the same
		// authorization together with the pinned source identity.
		encoded, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip Snapshot
		if err = json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(roundTrip.Kubernetes, snap.Kubernetes) || roundTrip.CommitSHA != snap.CommitSHA {
			t.Fatal("JSON lost authorization or commit")
		}
		if _, err = f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('source-kubernetes','project','running',?)", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if err = f.s.SaveManifest(context.Background(), "source-kubernetes", roundTrip); err != nil {
			t.Fatal(err)
		}
		ctx, err := f.s.Prepare(context.Background(), "source-kubernetes")
		if err != nil {
			t.Fatal(err)
		}
		ex, ok := ExecutionFrom(ctx)
		if !ok {
			t.Fatal("missing execution")
		}
		saved := ex.Snapshot()
		if !reflect.DeepEqual(saved.Kubernetes, snap.Kubernetes) || saved.CommitSHA != snap.CommitSHA || saved.Project["defaultBranch"] != "release/default" {
			t.Fatal("sealed snapshot lost Kubernetes policy or source context")
		}
	})
}
