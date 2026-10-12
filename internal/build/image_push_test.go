package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

type pushCall struct{ op, source, dest, user, pass string }
type pushTestDriver struct {
	Driver
	mu       sync.Mutex
	calls    []pushCall
	failOp   string
	failCode int
	failErr  error
	emit     string
	cancelOp string
	cancel   context.CancelFunc
}

func (*pushTestDriver) Binary() string { return "fake" }
func (d *pushTestDriver) call(c pushCall, line func(string, string)) (int, error) {
	d.mu.Lock()
	d.calls = append(d.calls, c)
	d.mu.Unlock()
	if line != nil && d.emit != "" {
		line(streamStderr, d.emit)
	}
	if c.op == d.cancelOp && d.cancel != nil {
		d.cancel()
	}
	if c.op == d.failOp {
		return d.failCode, d.failErr
	}
	return 0, nil
}
func (d *pushTestDriver) Build(_ context.Context, dir, _ string, tag string, _, _ []string, line func(string, string)) (int, error) {
	return d.call(pushCall{op: "build", source: dir, dest: tag}, line)
}
func (d *pushTestDriver) Login(_ context.Context, host, user, pass string, line func(string, string)) (int, error) {
	return d.call(pushCall{op: "login", dest: host, user: user, pass: pass}, line)
}
func (d *pushTestDriver) Tag(_ context.Context, local, remote string, line func(string, string)) (int, error) {
	return d.call(pushCall{op: "tag", source: local, dest: remote}, line)
}
func (d *pushTestDriver) Push(_ context.Context, remote string, line func(string, string)) (int, error) {
	return d.call(pushCall{op: "push", dest: remote}, line)
}
func (*pushTestDriver) InspectImage(_ context.Context, ref string) (string, int64, error) {
	return "sha256:" + fmt.Sprintf("%064x", len(ref)), 42, nil
}
func (d *pushTestDriver) operations(op string) []pushCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	var calls []pushCall
	for _, c := range d.calls {
		if c.op == op {
			calls = append(calls, c)
		}
	}
	return calls
}

type pushTestReport struct {
	mu       sync.Mutex
	arts     []run.Artifact
	logs     []string
	done     map[string]string
	evidence map[string]string
	job      string
	root     *pushTestReport
	emitErr  bool
}

func newPushReport() *pushTestReport {
	return &pushTestReport{done: map[string]string{}, evidence: map[string]string{}}
}
func (r *pushTestReport) state() *pushTestReport {
	if r.root != nil {
		return r.root
	}
	return r
}
func (r *pushTestReport) Log(_ context.Context, _, line string) error {
	s := r.state()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, r.job+": "+line)
	return nil
}
func (r *pushTestReport) EmitArtifact(_ context.Context, a run.Artifact) error {
	s := r.state()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emitErr {
		return errors.New("artifact storage unavailable")
	}
	s.arts = append(s.arts, a)
	return nil
}
func (*pushTestReport) JobRunning(context.Context, string) error { return nil }
func (r *pushTestReport) JobDone(_ context.Context, id, status string) error {
	s := r.state()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done[id] = status
	return nil
}
func (r *pushTestReport) JobReporter(id string) dagrun.StageReporter {
	return &pushTestReport{root: r.state(), job: id}
}
func (r *pushTestReport) RecordExecution(mode string) {
	s := r.state()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidence[r.job] = mode
}

func pushFixture(d *pushTestDriver, envURL string) (*Builder, *run.Run) {
	b := newDAGTestBuilder(d, &markerCloner{})
	b.disableImageGC = true
	b.projects = fakeProjects{proj: &project.Project{ID: "p", Name: "Probe Project", RepoURL: "https://unused.invalid/repo.git"}}
	settings := &pipeline.Settings{}
	if envURL != "" {
		settings.Environments = []pipeline.Environment{{Name: "prod", ImageRegistry: pipeline.ImageRegistry{
			Type: pipeline.RegistryCustom, URL: envURL, CredentialID: "env-cred",
		}}}
	}
	b.settings = fakeSettings{settings: settings}
	b.vault = fakeVault{secrets: map[string]string{"env-cred": "env-user:env-password", "node-cred": "node-user:node-password"}}
	return b, &run.Run{ID: "run-194", ProjectID: "p", Trigger: run.Trigger{ResolvedEnvironment: "prod", Commit: "deadbee"}}
}

