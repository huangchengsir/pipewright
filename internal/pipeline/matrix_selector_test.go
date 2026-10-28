package pipeline

import (
	"encoding/json"
	"testing"
)

func TestMatrixExplicitSelectorUsesOnlyKnownSameCellIDs(t *testing.T) {
	original := `{"stageId":"matrix","jobId":"build","declarationIndex":1}`
	stages := []Stage{{ID: "matrix", Name: "M", Matrix: map[string][]string{"os": {"a", "b"}}, Jobs: []Job{{ID: "build", Type: "build_image"}, {ID: "deploy", Type: "deploy_ssh", Config: map[string]any{"artifactSource": original}}, {ID: "foreign", Type: "deploy_ssh", Config: map[string]any{"artifactSource": `{"stageId":"other","jobId":"build","declarationIndex":0}`}}}}}
	out := ExpandMatrix(stages)
	for _, cell := range out {
		var selector struct{ StageID, JobID string }
		if json.Unmarshal([]byte(cell.Jobs[1].Config["artifactSource"].(string)), &selector) != nil || selector.StageID != cell.ID || selector.JobID != cell.Jobs[0].ID {
			t.Fatal(cell)
		}
		if cell.Jobs[2].Config["artifactSource"] != stages[0].Jobs[2].Config["artifactSource"] {
			t.Fatal("cross-cell/source selection guessed")
		}
	}
	if stages[0].Jobs[1].Config["artifactSource"] != original {
		t.Fatal("declared spec mutated")
	}
}
func TestCrossStageMatrixSelectorRequiresCompleteUniqueAxisTuple(t *testing.T) {
	selector := `{"stageId":"build","jobId":"bundle","declarationIndex":0}`
	source := Stage{ID: "build", Matrix: map[string][]string{"os": {"a", "b"}}, Jobs: []Job{{ID: "bundle", Type: "build_image"}}}
	consumer := Stage{ID: "deploy", Needs: []string{"build"}, Matrix: map[string][]string{"os": {"a", "b"}}, Jobs: []Job{{ID: "deploy", Type: "deploy_ssh", Config: map[string]any{"artifactSource": selector}}}}
	expanded := ExpandMatrix([]Stage{source, consumer})
	for _, cell := range expanded {
		if cell.MatrixParentID != "deploy" {
			continue
		}
		var parsed struct{ StageID, JobID string }
		if json.Unmarshal([]byte(cell.Jobs[0].Config["artifactSource"].(string)), &parsed) != nil {
			t.Fatal(cell)
		}
		axis := cell.Jobs[0].Config["__matrixEnv"].(map[string]string)["MATRIX_OS"]
		expected := MatrixCellID("build", map[string]string{"os": axis})
		if parsed.StageID != expected || parsed.JobID != "bundle__"+cellSuffixFromCell(map[string]string{"os": axis}) {
			t.Fatal(parsed, cell)
		}
	}
	if consumer.Jobs[0].Config["artifactSource"] != selector {
		t.Fatal("original selector changed")
	}
	for _, axes := range []map[string][]string{nil, {"other": {"a"}}} {
		consumer.Matrix = axes
		expanded = ExpandMatrix([]Stage{source, consumer})
		for _, cell := range expanded {
			if cell.ID == "deploy" || cell.MatrixParentID == "deploy" {
				if cell.Jobs[0].Config["__matrixArtifactSourceAmbiguous"] != true {
					t.Fatal("ambiguous cell was guessed", cell)
				}
			}
		}
	}
}
