package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

func TestArtifactDeclarationsValidateNames(t *testing.T) {
	for _, raw := range []string{
		`[{"path":"","name":"Web"}]`,
		`[{"path":"dist","name":"bad\nname"}]`,
		`[{"path":"dist","name":"` + strings.Repeat("x", 129) + `"}]`,
		`not-json`,
	} {
		if _, _, err := artifactDeclarations(map[string]any{"artifacts": raw}); err == nil {
			t.Fatalf("expected invalid declaration for %q", raw)
		}
	}
	decls, explicit, err := artifactDeclarations(map[string]any{"artifacts": []map[string]string{{"path": " dist ", "name": " Web UI "}}})
	if err != nil || !explicit || len(decls) != 1 || decls[0].Path != "dist" || decls[0].Name != "Web UI" {
		t.Fatalf("structured declaration = %+v, %v, %v", decls, explicit, err)
	}
}

func TestCollectScriptArtifactsNamedAndLegacy(t *testing.T) {
	workspace := artifactWorkspace(t)
	if err := os.MkdirAll(filepath.Join(workspace, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "target", "app.jar"), []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := &fakeReporter{}
	b := &Builder{}
	jobs := []pipeline.Job{{ID: "job-1", Name: "Build", Config: map[string]any{
		"artifacts":    `[{"path":"target/app.jar","name":"Backend API"}]`,
		"artifactPath": "target/app.jar",
	}}}
	if err := b.collectScriptArtifacts(context.Background(), jobs, workspace, "project", "stage-1", "Build", rep); err != nil {
		t.Fatalf("collect named artifacts: %v; logs=%v", err, rep.logs)
	}
	if len(rep.arts) != 1 || rep.arts[0].Name != "Backend API" {
		t.Fatalf("named declarations should replace legacy paths: %+v", rep.arts)
	}
	if rep.arts[0].Metadata["sourceJobId"] != "job-1" || rep.arts[0].Metadata["declarationIndex"] != 0 {
		t.Fatalf("missing stable source identity: %+v", rep.arts[0].Metadata)
	}
	rep = &fakeReporter{}
	jobs[0].Config = map[string]any{"artifactPath": "missing.jar\ntarget/app.jar"}
	if err := b.collectScriptArtifacts(context.Background(), jobs, workspace, "project", "stage-1", "Build", rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.arts) != 1 || rep.arts[0].Name != "project-app.jar" {
		t.Fatalf("legacy behavior changed: %+v", rep.arts)
	}
}

func TestCollectScriptArtifactsRejectsMissingAndDuplicateNamedPaths(t *testing.T) {
	workspace := artifactWorkspace(t)
	if err := os.WriteFile(filepath.Join(workspace, "a.jar"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Builder{}
	for _, declarations := range []string{
		`[{"path":"missing.jar","name":"Missing"}]`,
		`[{"path":"a.jar","name":"One"},{"path":"a.jar","name":"Two"}]`,
		`[{"path":"../outside.jar","name":"Outside"}]`,
	} {
		rep := &fakeReporter{}
		jobs := []pipeline.Job{{ID: "job", Name: "Build", Config: map[string]any{"artifacts": declarations}}}
		if err := b.collectScriptArtifacts(context.Background(), jobs, workspace, "project", "stage", "Build", rep); !errors.Is(err, ErrBuildFailed) {
			t.Fatalf("declarations %s: got %v", declarations, err)
		}
	}
}

func TestNamedArtifactDoesNotEmitPlaceholderWhenConfiguredStoreFails(t *testing.T) {
	workspace := artifactWorkspace(t)
	if err := os.WriteFile(filepath.Join(workspace, "app.jar"), []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	storeRoot := filepath.Join(workspace, "store")
	st, err := artifactstore.New(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(storeRoot, storeRoot+"-offline"); err != nil {
		t.Fatal(err)
	}
	b := &Builder{artStore: st}
	rep := &fakeReporter{}
	jobs := []pipeline.Job{{ID: "job", Name: "Build", Config: map[string]any{"artifacts": `[{"path":"app.jar","name":"Backend"}]`}}}
	if err := b.collectScriptArtifacts(context.Background(), jobs, workspace, "project", "stage", "Build", rep); !errors.Is(err, ErrBuildFailed) || len(rep.arts) != 0 {
		t.Fatalf("named artifact became a placeholder: %v, %+v", err, rep.arts)
	}
}

func artifactWorkspace(t *testing.T) string {
	t.Helper()
	workspace, err := os.MkdirTemp(".", "artifact-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	return workspace
}