func imageAndPushStage(cfg map[string]any) pipeline.Stage {
	return pipeline.Stage{ID: "build-stage", Name: "Images", Jobs: []pipeline.Job{
		{ID: "image", Name: "Build", Type: "build_image"},
		{ID: "push", Name: "Push", Type: "push_image", Needs: []string{"image"}, Config: cfg},
	}}
}

func TestImagePushNodeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, env, want, user, host string
		cfg                         map[string]any
	}{
		{"node_all", "https://env.invalid/env-ns/", "node.invalid:5000/registry-ns/team/app:release-abc1234", "node-user", "node.invalid:5000", map[string]any{
			"registry": "https://NODE.invalid:5000/registry-ns/", "imageName": "team/app", "tag": "release-${COMMIT_SHA}", "credentialId": "node-cred"}},
		{"node_without_environment", "", "node.invalid/team/app:stable", "node-user", "node.invalid", map[string]any{
			"registry": "node.invalid", "imageName": "team/app", "tag": "stable", "credentialId": "node-cred"}},
		{"environment_defaults", "https://env.invalid/env-ns/", "env.invalid/env-ns/probe-project:abc1234", "env-user", "env.invalid", nil},
		{"partial_node", "env.invalid/env-ns", "env.invalid/env-ns/team/service:nightly", "env-user", "env.invalid", map[string]any{"imageName": "team/service", "tag": "nightly"}},
		{"same_host_namespace", "https://env.invalid/env-ns/", "env.invalid/new-ns/app:abc1234", "env-user", "env.invalid", map[string]any{"registry": "env.invalid/new-ns", "imageName": "app"}},
		{"changed_host_no_credential_leak", "env.invalid/env-ns", "new.invalid/probe-project:abc1234", "", "", map[string]any{"registry": "new.invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, tc.env)
			rep := newPushReport()
			if err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(tc.cfg), rep); err != nil {
				t.Fatal(err, rep.logs)
			}
			pushes, tags, logins := d.operations("push"), d.operations("tag"), d.operations("login")
			if len(pushes) != 1 || pushes[0].dest != tc.want || len(tags) != 1 || tags[0].dest != tc.want {
				t.Fatalf("push=%v tag=%v want=%s", pushes, tags, tc.want)
			}
			if tc.user == "" {
				if len(logins) != 0 {
					t.Fatal("environment credential sent to changed host")
				}
			} else if len(logins) != 1 || logins[0].user != tc.user || logins[0].dest != tc.host {
				t.Fatalf("login=%v", logins)
			}
			if rep.done["push"] != run.StepSuccess || rep.evidence["push"] != run.ExecutionReal || len(rep.arts) != 1 {
				t.Fatalf("done=%v evidence=%v artifacts=%v", rep.done, rep.evidence, rep.arts)
			}
			if rep.arts[0].Reference != tc.want || metadataString(rep.arts[0], "sourceJobId") != "push" || metadataString(rep.arts[0], "sourceStageId") != "build-stage" {
				t.Fatal("pushed artifact not assigned to push step", rep.arts)
			}
			if !strings.Contains(strings.Join(rep.logs, "\n"), "push: image pushed: "+tc.want) {
				t.Fatal("push success log assigned to wrong step", rep.logs)
			}
			if strings.Contains(tags[0].source, "deadbee") || !strings.Contains(tags[0].source, "abc1234-") {
				t.Fatal("source must use clone-resolved commit, not trigger commit", tags)
			}
		})
	}
}

