package dagrun

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"testing"
)

func TestPrepareGitSourceGraph(t *testing.T) {
	src := pipeline.Stage{ID: "src", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Type: "git_source"}}}
	build := pipeline.Stage{ID: "build", Kind: pipeline.KindBuild, Jobs: []pipeline.Job{{ID: "job", Type: "script"}}}
	tests := []struct {
		name   string
		stages []pipeline.Stage
		deps   map[string][]string
		valid  bool
	}{
		{"linear", []pipeline.Stage{src, build}, map[string][]string{"src": nil, "build": {"src"}}, true},
		{"parallel without source", []pipeline.Stage{src, build}, map[string][]string{"src": nil, "build": nil}, false},
		{"allows source failure", []pipeline.Stage{func() pipeline.Stage { s := src; s.AllowFailure = true; return s }(), build}, map[string][]string{"src": nil, "build": {"src"}}, false},
		{"source post", []pipeline.Stage{func() pipeline.Stage { s := src; s.Post = []pipeline.PostStep{{}}; return s }(), build}, map[string][]string{"src": nil, "build": {"src"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := prepareGitSource(context.Background(), &run.Run{}, tc.stages, tc.deps, false)
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
			if err == nil {
				if _, ok := run.SourceFromContext(ctx); !ok {
					t.Fatal("missing source context")
				}
			}
		})
	}
}
