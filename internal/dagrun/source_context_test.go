package dagrun

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

func gitContextStages() (pipeline.Stage, pipeline.Stage) {
	return pipeline.Stage{ID: "src", Name: "source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "checkout", Type: "git_source"}}},
		pipeline.Stage{ID: "build", Name: "build", Kind: pipeline.KindBuild, Jobs: []pipeline.Job{{ID: "job", Name: "compile", Type: "script"}}}
}

func TestPrepareGitSourceGraph(t *testing.T) {
	src, build := gitContextStages()
	changed := func(f func(*pipeline.Stage)) pipeline.Stage { st := src; f(&st); return st }
	tests := []struct {
		name   string
		stages []pipeline.Stage
		valid  bool
	}{
		{"implicit linear", []pipeline.Stage{src, build}, true},
		{"explicit transitive", []pipeline.Stage{src, {ID: "middle", Needs: []string{"src"}}, {ID: "last", Needs: []string{"middle"}}}, true},
		{"downstream matrix", []pipeline.Stage{src, {ID: "matrix", Kind: pipeline.KindBuild, Needs: []string{"src"}, Matrix: map[string][]string{"os": {"a", "b"}}, Jobs: build.Jobs}}, true},
		{"source matrix", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Matrix = map[string][]string{"os": {"a", "b"}} }), build}, false},
		{"single-cell source matrix", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Matrix = map[string][]string{"os": {"a"}} }), build}, false},
		{"single-cell multi-axis source matrix", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Matrix = map[string][]string{"os": {"a"}, "arch": {"b"}} }), build}, false},
		{"frozen source matrix cell", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.MatrixParentID = "original-source" }), build}, false},
		{"missing source", []pipeline.Stage{build}, false},
		{"two sources", []pipeline.Stage{src, changed(func(s *pipeline.Stage) { s.ID = "other" })}, false},
		{"source without jobs", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Jobs = nil }), build}, false},
		{"non git source", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Jobs = build.Jobs }), build}, false},
		{"multiple jobs", []pipeline.Stage{changed(func(s *pipeline.Stage) {
			s.Jobs = append(s.Jobs, pipeline.Job{ID: "extra", Type: "script", Needs: []string{"git"}})
		}), build}, false},
		{"source gate", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Gate = true }), build}, false},
		{"source post", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Post = []pipeline.PostStep{{}} }), build}, false},
		{"source services", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.Services = []pipeline.ServiceSpec{{Name: "db", Image: "fixture"}} }), build}, false},
		{"source allow failure", []pipeline.Stage{changed(func(s *pipeline.Stage) { s.AllowFailure = true }), build}, false},
		{"source prerequisite", []pipeline.Stage{{ID: "bootstrap"}, changed(func(s *pipeline.Stage) { s.Needs = []string{"bootstrap"} }), build}, false},
		{"independent branch", []pipeline.Stage{src, {ID: "build", Needs: []string{"src"}}, {ID: "independent"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stages := ExpandMatrix(tc.stages)
			ctx, reason := prepareGitSource(context.Background(), &run.Run{}, stages, pipeline.StageDependencies(stages), false)
			_, enabled := run.SourceFromContext(ctx)
			if enabled != tc.valid || (reason == "") != tc.valid {
				t.Fatalf("enabled=%v reason=%q", enabled, reason)
			}
		})
	}
}

func TestPrepareGitSourceRecoveryUsesSerializedDefaultBranch(t *testing.T) {
	src, build := gitContextStages()
	data, err := json.Marshal(project.Project{DefaultBranch: "release/default"})
	if err != nil {
		t.Fatal(err)
	}
	var projectMap map[string]any
	if err = json.Unmarshal(data, &projectMap); err != nil {
		t.Fatal(err)
	}

	for name, captured := range map[string]map[string]any{
		"project serialization":   projectMap,
		"JSON defaultBranch":      {"defaultBranch": "release/default"},
		"canonical key preferred": {"defaultBranch": "release/default", "DefaultBranch": "old-default"},
		"empty canonical key":     {"defaultBranch": "  ", "DefaultBranch": "release/default"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, branch := range []string{"", "manual/ref"} {
				t.Run(branch, func(t *testing.T) {
					sha := strings.Repeat("a", 40)
					ex := r3FrozenExecution{snapshot: runretry.Snapshot{Project: captured, CommitSHA: sha}}
					ctx, reason := prepareGitSource(runretry.WithExecution(context.Background(), ex), &run.Run{Trigger: run.Trigger{Branch: branch}}, []pipeline.Stage{src, build}, map[string][]string{"src": nil, "build": {"src"}}, true)
					source, enabled := run.SourceFromContext(ctx)
					want := branch
					if want == "" {
						want = "release/default"
					}
					if reason != "" || !enabled || source.RefName != want || source.CommitSHA != sha {
						t.Fatalf("source=%+v enabled=%v reason=%q", source, enabled, reason)
					}
				})
			}
		})
	}
}