func TestImagePushValidation(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"registry", ""}, {"registry", "https://user:SECRET@reg.invalid/ns"},
		{"registry", "reg.invalid/ns?token=SECRET"}, {"registry", "reg.invalid/ns#fragment"},
		{"registry", "ftp://reg.invalid"}, {"registry", "--config"},
		{"registry", "reg.invalid:0"}, {"registry", "reg.invalid:65536"}, {"registry", "reg.invalid:abc"},
		{"registry", "reg.invalid:"}, {"registry", "reg.invalid/../ns"}, {"registry", "reg.invalid//ns"},
		{"registry", "reg.invalid/%2fns"}, {"registry", "reg.invalid\\ns"}, {"registry", "reg.invalid/Upper"},
		{"registry", "reg.invalid\nSECRET"}, {"registry", "reg.invalid\x00SECRET"},
		{"registry", "registry/app"}, {"registry", "registry"}, {"registry", "http://registry/app"},
		{"imageName", "../app"}, {"imageName", "-app"}, {"imageName", "team/App"},
		{"imageName", "team//app"}, {"imageName", "app:latest"}, {"imageName", "app@sha256:abc"},
		{"imageName", "https://other.invalid/app"}, {"imageName", "other.invalid/app"},
		{"imageName", "localhost/app"}, {"imageName", "team/app;cmd"}, {"imageName", strings.Repeat("a", 256)},
		{"tag", "${UNKNOWN}"}, {"tag", "$(cmd)"}, {"tag", "-option"}, {"tag", "a/b"},
		{"tag", strings.Repeat("a", 129)}, {"tag", "version\nSECRET"},
		{"credentialId", "missing-credential"},
	} {
		t.Run(tc.field+"="+tc.value, func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, "")
			cfg := map[string]any{"registry": "reg.invalid", "imageName": "team/app", "tag": "stable"}
			cfg[tc.field] = tc.value
			rep := newPushReport()
			err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(cfg), rep)
			if !errors.Is(err, ErrBuildFailed) || rep.done["push"] != run.StepFailed {
				t.Fatalf("err=%v done=%v", err, rep.done)
			}
			if len(d.operations("login"))+len(d.operations("tag"))+len(d.operations("push")) != 0 || len(rep.arts) != 0 {
				t.Fatalf("invalid settings reached Docker or emitted success: %v %v", d.calls, rep.arts)
			}
			if strings.Contains(strings.Join(rep.logs, "\n"), "SECRET") {
				t.Fatal("invalid input echoed into logs")
			}
		})
	}
	for _, field := range []string{"registry", "imageName", "tag", "credentialId"} {
		if _, err := resolveImagePushTarget(map[string]any{"registry": "reg.invalid", field: 123}, nil, &project.Project{Name: "app"}, "abc1234"); err == nil {
			t.Fatalf("non-string %s silently ignored", field)
		}
	}
	for _, commit := range []string{"", "latest"} {
		if _, err := resolveImagePushTarget(map[string]any{"registry": "reg.invalid", "tag": "${COMMIT_SHA}"}, nil, &project.Project{Name: "app"}, commit); err == nil {
			t.Fatal("unknown commit expanded to latest")
		}
	}
	for _, raw := range []string{"localhost/team", "registry:5000/app", "http://localhost:5000/team", "https://[::1]:5000/ns", "docker.io", "registry-1.docker.io/user", "harbor.internal/team__name/repo--name"} {
		if _, _, err := parseImageRegistry(raw); err != nil {
			t.Fatalf("valid registry rejected %s: %v", raw, err)
		}
	}
}

func TestImagePushDriverFailuresAndMasking(t *testing.T) {
	for _, op := range []string{"login", "tag", "push", "build"} {
		for _, code := range []int{-1, 0, 1} {
			t.Run(fmt.Sprintf("%s-%d", op, code), func(t *testing.T) {
				d := &pushTestDriver{failOp: op, failCode: code, failErr: errors.New("SECRET driver details")}
				b, r := pushFixture(d, "reg.invalid")
				rep := newPushReport()
				if err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(nil), rep); !errors.Is(err, ErrBuildFailed) {
					t.Fatal("driver error reported success", err)
				}
				if rep.done["push"] == run.StepSuccess || len(rep.arts) > 1 || strings.Contains(strings.Join(rep.logs, "\n"), "SECRET") {
					t.Fatal("failure reported success or leaked driver error", rep.done, rep.logs)
				}
				if op == "login" && len(d.operations("tag")) != 0 || op == "tag" && len(d.operations("push")) != 0 {
					t.Fatal("push continued after failure")
				}
			})
		}
	}
	t.Run("mask_login_tag_and_push", func(t *testing.T) {
		d := &pushTestDriver{emit: "node-user:node-password node-password"}
		b, r := pushFixture(d, "")
		rep := newPushReport()
		// Avoid build output: a real Docker build cannot know the registry password.
		b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
			return []run.Artifact{{Type: run.ArtifactImage, Reference: "pipewright/app:abc1234", Metadata: map[string]any{"commitTag": "abc1234"}}}, nil
		}
		stage := pipeline.Stage{ID: "push-stage", Jobs: []pipeline.Job{{ID: "push", Type: "push_image", Config: map[string]any{"registry": "reg.invalid", "credentialId": "node-cred"}}}}
		if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(rep.logs, "\n"), "node-password") || d.operations("login")[0].pass != "node-password" {
			t.Fatal("password logging or login payload regression", rep.logs)
		}
	})
	for _, secret := range []string{"", "token-only", ":password", "user:", "bad\nuser:password", "-option:password"} {
		t.Run("unusable_credential="+secret, func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, "reg.invalid")
			b.vault = fakeVault{secrets: map[string]string{"env-cred": secret}}
			rep := newPushReport()
			if err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(nil), rep); !errors.Is(err, ErrBuildFailed) || len(d.operations("login"))+len(d.operations("push")) != 0 {
				t.Fatal("invalid selected credential silently became anonymous", err)
			}
		})
	}
}

