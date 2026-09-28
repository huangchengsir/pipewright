package deploy

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/huangchengsir/pipewright/internal/run"
)

var (
	ErrArtifactSourceInvalid   = errors.New("deploy: invalid artifact source")
	ErrArtifactSourceAmbiguous = errors.New("deploy: artifact source matched multiple artifacts")
)

type stageArtifactSource struct {
	StageID          string `json:"stageId"`
	JobID            string `json:"jobId"`
	DeclarationIndex *int   `json:"declarationIndex"`
}

// Explicit source identity is stable across runs; an artifact ID is not.
func selectStageArtifact(arts []run.Artifact, prefer, sourceJSON string) (*run.Artifact, error) {
	prefer = strings.ToLower(strings.TrimSpace(prefer))
	if sourceJSON == "" {
		return pickStageArtifact(arts, prefer), nil
	}
	var source stageArtifactSource
	decoder := json.NewDecoder(strings.NewReader(sourceJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil || strings.TrimSpace(source.StageID) == "" || strings.TrimSpace(source.JobID) == "" || source.DeclarationIndex == nil || *source.DeclarationIndex < 0 {
		return nil, ErrArtifactSourceInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrArtifactSourceInvalid
	}
	var selected *run.Artifact
	for i := range arts {
		a := &arts[i]
		if !deployableStageArtifact(*a) || (prefer != "" && a.Type != prefer) {
			continue
		}
		index, ok := artifactDeclarationIndex(a.Metadata["declarationIndex"])
		if !ok || index != *source.DeclarationIndex || a.Metadata["sourceStageId"] != source.StageID || a.Metadata["sourceJobId"] != source.JobID {
			continue
		}
		if selected != nil {
			return nil, ErrArtifactSourceAmbiguous
		}
		selected = a
	}
	if selected == nil {
		return nil, ErrArtifactNotFound
	}
	return selected, nil
}

func artifactDeclarationIndex(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		return int(n), int64(int(n)) == n
	case float64:
		return int(n), float64(int(n)) == n
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil && int64(int(i)) == i
	default:
		return 0, false
	}
}
