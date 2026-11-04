package dagrun

import (
	"context"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

// WithGitSourceContext enables strict source binding for eligible execution graphs.
// Other graphs retain the existing DAG executor and receive a compatibility notice.
func WithGitSourceContext() Option { return func(r *Runner) { r.gitSourceContext = true } }

const gitSourceCompatibilityPrefix = "Git context compatibility mode: "

// Eligibility uses the expanded stages and the same dependencies as scheduling,
// including frozen dependencies on recovery. It never rewrites the user graph.
func gitSourceCompatibilityReason(stages []pipeline.Stage, deps map[string][]string) string {
	var source string
	for _, st := range stages {
		if st.Kind != pipeline.KindSource {
			continue
		}
		if st.MatrixParentID != "" || len(st.Matrix) != 0 {
			return "source matrix"
		}
		if source != "" || len(st.Jobs) != 1 || st.Jobs[0].Type != "git_source" || st.AllowFailure || st.Gate || len(st.Post) != 0 || len(st.Services) != 0 || len(deps[st.ID]) != 0 {
			return "requires one source stage with one git_source and no source dependencies, gate, allowFailure, post or services"
		}
		source = st.ID
	}
	if source == "" {
		return "no source stage"
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
			return "a stage does not depend on the source stage"
		}
	}
	return ""
}

// Only graph ineligibility selects compatibility mode, before any stage starts.
// Once installed, source resolution/binding/checkout errors remain fatal.
func prepareGitSource(ctx context.Context, rn *run.Run, stages []pipeline.Stage, deps map[string][]string, recovering bool) (context.Context, string) {
	if reason := gitSourceCompatibilityReason(stages, deps); reason != "" {
		return run.WithoutSourceContext(ctx), reason
	}
	initial := run.SourceContext{RefName: strings.TrimSpace(rn.Trigger.Branch)}
	if recovering {
		if ex, ok := runretry.ExecutionFrom(ctx); ok {
			snap := ex.Snapshot()
			initial.CommitSHA = snap.CommitSHA
			if initial.RefName == "" {
				branch, _ := snap.Project["defaultBranch"].(string)
				branch = strings.TrimSpace(branch)
				if branch == "" {
					// Historical captures serialized project.Project without JSON tags.
					branch, _ = snap.Project["DefaultBranch"].(string)
				}
				initial.RefName = strings.TrimSpace(branch)
			}
		}
	}
	return run.WithSourceContext(ctx, initial), ""
}