type sourceCompatibilitySink struct {
	*fakeSink
	t       *testing.T
	planErr error
	planned bool
	notices int
}

func (s *sourceCompatibilitySink) Plan(ctx context.Context, decls []run.StepDecl) error {
	if s.planErr != nil {
		return s.planErr
	}
	s.planned = true
	return s.fakeSink.Plan(ctx, decls)
}
func (s *sourceCompatibilitySink) Log(ctx context.Context, stream string, ordinal int, line string) error {
	if strings.HasPrefix(line, gitSourceCompatibilityPrefix) {
		if !s.planned {
			s.t.Error("compatibility notice preceded successful plan")
		}
		if ordinal < 0 || ordinal >= len(s.fakeSink.planned) {
			s.t.Errorf("notice ordinal=%d", ordinal)
		}
		s.notices++
	}
	return s.fakeSink.Log(ctx, stream, ordinal, line)
}

func TestGitSourceCompatibilityPreservesDAGScheduling(t *testing.T) {
	for _, name := range []string{"source gate", "source gate rejected", "source post", "source services", "multiple jobs", "empty jobs", "non git source", "source matrix", "single-cell source matrix", "source prerequisite", "source allow failure", "independent branch"} {
		t.Run(name, func(t *testing.T) {
			src, build := gitContextStages()
			stages := []pipeline.Stage{src, build}
			failSource := false
			switch name {
			case "source gate", "source gate rejected":
				stages[0].Gate = true
			case "source post":
				stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: "fixture", Commands: []string{"cleanup"}}}
			case "source services":
				stages[0].Services = []pipeline.ServiceSpec{{Name: "db", Image: "fixture"}}
			case "multiple jobs":
				stages[0].Jobs = append(stages[0].Jobs, pipeline.Job{ID: "extra", Type: "script", Needs: []string{"git"}})
			case "empty jobs":
				stages[0].Jobs = nil
			case "non git source":
				stages[0].Jobs = build.Jobs
			case "source matrix":
				stages[0].Matrix = map[string][]string{"os": {"a", "b"}}
			case "single-cell source matrix":
				stages[0].Matrix = map[string][]string{"os": {"a"}}
			case "source prerequisite":
				stages[0].Needs = []string{"bootstrap"}
				stages[1].Needs = []string{"src"}
				stages = append([]pipeline.Stage{{ID: "bootstrap", Name: "bootstrap"}}, stages...)
			case "source allow failure":
				stages[0].AllowFailure = true
				failSource = true
			case "independent branch":
				stages[1].Needs = []string{"src"}
				stages = append(stages, pipeline.Stage{ID: "independent", Name: "independent", Jobs: []pipeline.Job{{ID: "ind", Name: "independent"}}})
				failSource = true
			}
			type observed struct {
				stages  map[string]pipeline.Stage
				gates   []string
				planned []string
				done    map[int]string
				failed  bool
			}
			execute := func(enabled bool) observed {
				got := observed{stages: map[string]pipeline.Stage{}}
				sink := &sourceCompatibilitySink{fakeSink: newFakeSink(), t: t}
				opts := []Option{WithConcurrency(1), WithGate(func(ctx context.Context, _ *run.Run, st pipeline.Stage) (bool, error) {
					if _, present := run.SourceFromContext(ctx); present {
						t.Error("compatibility gate received new source context")
					}
					got.gates = append(got.gates, st.ID)
					return name != "source gate rejected", nil
				}), WithStageExecutor(func(ctx context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
					if _, present := run.SourceFromContext(ctx); present {
						t.Error("compatibility executor received new source context")
					}
					got.stages[st.ID] = st
					if failSource && st.Kind == pipeline.KindSource {
						return errors.New("fixture source failure")
					}
					return nil
				})}
				if enabled {
					opts = append(opts, WithGitSourceContext())
				}
				got.failed = New(&fakeLoader{cfg: cfgWith(stages...)}, opts...).Run(context.Background(), &run.Run{}, sink) != nil
				wantNotices := 0
				if enabled {
					wantNotices = 1
				}
				if sink.notices != wantNotices {
					t.Fatalf("notices=%d want=%d", sink.notices, wantNotices)
				}
				got.planned, got.done = sink.fakeSink.planned, sink.done
				return got
			}
			baseline, enabled := execute(false), execute(true)
			if !reflect.DeepEqual(enabled, baseline) {
				t.Fatalf("enabled=%+v baseline=%+v", enabled, baseline)
			}
			if name == "source allow failure" && enabled.stages["build"].ID != "build" {
				t.Fatal("allowed source failure blocked downstream")
			}
			if name == "independent branch" {
				if enabled.stages["independent"].ID == "" || enabled.stages["build"].ID != "" {
					t.Fatal("independent branch or failure blocking changed")
				}
			}
			if name == "source matrix" && len(enabled.stages) != 3 {
				t.Fatal("source matrix cells lost")
			}
		})
	}
}

