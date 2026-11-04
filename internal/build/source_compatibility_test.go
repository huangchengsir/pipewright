package build

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

type sourceCompatibilityDriver struct {
	*defaultsDriver
	serviceMu         sync.Mutex
	services, stopped int
	networks, removed int
}

func (d *sourceCompatibilityDriver) CreateNetwork(context.Context, string, func(string, string)) (int, error) {
	d.serviceMu.Lock()
	defer d.serviceMu.Unlock()
	d.networks++
	return 0, nil
}
func (d *sourceCompatibilityDriver) RemoveNetwork(context.Context, string, func(string, string)) (int, error) {
	d.serviceMu.Lock()
	defer d.serviceMu.Unlock()
	d.removed++
	return 0, nil
}
func (d *sourceCompatibilityDriver) RunService(context.Context, string, string, string, string, []string, []string, func(string, string)) (int, error) {
	d.serviceMu.Lock()
	defer d.serviceMu.Unlock()
	d.services++
	return 0, nil
}
func (d *sourceCompatibilityDriver) StopService(context.Context, string, func(string, string)) (int, error) {
	d.serviceMu.Lock()
	defer d.serviceMu.Unlock()
	d.stopped++
	return 0, nil
}

func TestGitSourceCompatibilityKeepsBuilderFeatures(t *testing.T) {
	for _, name := range []string{"gate", "post", "services", "multiple jobs", "source matrix", "single-cell source matrix", "independent branch"} {
		t.Run(name, func(t *testing.T) {
			source := sourceStage()
			build := scriptStage(scriptJob("compile", "compile", "true"))
			build.ID, build.Jobs[0].ID = "build", "compile"
			stages := []pipeline.Stage{source, build}
			switch name {
			case "gate":
				stages[0].Gate = true
			case "post":
				stages[0].Post = []pipeline.PostStep{{Condition: pipeline.PostAlways, Image: "post", Commands: []string{"cleanup"}}}
			case "services":
				stages[0].Services = []pipeline.ServiceSpec{{Name: "db", Image: "fixture"}}
			case "multiple jobs":
				extra := scriptJob("source-script", "source-script", "true")
				extra.ID, extra.Needs = "source-script", []string{"git"}
				stages[0].Jobs = append(stages[0].Jobs, extra)
			case "source matrix":
				stages[0].Matrix = map[string][]string{"os": {"a", "b"}}
			case "single-cell source matrix":
				stages[0].Matrix = map[string][]string{"os": {"a"}}
			case "independent branch":
				stages[1].Needs = []string{"src"}
				other := scriptStage(scriptJob("independent", "independent", "true"))
				other.ID, other.Jobs[0].ID = "independent", "independent"
				stages = append(stages, other)
			}
			type observation struct {
				images                                              []string
				services, stopped, networks, removed, gates, clones int
			}
			execute := func(enabled bool) observation {
				driver := &sourceCompatibilityDriver{defaultsDriver: &defaultsDriver{}}
				cloner := &sourceRecordingCloner{actual: checkoutTestSHA}
				b := newDAGTestBuilder(driver, cloner)
				b.bindSourceCommit = func(context.Context, string, string) error {
					t.Error("compatibility attempted new source binding")
					return errors.New("unexpected source binding")
				}
				loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
					return &pipeline.Config{Spec: pipeline.Spec{Stages: stages}}, nil
				})
				got := observation{}
				opts := []dagrun.Option{dagrun.WithConcurrency(1), dagrun.WithStageExecutor(NewStageExecutor(b, nil)), dagrun.WithGate(func(ctx context.Context, _ *run.Run, _ pipeline.Stage) (bool, error) {
					if _, present := run.SourceFromContext(ctx); present {
						t.Error("old gate received automatic Git context")
					}
					got.gates++
					return true, nil
				})}
				if enabled {
					opts = append(opts, dagrun.WithGitSourceContext())
				}
				sink := &retryEvidenceSink{}
				if err := dagrun.New(loader, opts...).Run(context.Background(), &run.Run{ID: "compatibility", ProjectID: "p1"}, sink); err != nil {
					t.Fatal(err)
				}
				for _, call := range driver.calls {
					got.images = append(got.images, call.image)
					for _, env := range call.env {
						if strings.HasPrefix(env, "CI_COMMIT_SHA=") || strings.HasPrefix(env, "PIPELINE_ID=") {
							t.Errorf("compatibility injected new context: %q", env)
						}
					}
				}
				sort.Strings(got.images)
				got.services, got.stopped, got.networks, got.removed, got.clones = driver.services, driver.stopped, driver.networks, driver.removed, len(cloner.commits)
				notices := 0
				for _, line := range sink.logs {
					if strings.HasPrefix(line, "Git context compatibility mode:") {
						notices++
					}
				}
				wantNotices := 0
				if enabled {
					wantNotices = 1
				}
				if notices != wantNotices {
					t.Fatalf("notices=%d want=%d", notices, wantNotices)
				}
				return got
			}
			baseline, enabled := execute(false), execute(true)
			if !reflect.DeepEqual(enabled, baseline) {
				t.Fatalf("new=%+v baseline=%+v", enabled, baseline)
			}
			wantImages := []string{"compile"}
			if name == "post" {
				wantImages = append(wantImages, "post")
			}
			if name == "multiple jobs" {
				wantImages = append(wantImages, "source-script")
			}
			if name == "independent branch" {
				wantImages = append(wantImages, "independent")
			}
			sort.Strings(wantImages)
			if !reflect.DeepEqual(enabled.images, wantImages) {
				t.Fatalf("script/post jobs lost: %+v", enabled)
			}
			if name == "services" && (enabled.services != 1 || enabled.stopped != 1 || enabled.networks != 1 || enabled.removed != 1) {
				t.Fatalf("service lifecycle lost: %+v", enabled)
			}
			if name == "gate" && enabled.gates != 1 {
				t.Fatal("source approval omitted")
			}
		})
	}
}

