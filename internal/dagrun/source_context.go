package dagrun

import (
	"context"
	"fmt"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

// WithGitSourceContext enables strict source binding for the real DAG executor.
func WithGitSourceContext() Option { return func(r *Runner) { r.gitSourceContext = true } }

func prepareGitSource(ctx context.Context, rn *run.Run, stages []pipeline.Stage, deps map[string][]string, recovering bool) (context.Context, error) {
	var source string
	for _, st := range stages {
		if st.Kind != pipeline.KindSource {
			continue
		}
		if source != "" || len(st.Jobs) != 1 || st.Jobs[0].Type != "git_source" || st.AllowFailure || st.Gate || len(st.Post) != 0 || len(st.Services) != 0 || len(deps[st.ID]) != 0 {
			return nil, fmt.Errorf("dagrun: source stage must contain exactly one git_source, without dependencies, gate, allowFailure, post or services")
		}
		source = st.ID
	}
	if source == "" {
		return nil, fmt.Errorf("dagrun: missing source stage")
	}
	var reaches func(string, map[string]bool) bool
	reaches = func(id string, seen map[string]bool) bool {
		if id == source {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, dep := range deps[id] {
			if reaches(dep, seen) {
				return true
			}
		}
		return false
	}
	for _, st := range stages {
		if !reaches(st.ID, map[string]bool{}) {
			return nil, fmt.Errorf("dagrun: stage %s must depend on the source stage", st.ID)
		}
	}
	initial := run.SourceContext{RefName: strings.TrimSpace(rn.Trigger.Branch)}
	if recovering {
		if ex, ok := runretry.ExecutionFrom(ctx); ok {
			snap := ex.Snapshot()
			initial.CommitSHA = snap.CommitSHA
			if initial.RefName == "" {
				initial.RefName, _ = snap.Project["DefaultBranch"].(string)
			}
		}
	}
	return run.WithSourceContext(ctx, initial), nil
}
