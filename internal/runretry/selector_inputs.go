package runretry

import (
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"io"
	"strings"
)

type artifactSelector struct {
	StageID          string `json:"stageId"`
	JobID            string `json:"jobId"`
	DeclarationIndex *int   `json:"declarationIndex"`
}

func (e *execution) selectedProducer(n NodeRef) (NodeRef, int, string, bool, error) {
	var job pipeline.Job
	for _, stage := range e.l.graph.Stages {
		if stage.ID == n.ExecutionStageID {
			for _, candidate := range stage.Jobs {
				if candidate.ID == n.ExecutionJobID {
					job = candidate
				}
			}
		}
	}
	if job.Config["__matrixArtifactSourceAmbiguous"] == true {
		return NodeRef{}, 0, "", true, ErrInvalid
	}
	text, _ := job.Config["artifactSource"].(string)
	if strings.TrimSpace(text) == "" {
		return NodeRef{}, 0, "", false, nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var selector artifactSelector
	if decoder.Decode(&selector) != nil || decoder.Decode(new(any)) != io.EOF || selector.StageID == "" || selector.JobID == "" || selector.DeclarationIndex == nil || *selector.DeclarationIndex < 0 {
		return NodeRef{}, 0, "", true, ErrInvalid
	}
	var producer NodeRef
	for _, candidate := range e.l.graph.Nodes {
		if candidate.ControlKey == "" && candidate.ExecutionStageID == selector.StageID && candidate.ExecutionJobID == selector.JobID {
			if producer.Key != "" {
				return NodeRef{}, 0, "", true, ErrInvalid
			}
			producer = candidate
		}
	}
	if producer.Key == "" {
		return producer, 0, "", true, ErrUnavailable
	}
	// A selector can narrow data ancestry, never add a new dependency/permission.
	incoming := map[string][]string{}
	for _, edge := range e.l.graph.Edges {
		if edge.Kind == "file" || edge.Kind == "image" {
			incoming[edge.To] = append(incoming[edge.To], edge.From)
		}
	}
	seen := map[string]bool{}
	queue := append([]string(nil), incoming[n.Key]...)
	allowed := false
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if seen[key] {
			continue
		}
		seen[key] = true
		if key == producer.Key {
			allowed = true
			break
		}
		queue = append(queue, incoming[key]...)
	}
	if !allowed {
		return producer, 0, "", true, ErrBlocked
	}
	prefer, _ := job.Config["artifactType"].(string)
	return producer, *selector.DeclarationIndex, strings.TrimSpace(prefer), true, nil
}
func declarationMatches(metadata map[string]any, want int) bool {
	switch value := metadata["declarationIndex"].(type) {
	case int:
		return value == want
	case int64:
		return value == int64(want)
	case float64:
		return value == float64(want)
	case json.Number:
		n, err := value.Int64()
		return err == nil && n == int64(want)
	}
	return false
}
