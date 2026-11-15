package runretry

import (
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"testing"
)

func TestSnapshotDigestUsesFrozenGraphWithoutRecompiling(t *testing.T) {
	snap, err := FreezeSnapshot(Snapshot{SchemaVersion: SchemaVersion, ProjectID: "p", Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "s", Name: "Source", Kind: "source", Jobs: []pipeline.Job{{ID: "j", Name: "Checkout", Type: "git_source"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := SnapshotDigest(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Different future compiler interpretations of the source declaration must
	// not replace a captured graph. Every input still participates in its digest.
	snap.Spec.Stages[0].Name = "changed source declaration"
	after, err := SnapshotDigest(snap)
	if err != nil {
		t.Fatal("digest recompiled frozen source", err)
	}
	if before == after {
		t.Fatal("private input omitted from digest")
	}
	snap.Compiled.Version = SchemaVersion + 1
	if _, err = SnapshotDigest(snap); err == nil {
		t.Fatal("unknown frozen graph version accepted")
	}
	snap.Compiled = nil
	if _, err = SnapshotDigest(snap); err == nil {
		t.Fatal("unfrozen graph accepted")
	}
}
