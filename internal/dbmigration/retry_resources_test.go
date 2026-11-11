package dbmigration

import (
	"bytes"
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointOnlyCASIsVerifiedWithoutArtifactRows(t *testing.T) {
	storetest.ForEachDialect(t, testCheckpointOnlyCAS)
}

func testCheckpointOnlyCAS(t *testing.T, db *store.Store) {
	ctx := context.Background()
	key := [32]byte{19}
	paths := RuntimePaths{Artifacts: filepath.Join(t.TempDir(), "artifacts")}
	blobs, e := artifactstore.New(paths.Artifacts)
	if e != nil {
		t.Fatal(e)
	}
	hash, size, e := blobs.Put(bytes.NewBufferString("checkpoint-only artifact"))
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := vault.New(db.DB, &key).SealSecret([]byte("fixture"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('cred','cred','git_token','',?,'***','now','now')", cipher); e != nil {
		t.Fatal(e)
	}
	if _, e = db.DB.Exec("INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES('project','project','url','cred','now','now')"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.DB.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('run','project','failed','now')"); e != nil {
		t.Fatal(e)
	}
	retry := runretry.New(db.DB, vault.New(db.DB, &key), runretry.WithBlobStore(blobs))
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "stage", Name: "stage", Kind: "build", Jobs: []pipeline.Job{{ID: "job", Name: "job", Type: "script"}}}}}
	snapshot := runretry.Snapshot{SchemaVersion: runretry.SchemaVersion, ProjectID: "project", Spec: spec, CommitSHA: strings.Repeat("a", 40)}
	if e = retry.SaveManifest(ctx, "run", snapshot); e != nil {
		t.Fatal(e)
	}
	execCtx, e := retry.Prepare(ctx, "run")
	if e != nil {
		t.Fatal(e)
	}
	execution, ok := runretry.ExecutionFrom(execCtx)
	if !ok {
		t.Fatal("missing execution")
	}
	node, ok := execution.Node("stage", "job")
	if !ok {
		t.Fatal("missing node")
	}
	outcome := runretry.Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "none", Outputs: runretry.Inputs{Files: []runretry.FileRef{{Key: hash, SHA256: hash, Size: size, Path: "artifact.bin", Kind: "file"}}}}
	if e = execution.Finish(ctx, node, outcome); e != nil {
		t.Fatal(e)
	}
	if e = VerifyLocalResources(ctx, db, &key, paths); e != nil {
		t.Fatal("valid retained object rejected", e)
	}
	object := filepath.Join(paths.Artifacts, hash[:2], hash)
	if e = os.WriteFile(object, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = VerifyLocalResources(ctx, db, &key, paths); !errors.Is(e, ErrResources) {
		t.Fatal("corrupt retained object accepted", e)
	}
	if e = os.Remove(object); e != nil {
		t.Fatal(e)
	}
	if e = VerifyLocalResources(ctx, db, &key, paths); !errors.Is(e, ErrResources) {
		t.Fatal("missing retained object accepted", e)
	}
}