type sequentialSourceCloner struct {
	results []*CloneResolved
	err     error
	calls   int
}

func (c *sequentialSourceCloner) Clone(context.Context, string, string, string, string, string, string) (*CloneResolved, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	index := c.calls - 1
	if index >= len(c.results) {
		index = len(c.results) - 1
	}
	return c.results[index], nil
}

func TestEligibleGitSourceFailureNeverEntersCompatibility(t *testing.T) {
	good := &CloneResolved{CommitFull: checkoutTestSHA, CommitTitle: "fixture", CommitAuthor: "author", MetadataReady: true}
	for _, name := range []string{"resolution", "invalid SHA", "persistence", "missing metadata", "downstream checkout"} {
		t.Run(name, func(t *testing.T) {
			cloner := &sequentialSourceCloner{results: []*CloneResolved{good}}
			var bindErr error
			switch name {
			case "resolution":
				cloner.err = errors.New("cannot resolve ref")
			case "invalid SHA":
				cloner.results = []*CloneResolved{{CommitFull: "abc"}}
			case "persistence":
				bindErr = errors.New("commit persistence unavailable")
			case "missing metadata":
				cloner.results = []*CloneResolved{{CommitFull: checkoutTestSHA}}
			case "downstream checkout":
				cloner.results = []*CloneResolved{good, {CommitFull: strings.Repeat("b", 40), MetadataReady: true}}
			}
			driver := &defaultsDriver{}
			b := newDAGTestBuilder(driver, cloner)
			b.bindSourceCommit = func(context.Context, string, string) error { return bindErr }
			source := sourceStage()
			build := scriptStage(scriptJob("compile", "compile", "true"))
			build.ID, build.Jobs[0].ID, build.Needs = "build", "compile", []string{"src"}
			deploy := pipeline.Stage{ID: "deploy", Kind: pipeline.KindDeploy, Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "kube", Type: pipeline.KubernetesJobType}}}
			stages := []pipeline.Stage{source, build, deploy}
			loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
				return &pipeline.Config{Spec: pipeline.Spec{Stages: stages}}, nil
			})
			prepared := 0
			runner := dagrun.New(loader, dagrun.WithGitSourceContext(), dagrun.WithStageExecutor(NewStageExecutor(b, nil)), dagrun.WithStagePreparation(func(_ context.Context, _ *run.Run, st pipeline.Stage) (dagrun.StagePreparation, error) {
				if st.ID == "deploy" {
					prepared++
				}
				return dagrun.StagePreparation{}, nil
			}))
			sink := &retryEvidenceSink{}
			if err := runner.Run(context.Background(), &run.Run{ID: "strict", ProjectID: "p1"}, sink); err == nil {
				t.Fatal("source failure was accepted")
			}
			if prepared != 0 || len(driver.calls) != 0 {
				t.Fatal("source failure reached script or Kubernetes preparation")
			}
			wantClones := 1
			if name == "downstream checkout" {
				wantClones = 2
			}
			if cloner.calls != wantClones {
				t.Fatalf("unexpected retry/fallback clone: %d", cloner.calls)
			}
			for _, line := range sink.logs {
				if strings.HasPrefix(line, "Git context compatibility mode:") {
					t.Fatal("runtime source failure selected compatibility")
				}
			}
		})
	}
}