func TestImagePushCancellation(t *testing.T) {
	for _, op := range []string{"before", "login", "tag", "push"} {
		t.Run(op, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := &pushTestDriver{cancelOp: op, cancel: cancel}
			b, _ := pushFixture(d, "")
			if op == "before" {
				cancel()
			}
			_, _, err := b.pushImageTarget(ctx, &reporterSink{rep: newPushReport()}, 0, "pipewright/app:abc1234", imagePushTarget{host: "reg.invalid", reference: "reg.invalid/app:abc1234", credentialID: "node-cred"})
			if !errors.Is(err, run.ErrCanceled) {
				t.Fatal("cancellation reported as success", err)
			}
			if op == "before" && len(d.calls) != 0 || op == "login" && len(d.operations("tag")) != 0 || op == "tag" && len(d.operations("push")) != 0 {
				t.Fatal("continued after cancellation", d.calls)
			}
		})
	}
}

func TestImagePushDAGSourceIsolation(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "")
	stage := pipeline.Stage{ID: "parallel", Jobs: []pipeline.Job{
		{ID: "build-a", Type: "build_image"}, {ID: "build-b", Type: "build_image"},
		{ID: "bridge-a", Type: "git_source", Needs: []string{"build-a"}},
		{ID: "push-a", Type: "push_image", Needs: []string{"bridge-a"}, Config: map[string]any{"registry": "reg.invalid", "imageName": "a"}},
		{ID: "push-b", Type: "push_image", Needs: []string{"build-b"}, Config: map[string]any{"registry": "reg.invalid", "imageName": "b"}},
		{ID: "push-a-again", Type: "push_image", Needs: []string{"push-a"}, Config: map[string]any{"registry": "reg.invalid", "imageName": "a", "tag": "latest"}},
	}}
	rep := newPushReport()
	if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); err != nil {
		t.Fatal(err, rep.logs)
	}
	a := imageJobLocalTag(&project.Project{Name: "Probe Project"}, r.ID, stage.ID, "build-a", "abc1234")
	bb := imageJobLocalTag(&project.Project{Name: "Probe Project"}, r.ID, stage.ID, "build-b", "abc1234")
	if a == bb || len(d.operations("push")) != 3 {
		t.Fatal("parallel images collided or pushes omitted", d.calls)
	}
	for _, tag := range d.operations("tag") {
		want := a
		if strings.Contains(tag.dest, "/b:") {
			want = bb
		}
		if tag.source != want {
			t.Fatalf("wrong source for %s: %s want %s", tag.dest, tag.source, want)
		}
	}
}

func TestImagePushImplicitAndGroupedDependency(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, "")
			stage := imageAndPushStage(map[string]any{"registry": "reg.invalid"})
			stage.Jobs[1].Needs = nil
			if grouped {
				stage.Jobs[0].ID = ""
			}
			// Declaration order must not make a push run before its source.
			stage.Jobs[0], stage.Jobs[1] = stage.Jobs[1], stage.Jobs[0]
			rep := newPushReport()
			if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); err != nil {
				t.Fatal(err, rep.logs)
			}
			if len(d.operations("push")) != 1 || rep.done["push"] != run.StepSuccess {
				t.Fatal("push omitted", d.calls, rep.done)
			}
		})
	}
}

