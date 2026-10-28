package runretry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

func TestR3CrossStageMatrixSelectorNarrowsFilesImagesAndPreservesDeclaredEnv(t *testing.T) {
	for _, kind := range []string{"archive", "image", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			blobs := memoryBlobs{}
			keys := map[string]string{}
			for _, axis := range []string{"a", "b"} {
				data := []byte("payload-" + axis)
				sum := sha256.Sum256(data)
				keys[axis] = hex.EncodeToString(sum[:])
				blobs[keys[axis]] = data
			}
			f := newFixture(t, storetest.OpenForDialect(t, store.SQLite), WithBlobStore(blobs))
			ctx := context.Background()
			if _, e := f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('matrix','project','running','old')"); e != nil {
				t.Fatal(e)
			}
			selector := `{"stageId":"source","jobId":"bundle","declarationIndex":0}`
			consumer := pipeline.Stage{ID: "consume", Name: "consume", Needs: []string{"source"}, Matrix: map[string][]string{"os": {"a", "b"}}, Jobs: []pipeline.Job{{ID: "env", Name: "env", Type: "script"}, {ID: "deploy", Name: "deploy", Type: "deploy_ssh", Needs: []string{"env"}, Config: map[string]any{"artifactType": kind, "artifactSource": selector}}}}
			if kind == "ambiguous" {
				consumer.Matrix = nil
				consumer.Jobs[1].Config["artifactType"] = "archive"
			}
			spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "source", Name: "source", Matrix: map[string][]string{"os": {"a", "b"}}, Jobs: []pipeline.Job{{ID: "bundle", Name: "bundle", Type: "build_image"}}}, consumer}}
			if e := f.s.SaveManifest(ctx, "matrix", Snapshot{ProjectID: "project", Spec: spec, CommitSHA: strings.Repeat("a", 40)}); e != nil {
				t.Fatal(e)
			}
			prepared, e := f.s.Prepare(ctx, "matrix")
			if e != nil {
				t.Fatal(e)
			}
			ex, _ := ExecutionFrom(prepared)
			view, e := f.s.Context(ctx, "matrix")
			if e != nil {
				t.Fatal(e)
			}
			for _, n := range view.Graph.Nodes {
				if n.ControlKey != "" || n.JobID == "deploy" {
					continue
				}
				o := Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, Outputs: Inputs{Env: map[string]string{}}}
				if n.JobID == "env" {
					o.Outputs.Env["DIRECT"] = "env-" + n.Axes["os"]
				} else {
					axis := n.Axes["os"]
					o.Outputs.Env["SOURCE_PRIVATE"] = axis
					if kind == "image" {
						o.Outputs.Images = map[string]ImageRef{n.Key: {Ref: "pipewright/cell:" + axis, ImageID: "sha256:" + strings.Repeat(axis, 64), RuntimeID: "runtime", ConfigSHA: strings.Repeat("f", 64), Metadata: map[string]any{"declarationIndex": 0}}}
					} else {
						o.Outputs.Files = []FileRef{{Key: keys[axis], SHA256: keys[axis], Size: int64(len(blobs[keys[axis]])), Path: "same-path.txt", Kind: "file", ArtifactType: "archive", Metadata: map[string]any{"declarationIndex": 0}}}
					}
				}
				if e = ex.Begin(prepared, n); e != nil {
					t.Fatal(e)
				}
				if e = ex.Finish(prepared, n, o); e != nil {
					t.Fatal(e)
				}
			}
			for _, n := range view.Graph.Nodes {
				if n.JobID != "deploy" {
					continue
				}
				if e = ex.Begin(prepared, n); e != nil {
					t.Fatal(e)
				}
				in, e := ex.Inputs(prepared, n)
				if kind == "ambiguous" {
					if e == nil {
						t.Fatal("non-matrix consumer guessed a source cell")
					}
					continue
				}
				if e != nil {
					t.Fatal(e)
				}
				axis := n.Axes["os"]
				if in.Env["DIRECT"] != "env-"+axis || in.Env["SOURCE_PRIVATE"] != "" {
					t.Fatal("selector changed env dependency semantics", in.Env)
				}
				if kind == "image" {
					if len(in.Images) != 1 {
						t.Fatal(in.Images)
					}
					for _, image := range in.Images {
						if image.Ref != "pipewright/cell:"+axis {
							t.Fatal("wrong image cell", image)
						}
					}
				} else {
					if len(in.Files) != 1 || in.Files[0].Key != keys[axis] {
						t.Fatal("wrong file cell", in.Files)
					}
				}
			}
		})
	}
}
