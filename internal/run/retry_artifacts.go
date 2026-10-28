package run

import (
	"context"
	"sort"

	"github.com/huangchengsir/pipewright/internal/runretry"
)

// ListArtifacts projects proven inherited user artifacts without inserting new
// rows or changing their original ID/time. A task-scoped caller receives only
// the references permitted by that task's frozen inputs.
func (s *service) ListArtifacts(ctx context.Context, id string) ([]Artifact, error) {
	own, err := s.listOwnArtifacts(ctx, id)
	if err != nil {
		return nil, err
	}
	out := append([]Artifact{}, own...)
	if recovery, ok := s.retryRuntime.(interface {
		ArtifactSources(context.Context, string) ([]runretry.ArtifactSource, error)
	}); ok {
		sources, err := recovery.ArtifactSources(ctx, id)
		if err != nil {
			if _, strict := runretry.NodeFrom(ctx); strict {
				return nil, err
			}
			// A history/read view can still show its own immutable user artifacts.
			// No unverified inherited source is guessed when recovery evidence is lost.
			return out, nil
		}
		loaded := map[string][]Artifact{}
		seen := map[string]bool{}
		for _, a := range out {
			seen[a.ID] = true
		}
		for _, source := range sources {
			artifacts, ok := loaded[source.RunID]
			if !ok {
				artifacts, err = s.listOwnArtifacts(ctx, source.RunID)
				if err != nil {
					return nil, err
				}
				loaded[source.RunID] = artifacts
			}
			refs := map[string]bool{}
			for _, ref := range source.References {
				refs[ref] = true
			}
			for _, a := range artifacts {
				key, _ := a.Metadata["logicalNodeKey"].(string)
				if seen[a.ID] || key != source.NodeKey || !refs[a.Reference] {
					continue
				}
				a.SourceRunID = source.RunID
				a.SourceNodeKey = source.NodeKey
				seen[a.ID] = true
				out = append(out, a)
			}
		}
	}
	if node, ok := runretry.NodeFrom(ctx); ok {
		ex, present := runretry.ExecutionFrom(ctx)
		if !present {
			return nil, runretry.ErrInvalid
		}
		inputs, err := ex.Inputs(ctx, node)
		if err != nil {
			return nil, err
		}
		permitted := map[string]bool{}
		for _, f := range inputs.Files {
			permitted[f.ProducerAttemptID+"\x00"+f.ProducerNodeKey+"\x00"+f.Key] = true
		}
		for _, image := range inputs.Images {
			permitted[image.ProducerAttemptID+"\x00"+image.ProducerNodeKey+"\x00"+image.Ref] = true
		}
		filtered := make([]Artifact, 0, len(out))
		for _, a := range out {
			producer, _ := a.Metadata["logicalNodeKey"].(string)
			if permitted[a.RunID+"\x00"+producer+"\x00"+a.Reference] {
				filtered = append(filtered, a)
			}
		}
		out = filtered
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}
