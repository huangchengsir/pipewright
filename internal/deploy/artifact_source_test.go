package deploy

import (
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
)

func TestSelectStageArtifactByStableSource(t *testing.T) {
	source := `{"stageId":"build","jobId":"frontend","declarationIndex":0}`
	arts := []run.Artifact{
		{ID: "run-1", Type: run.ArtifactDist, Name: "other", Metadata: map[string]any{"sourceStageId": "build", "sourceJobId": "other", "declarationIndex": float64(0)}},
		{ID: "run-2", Type: run.ArtifactDist, Name: "frontend", Metadata: map[string]any{"sourceStageId": "build", "sourceJobId": "frontend", "declarationIndex": float64(0)}},
	}
	got, err := selectStageArtifact(arts, "dist", source)
	if err != nil || got == nil || got.ID != "run-2" {
		t.Fatalf("selection = %+v, %v", got, err)
	}
	if _, err := selectStageArtifact(arts, "jar", source); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("mismatched type = %v", err)
	}
	if _, err := selectStageArtifact(arts[:1], "dist", source); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("missing source = %v", err)
	}
	arts = append(arts, run.Artifact{ID: "run-3", Type: run.ArtifactDist, Metadata: map[string]any{"sourceStageId": "build", "sourceJobId": "frontend", "declarationIndex": 0}})
	if _, err := selectStageArtifact(arts, "dist", source); !errors.Is(err, ErrArtifactSourceAmbiguous) {
		t.Fatalf("ambiguous source = %v", err)
	}
}

func TestSelectStageArtifactRejectsInvalidSource(t *testing.T) {
	for _, source := range []string{`{}`, `{"stageId":"build","jobId":"frontend"}`, `{"stageId":"build","jobId":"frontend","declarationIndex":-1}`, `{"stageId":"build","jobId":"frontend","declarationIndex":0,"extra":1}`, `{"stageId":"build","jobId":"frontend","declarationIndex":0} {}`} {
		if _, err := selectStageArtifact(nil, "", source); !errors.Is(err, ErrArtifactSourceInvalid) {
			t.Errorf("source %q = %v", source, err)
		}
	}
}