func TestGitSourceCompatibilityNoticeRequiresSuccessfulPlan(t *testing.T) {
	failure := errors.New("plan unavailable")
	sink := &sourceCompatibilitySink{fakeSink: newFakeSink(), t: t, planErr: failure}
	runner := New(&fakeLoader{cfg: cfgWith(pipeline.Stage{ID: "old", Name: "old"})}, WithGitSourceContext(), WithStageExecutor(func(context.Context, *run.Run, pipeline.Stage, StageReporter) error {
		t.Error("executor ran after plan failure")
		return nil
	}))
	if err := runner.Run(context.Background(), &run.Run{}, sink); !errors.Is(err, failure) || sink.notices != 0 {
		t.Fatalf("err=%v notices=%d", err, sink.notices)
	}
}

func TestGitSourceRecoveryUsesFrozenDependenciesForCompatibility(t *testing.T) {
	src, build := gitContextStages()
	stages := []pipeline.Stage{src, build}
	// No declared needs would imply a linear graph today, while the captured
	// execution has independent roots. A source failure must not skip build.
	snapshot := runretry.Snapshot{Spec: pipeline.Spec{Stages: stages}, Compiled: &runretry.FrozenGraph{Version: runretry.SchemaVersion, Stages: stages, Dependencies: map[string][]string{"src": {}, "build": {}}}}
	for _, enabled := range []bool{false, true} {
		calls := map[string]int{}
		sink := &sourceCompatibilitySink{fakeSink: newFakeSink(), t: t}
		opts := []Option{WithConcurrency(1), WithStageExecutor(func(ctx context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if _, present := run.SourceFromContext(ctx); present {
				t.Error("frozen parallel graph received new source context")
			}
			calls[st.ID]++
			if st.ID == "src" {
				return errors.New("source failure")
			}
			return nil
		})}
		if enabled {
			opts = append(opts, WithGitSourceContext())
		}
		ctx := runretry.WithExecution(context.Background(), r3FrozenExecution{snapshot: snapshot})
		if err := New(&fakeLoader{}, opts...).Run(ctx, &run.Run{}, sink); err == nil || calls["src"] != 1 || calls["build"] != 1 {
			t.Fatalf("frozen independent graph changed: calls=%v err=%v", calls, err)
		}
		if enabled && sink.notices != 1 {
			t.Fatalf("notices=%d", sink.notices)
		}
	}
}

func TestGitSourceCompatibilityMasksInheritedSourceContext(t *testing.T) {
	sha := strings.Repeat("a", 40)
	parent := run.WithSourceContext(context.Background(), run.SourceContext{CommitSHA: sha})
	ctx, reason := prepareGitSource(parent, &run.Run{}, []pipeline.Stage{{ID: "old"}}, map[string][]string{"old": nil}, false)
	if _, present := run.SourceFromContext(ctx); present || reason == "" {
		t.Fatal("compatibility inherited a source binding")
	}
	if err := run.BindSourceContext(ctx, run.SourceContext{CommitSHA: strings.Repeat("b", 40)}); !errors.Is(err, run.ErrSourceUnavailable) {
		t.Fatal(err)
	}
	if source, present := run.SourceFromContext(parent); !present || source.CommitSHA != sha {
		t.Fatal("parent binding was changed")
	}
}