func TestImagePushSeparateStage(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "")
	rep := newPushReport()
	exec := NewStageExecutor(b, nil)
	buildStage := pipeline.Stage{ID: "build", Jobs: []pipeline.Job{{ID: "image", Type: "build_image"}}}
	if err := exec(context.Background(), r, buildStage, rep); err != nil {
		t.Fatal(err)
	}
	b.artifactLister = func(_ context.Context, runID string) ([]run.Artifact, error) {
		if runID != r.ID {
			t.Fatal("cross-run image lookup")
		}
		rep.mu.Lock()
		defer rep.mu.Unlock()
		return append([]run.Artifact(nil), rep.arts...), nil
	}
	pushStage := pipeline.Stage{ID: "publish", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "push", Type: "push_image", Config: map[string]any{"registry": "reg.invalid", "imageName": "team/app", "tag": "${COMMIT_SHA}"}}}}
	for i := 0; i < 2; i++ {
		if err := exec(context.Background(), r, pushStage, rep); err != nil {
			t.Fatal(err, rep.logs)
		}
	}
	if len(d.operations("build")) != 1 || len(d.operations("push")) != 2 {
		t.Fatal("standalone push rebuilt or failed to push", d.calls)
	}
	for _, tag := range d.operations("tag") {
		if tag.source != d.operations("build")[0].dest || tag.dest != "reg.invalid/team/app:abc1234" {
			t.Fatal("cross-stage commit or immutable local source lost", tag)
		}
	}
}

func TestImagePushStageDependencyIgnoresUnrelatedImage(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "")
	b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
		return []run.Artifact{
			{Type: run.ArtifactImage, Reference: "pipewright/unrelated:abc1234", Metadata: map[string]any{"sourceStageId": "unrelated"}},
			{Type: run.ArtifactImage, Reference: "pipewright/upstream:abc1234", Metadata: map[string]any{"sourceStageId": "upstream", "commitTag": "abc1234"}},
		}, nil
	}
	stage := pipeline.Stage{ID: "publish", Needs: []string{"upstream"}, Jobs: []pipeline.Job{{ID: "push", Type: "push_image", Config: map[string]any{"registry": "reg.invalid"}}}}
	if err := NewStageExecutor(b, nil)(context.Background(), r, stage, newPushReport()); err != nil {
		t.Fatal(err)
	}
	if tags := d.operations("tag"); len(tags) != 1 || tags[0].source != "pipewright/upstream:abc1234" {
		t.Fatal("unrelated stage image affected push", tags)
	}
}

func TestImagePushMissingOrAmbiguousSource(t *testing.T) {
	for _, mode := range []string{"standalone", "multiple_stage_sources", "multiple_prior_sources", "lister_error", "jar_only", "wrong_dependency", "bad_prior_reference"} {
		t.Run(mode, func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, "")
			push := pipeline.Job{ID: "push", Type: "push_image", Config: map[string]any{"registry": "reg.invalid"}}
			stage := pipeline.Stage{ID: "stage", Jobs: []pipeline.Job{push}}
			switch mode {
			case "multiple_stage_sources":
				stage.Jobs = append(stage.Jobs, pipeline.Job{ID: "a", Type: "build_image"}, pipeline.Job{ID: "b", Type: "build_image"})
			case "multiple_prior_sources":
				b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
					return []run.Artifact{{Type: run.ArtifactImage, Reference: "pipewright/a:abc1234"}, {Type: run.ArtifactImage, Reference: "pipewright/b:abc1234"}}, nil
				}
			case "lister_error":
				b.artifactLister = func(context.Context, string) ([]run.Artifact, error) { return nil, errors.New("SECRET db error") }
			case "jar_only":
				b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
					return []run.Artifact{{Type: run.ArtifactJar, Reference: "a.jar"}}, nil
				}
			case "wrong_dependency":
				stage.Jobs[0].Needs = []string{"source"}
				stage.Jobs = append(stage.Jobs, pipeline.Job{ID: "source", Type: "git_source"}, pipeline.Job{ID: "a", Type: "build_image"})
			case "bad_prior_reference":
				b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
					return []run.Artifact{{Type: run.ArtifactImage, Reference: "--config=SECRET"}}, nil
				}
			}
			rep := newPushReport()
			if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); !errors.Is(err, ErrBuildFailed) || rep.done["push"] != run.StepFailed || len(d.operations("tag"))+len(d.operations("push")) != 0 {
				t.Fatal("missing/ambiguous source silently succeeded", err, rep.done, d.calls)
			}
			if strings.Contains(strings.Join(rep.logs, "\n"), "SECRET") {
				t.Fatal("source error leaked details")
			}
		})
	}
}

func TestImagePushEnvironmentAutopushCompatibility(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "https://env.invalid/ns/")
	rep := newPushReport()
	stage := pipeline.Stage{ID: "build", Jobs: []pipeline.Job{{ID: "image", Type: "build_image"}}}
	if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); err != nil {
		t.Fatal(err)
	}
	if len(d.operations("push")) != 1 || d.operations("push")[0].dest != "env.invalid/ns/probe-project:abc1234" || len(rep.arts) != 1 || rep.arts[0].Reference != d.operations("push")[0].dest {
		t.Fatal("environment-only autopush changed", d.calls, rep.arts)
	}
	if metadataString(rep.arts[0], "localReference") != d.operations("build")[0].dest {
		t.Fatal("autopush discarded immutable source")
	}
}

