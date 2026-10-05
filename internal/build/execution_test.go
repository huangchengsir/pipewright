package build

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type evidenceReporter struct{ run.ExecutionEvidence }

func TestNotificationEvidenceRequiresEnabledDelivery(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			n := notify.New(storetest.OpenDB(t), nil, srv.Client())
			ch, err := n.Create(context.Background(), notify.CreateInput{Name: "test", Type: notify.TypeWebhook, Enabled: enabled, Config: notify.Config{URL: srv.URL}})
			if err != nil {
				t.Fatal(err)
			}
			b := newDAGTestBuilder(&imgDriver{}, &markerCloner{})
			b.notifier = n
			rep := &evidenceReporter{}
			b.runNotifyJob(context.Background(), rep, pipeline.Job{Type: "notify", Config: map[string]any{"channel": ch.ID}}, &run.Run{})
			want, calls := run.ExecutionStub, 0
			if enabled {
				want, calls = run.ExecutionReal, 1
			}
			if rep.Mode() != want || hits != calls {
				t.Fatalf("mode %s, hits %d; want %s, %d", rep.Mode(), hits, want, calls)
			}
		})
	}
}

func (*evidenceReporter) Log(context.Context, string, string) error        { return nil }
func (*evidenceReporter) EmitArtifact(context.Context, run.Artifact) error { return nil }
func (*evidenceReporter) JobRunning(context.Context, string) error         { return nil }
func (*evidenceReporter) JobDone(context.Context, string, string) error    { return nil }
func (r *evidenceReporter) JobReporter(string) dagrun.StageReporter        { return r }

func TestLocalExecutionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		stage      pipeline.Stage
		fail       bool
	}{
		{name: "empty", want: run.ExecutionPending, stage: scriptStage()},
		{name: "source", want: run.ExecutionPending, stage: scriptStage(pipeline.Job{Type: "git_source"})},
		{name: "push_marker", want: run.ExecutionPending, stage: scriptStage(pipeline.Job{Type: "push_image"})},
		{name: "unknown", want: run.ExecutionStub, stage: scriptStage(pipeline.Job{Type: "future_task"})},
		{name: "missing_deployer", want: run.ExecutionStub, stage: scriptStage(pipeline.Job{Type: "deploy_ssh"})},
		{name: "missing_notifier", want: run.ExecutionStub, stage: scriptStage(pipeline.Job{Type: "notify"})},
		{name: "script", want: run.ExecutionReal, stage: scriptStage(scriptJob("script", "alpine", "true"))},
		{name: "image", want: run.ExecutionReal, stage: scriptStage(pipeline.Job{Type: "build_image"})},
		{name: "invalid_script", want: run.ExecutionPending, stage: scriptStage(scriptJob("script", "", "true")), fail: true},
		{name: "grouped_mixed", want: run.ExecutionMixed, stage: scriptStage(scriptJob("script", "alpine", "true"), pipeline.Job{Type: "future_task"})},
		{name: "dag_mixed", want: run.ExecutionMixed, stage: scriptStage(scriptJobID("script", "alpine"), pipeline.Job{ID: "future", Type: "future_task"})},
		{name: "dag_source_marker", want: run.ExecutionReal, stage: scriptStage(scriptJobID("script", "alpine"), pipeline.Job{ID: "source", Type: "git_source"})},
		{name: "post_only", want: run.ExecutionReal, stage: pipeline.Stage{ID: "post", Post: []pipeline.PostStep{{Condition: pipeline.PostOnSuccess, Image: "alpine", Commands: []string{"true"}}}}},
		{name: "post_condition_skipped", want: run.ExecutionPending, stage: pipeline.Stage{ID: "post", Post: []pipeline.PostStep{{Condition: pipeline.PostOnFailure, Image: "alpine", Commands: []string{"true"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newDAGTestBuilder(&imgDriver{}, &markerCloner{})
			rep := &evidenceReporter{}
			err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1"}, tc.stage, rep)
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v, expected failure %v", err, tc.fail)
			}
			if got := rep.Mode(); got != tc.want {
				t.Fatalf("mode = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRemoteExecutionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		stage      pipeline.Stage
		local      bool
	}{
		{name: "empty", want: run.ExecutionPending, stage: scriptStage()},
		{name: "source", want: run.ExecutionPending, stage: scriptStage(pipeline.Job{Type: "git_source"})},
		{name: "unknown", want: run.ExecutionStub, stage: scriptStage(pipeline.Job{Type: "build_image"})},
		{name: "script", want: run.ExecutionReal, stage: scriptStage(scriptJob("script", "alpine", "true"))},
		{name: "mixed_omission", want: run.ExecutionMixed, stage: scriptStage(scriptJob("script", "alpine", "true"), pipeline.Job{Type: "deploy_ssh"})},
		{name: "source_and_push_markers", want: run.ExecutionReal, stage: scriptStage(scriptJob("script", "alpine", "true"), pipeline.Job{Type: "git_source"}, pipeline.Job{Type: "push_image"})},
		{name: "post_omitted", want: run.ExecutionMixed, stage: postStage("alpine", postSet)},
		{name: "post_condition_skipped", want: run.ExecutionReal, stage: postStage("alpine", []pipeline.PostStep{{Condition: pipeline.PostOnFailure, Image: "alpine", Commands: []string{"true"}}})},
		{name: "post_only_omitted", want: run.ExecutionStub, stage: pipeline.Stage{ID: "post", Post: []pipeline.PostStep{{Condition: pipeline.PostAlways, Image: "alpine", Commands: []string{"true"}}}}},
		{name: "report_omitted", want: run.ExecutionMixed, stage: scriptStage(pipeline.Job{Type: "script", Config: map[string]any{"image": "alpine", "commands": "true", "testReport": "junit", "reportPath": "junit.xml", "gateMaxFailures": "0"}})},
		{name: "services_omitted", want: run.ExecutionMixed, stage: pipeline.Stage{ID: "services", Jobs: []pipeline.Job{scriptJob("script", "alpine", "true")}, Services: []pipeline.ServiceSpec{{Name: "db", Image: "redis"}}}},
		{name: "fallback_local", want: run.ExecutionReal, stage: scriptStage(scriptJob("script", "alpine", "true")), local: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newRemoteTestBuilder(&imgDriver{})
			lookup := fakeRunnerLookup{serverID: "remote"}
			if tc.local {
				lookup.serverID = ""
			}
			rep := &evidenceReporter{}
			if err := NewStageExecutorWithRunner(b, nil, lookup, &fakeRemoteTarget{})(context.Background(), &run.Run{ID: "r", ProjectID: "p1"}, tc.stage, rep); err != nil {
				t.Fatal(err)
			}
			if got := rep.Mode(); got != tc.want {
				t.Fatalf("mode = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestScriptCanceledBeforeInvocationStaysPending(t *testing.T) {
	drv := &recordingDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	rep := &evidenceReporter{}
	sink := &reporterSink{rep: rep}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := b.scriptEvidenceBuilder(sink).runScriptStepWithOpts(ctx, sink, 0, pipeline.PipelineStep{Image: "alpine", Commands: []string{"true"}}, t.TempDir())
	if err == nil || drv.callCount != 0 || rep.Mode() != run.ExecutionPending {
		t.Fatalf("canceled: %v, calls %d, mode %s", err, drv.callCount, rep.Mode())
	}
}

func TestLegacyBuilderExecutionEvidence(t *testing.T) {
	b := newDAGTestBuilder(&imgDriver{}, &markerCloner{})
	b.settings = fakeSettings{settings: &pipeline.Settings{Build: pipeline.BuildConfig{ArtifactType: pipeline.ArtifactImage}}}
	rep := &evidenceReporter{}
	if err := b.Run(context.Background(), &run.Run{ProjectID: "p1"}, &reporterSink{rep: rep}); err != nil {
		t.Fatal(err)
	}
	if rep.Mode() != run.ExecutionReal {
		t.Fatal(rep.Mode())
	}
}
