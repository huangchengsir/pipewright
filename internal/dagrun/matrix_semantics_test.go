package dagrun

import (
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"testing"
)

func matrixSemanticStage() pipeline.Stage {
	return pipeline.Stage{ID: "matrix", Name: "Matrix", Kind: pipeline.KindBuild, Needs: []string{"source"}, Gate: true, AllowFailure: true,
		When:     pipeline.When{Branches: []string{"main"}, Events: []string{"manual"}},
		Matrix:   map[string][]string{"go": {"1.21", "1-21"}, "os": {"linux", "windows"}},
		Post:     []pipeline.PostStep{{Condition: "always", Image: "post", Commands: []string{"cleanup"}}},
		Services: []pipeline.ServiceSpec{{Name: "testdb", Image: "service", Env: []string{"MARKER=source"}, Ports: []string{"8080"}}},
		Jobs:     []pipeline.Job{{ID: "a", Name: "A", Type: "script", Config: map[string]any{"nested": map[string]any{"values": []any{map[string]any{"key": "source"}}}, "typed": map[string][]string{"keys": {"source"}}}}, {ID: "b", Name: "B", Type: "script", Needs: []string{"a"}, Config: map[string]any{"commands": []string{"run"}}}},
	}
}
func TestMatrixPreservesStageSemanticsAndLocalJobNeeds(t *testing.T) {
	input := matrixSemanticStage()
	cells := ExpandMatrix([]pipeline.Stage{input})
	if len(cells) != 4 {
		t.Fatalf("cells=%d", len(cells))
	}
	for _, cell := range cells {
		if cell.Matrix != nil || !cell.Gate || !cell.AllowFailure || cell.When.Branches[0] != "main" {
			t.Fatalf("metadata lost: %+v", cell)
		}
		if len(cell.Post) != 1 || len(cell.Services) != 1 {
			t.Fatalf("post/services lost: %+v", cell)
		}
		if len(cell.Jobs[1].Needs) != 1 || cell.Jobs[1].Needs[0] != cell.Jobs[0].ID {
			t.Fatalf("cross-cell or stale needs: %+v", cell.Jobs)
		}
		if cell.Jobs[0].ID == "a" || cell.Jobs[1].ID == "b" {
			t.Fatal("job IDs were not scoped")
		}
	}
}
func TestMatrixMutableValuesAreIndependent(t *testing.T) {
	input := matrixSemanticStage()
	cells := ExpandMatrix([]pipeline.Stage{input})
	a, b := &cells[0], &cells[1]
	a.Jobs[0].Config["nested"].(map[string]any)["values"].([]any)[0].(map[string]any)["key"] = "changed"
	a.Jobs[0].Config["typed"].(map[string][]string)["keys"][0] = "changed"
	if b.Jobs[0].Config["typed"].(map[string][]string)["keys"][0] != "source" || input.Jobs[0].Config["nested"].(map[string]any)["values"].([]any)[0].(map[string]any)["key"] != "source" {
		t.Fatal("nested config aliases source or other cell")
	}
	a.Jobs[1].Needs[0] = "changed"
	if input.Jobs[1].Needs[0] != "a" || b.Jobs[1].Needs[0] != b.Jobs[0].ID {
		t.Fatal("job needs alias")
	}
	if len(a.Post) == 0 || len(a.Services) == 0 {
		t.Fatal("post/services lost")
	}
	a.Post[0].Commands[0] = "changed"
	a.Services[0].Env[0] = "changed"
	a.Services[0].Ports[0] = "changed"
	a.When.Branches[0] = "changed"
	a.When.Events[0] = "changed"
	a.Needs[0] = "changed"
	if input.Post[0].Commands[0] != "cleanup" || b.Post[0].Commands[0] != "cleanup" || input.Services[0].Env[0] != "MARKER=source" || b.Services[0].Ports[0] != "8080" || input.When.Branches[0] != "main" || b.When.Events[0] != "manual" || b.Needs[0] != "source" {
		t.Fatal("stage mutable values alias")
	}
}
func TestMatrixPostOnlyStageRetainsPost(t *testing.T) {
	input := matrixSemanticStage()
	input.Jobs = nil
	cells := ExpandMatrix([]pipeline.Stage{input})
	for _, cell := range cells {
		if len(cell.Jobs) != 0 || len(cell.Post) != 1 {
			t.Fatalf("post-only lost: %+v", cell)
		}
	}
}