func TestImagePushPublishedDeploymentSelection(t *testing.T) {
	for _, tc := range []struct {
		name, prefer, explicit string
		arts                   []run.Artifact
		wantSelection, fail    bool
	}{
		{name: "local_and_published", prefer: "image", wantSelection: true},
		{name: "default_image", wantSelection: true},
		{name: "explicit_preserved", prefer: "image", explicit: "explicit-source"},
		{name: "file_preference", prefer: "jar"},
		{name: "default_file", arts: []run.Artifact{{Type: run.ArtifactJar, Metadata: map[string]any{"sourceStageId": "publish"}}}},
		{name: "ambiguous_published", prefer: "image", fail: true, arts: []run.Artifact{{Type: run.ArtifactImage, Metadata: map[string]any{"pushed": true, "sourceStageId": "publish"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := pushFixture(&pushTestDriver{}, "")
			arts := []run.Artifact{
				{Type: run.ArtifactImage, Reference: "pipewright/app:local", Metadata: map[string]any{"sourceStageId": "publish", "sourceJobId": "build"}},
				{Type: run.ArtifactImage, Reference: "reg.invalid/app:stable", Metadata: map[string]any{"pushed": true, "localReference": "pipewright/app:local", "sourceStageId": "publish", "sourceJobId": "push", "declarationIndex": 0}},
			}
			arts = append(arts, tc.arts...)
			b.artifactLister = func(context.Context, string) ([]run.Artifact, error) { return arts, nil }
			cfg := map[string]string{"artifactType": tc.prefer, "artifactSource": tc.explicit}
			err := b.preferPublishedImage(context.Background(), "run-194", cfg, pipeline.Stage{ID: "deploy", Needs: []string{"publish"}}, pipeline.Job{})
			if (err != nil) != tc.fail {
				t.Fatal(err)
			}
			if tc.wantSelection {
				var selector map[string]any
				if err := json.Unmarshal([]byte(cfg["artifactSource"]), &selector); err != nil || selector["jobId"] != "push" || selector["stageId"] != "publish" || selector["declarationIndex"] != float64(0) {
					t.Fatal("published artifact not selected", cfg)
				}
			} else if cfg["artifactSource"] != tc.explicit {
				t.Fatal("explicit/file selector changed", cfg)
			}
		})
	}
}

func TestImagePushAuthenticationIsAtomic(t *testing.T) {
	d := &pushTestDriver{}
	b, _ := pushFixture(d, "")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := b.pushImageTarget(context.Background(), &reporterSink{rep: newPushReport()}, 0, fmt.Sprintf("pipewright/app:v%d", i), imagePushTarget{host: "reg.invalid", reference: fmt.Sprintf("reg.invalid/app:v%d", i), credentialID: "node-cred"})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(d.calls) != 48 {
		t.Fatal("missing driver calls", len(d.calls))
	}
	for i := 0; i < len(d.calls); i += 3 {
		if d.calls[i].op != "login" || d.calls[i+1].op != "tag" || d.calls[i+2].op != "push" || d.calls[i+1].dest != d.calls[i+2].dest {
			t.Fatal("parallel authentication/push interleaved", d.calls)
		}
	}
}

func TestImagePushGateCancellation(t *testing.T) {
	imagePushGate <- struct{}{}
	defer func() { <-imagePushGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	d := &pushTestDriver{}
	b, _ := pushFixture(d, "")
	_, _, err := b.pushImageTarget(ctx, &reporterSink{rep: newPushReport()}, 0, "pipewright/app:latest", imagePushTarget{host: "reg.invalid", reference: "reg.invalid/app:latest"})
	if !errors.Is(err, run.ErrCanceled) || len(d.calls) != 0 {
		t.Fatal("waiting push ignored cancellation", err, d.calls)
	}
}

func TestImagePushArtifactRegistrationFailure(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "")
	rep := newPushReport()
	rep.emitErr = true
	if err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(map[string]any{"registry": "reg.invalid"}), rep); !errors.Is(err, ErrBuildFailed) || rep.done["push"] != run.StepFailed || len(rep.arts) != 0 {
		t.Fatal("artifact registration failure reported success", err, rep.done)
	}
}
