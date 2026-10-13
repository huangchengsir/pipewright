package build

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

func TestImagePushEmptyStageDependencyNeverUsesOtherBranch(t *testing.T) {
	for _, name := range []string{"missing", "file_only", "intermediary"} {
		t.Run(name, func(t *testing.T) {
			d := &pushTestDriver{}
			b, r := pushFixture(d, "")
			b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
				return []run.Artifact{
					{Type: run.ArtifactImage, Reference: "pipewright/other:stable", Metadata: map[string]any{"sourceStageId": "independent"}},
					{Type: run.ArtifactJar, Reference: "source.jar", Metadata: map[string]any{"sourceStageId": name}},
				}, nil
			}
			stage := pipeline.Stage{ID: "publish", Needs: []string{name}, Jobs: []pipeline.Job{{ID: "push", Type: "push_image", Config: map[string]any{"registry": "reg.invalid"}}}}
			rep := newPushReport()
			if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); !errors.Is(err, ErrBuildFailed) || rep.done["push"] != run.StepFailed {
				t.Fatal("missing dependency image succeeded", err, rep.done)
			}
			if len(d.operations("tag"))+len(d.operations("push")) != 0 || len(rep.arts) != 0 {
				t.Fatal("independent branch was consumed", d.calls, rep.arts)
			}
		})
	}
}

func TestImagePushDeploymentBranchIsolation(t *testing.T) {
	ctx := context.Background()
	image := func(stage, job, ref string, pushed bool) run.Artifact {
		return run.Artifact{Type: run.ArtifactImage, Reference: ref, Metadata: map[string]any{"sourceStageId": stage, "sourceJobId": job, "pushed": pushed, "declarationIndex": 0}}
	}
	a := image("a", "build-a", "reg.invalid/a:stable", true)
	bArt := image("b", "push-b", "reg.invalid/b:stable", true)
	for _, reverse := range []bool{false, true} {
		for _, sameStage := range []bool{false, true} {
			for _, auto := range []bool{false, true} {
				arts := []run.Artifact{a, bArt}
				arts[0].Metadata = map[string]any{"sourceStageId": "a", "sourceJobId": "build-a", "pushed": auto}
				if reverse {
					arts[0], arts[1] = arts[1], arts[0]
				}
				builder, _ := pushFixture(&pushTestDriver{}, "")
				builder.artifactLister = func(context.Context, string) ([]run.Artifact, error) { return arts, nil }
				stage := pipeline.Stage{ID: "deploy", Needs: []string{"a"}}
				job := pipeline.Job{ID: "deploy-a"}
				if sameStage {
					stage = pipeline.Stage{ID: "a", Jobs: []pipeline.Job{{ID: "build-a", Type: "build_image"}, {ID: "bridge", Needs: []string{"build-a"}}}}
					job.Needs = []string{"bridge"}
				}
				cfg := map[string]string{}
				if err := builder.preferPublishedImage(ctx, "r", cfg, stage, job); err != nil {
					t.Fatal(err)
				}
				var selector map[string]any
				if err := json.Unmarshal([]byte(cfg["artifactSource"]), &selector); err != nil || selector["stageId"] != "a" || selector["jobId"] != "build-a" {
					t.Fatal("branch B hijacked branch A default", cfg, reverse, sameStage, auto)
				}
			}
		}
	}
}

func TestImagePushDeploymentEmptyAndFileCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, prefer string
		arts         []run.Artifact
		fail         bool
	}{
		{name: "empty_default"},
		{name: "script_default", arts: []run.Artifact{}},
		{name: "unscoped_file_default", arts: []run.Artifact{{Type: run.ArtifactJar}}},
		{name: "file_and_unrelated_image", arts: []run.Artifact{{Type: run.ArtifactJar}, {Type: run.ArtifactImage, Metadata: map[string]any{"pushed": true, "sourceStageId": "other"}}}},
		{name: "explicit_image_empty", prefer: "image", fail: true},
		{name: "normalized_explicit_image_empty", prefer: " IMAGE ", fail: true},
		{name: "explicit_image_file_only", prefer: "image", fail: true, arts: []run.Artifact{{Type: run.ArtifactJar}}},
		{name: "independent_image_default", fail: true, arts: []run.Artifact{{Type: run.ArtifactImage, Metadata: map[string]any{"pushed": true, "sourceStageId": "other"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := pushFixture(&pushTestDriver{}, "")
			b.artifactLister = func(context.Context, string) ([]run.Artifact, error) { return tc.arts, nil }
			cfg := map[string]string{"artifactType": tc.prefer}
			err := b.preferPublishedImage(context.Background(), "r", cfg, pipeline.Stage{ID: "deploy", Needs: []string{"empty"}}, pipeline.Job{})
			if (err != nil) != tc.fail || cfg["artifactSource"] != "" {
				t.Fatal("default file/script compatibility or missing-image failure changed", err, cfg)
			}
		})
	}
}

func TestImagePushPartialStagePreservesUnconsumedBuild(t *testing.T) {
	for _, env := range []string{"", "env.invalid/ns"} {
		d := &pushTestDriver{}
		b, r := pushFixture(d, env)
		stage := imageAndPushStage(map[string]any{"registry": "reg.invalid", "imageName": "a", "tag": "stable"})
		stage.Jobs = append(stage.Jobs, pipeline.Job{ID: "b", Type: "build_image"})
		rep := newPushReport()
		if err := NewStageExecutor(b, nil)(context.Background(), r, stage, rep); err != nil {
			t.Fatal(err)
		}
		if len(rep.arts) != 2 {
			t.Fatal("unconsumed B hidden or consumed A persisted", rep.arts)
		}
		foundB, foundPush := false, false
		for _, a := range rep.arts {
			switch metadataString(a, "sourceJobId") {
			case "b":
				foundB = true
				if a.Metadata["declarationIndex"] != 0 {
					t.Fatal("unconsumed build cannot be selected for deployment", a)
				}
				if (a.Metadata["pushed"] == true) != (env != "") {
					t.Fatal("unconsumed build autopush metadata lost", a)
				}
			case "push":
				foundPush = true
				if a.Reference != "reg.invalid/a:stable" || metadataString(a, "digestRepository") != "reg.invalid/a" {
					t.Fatal("wrong explicit push result", a)
				}
			default:
				t.Fatal("consumed local A persisted", a)
			}
		}
		if !foundB || !foundPush {
			t.Fatal("missing expected artifact", rep.arts)
		}
	}
}

func TestImagePushStageExecutorPassesDeployScope(t *testing.T) {
	for _, needs := range [][]string{nil, {"build-a"}} {
		b, r := pushFixture(&pushTestDriver{}, "")
		dep := &stubStageDeployer{}
		b.deployer = dep
		b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
			return []run.Artifact{
				{Type: run.ArtifactImage, Reference: "reg.invalid/b:stable", Metadata: map[string]any{"pushed": true, "sourceStageId": "b", "sourceJobId": "push-b", "declarationIndex": 0}},
				{Type: run.ArtifactImage, Reference: "reg.invalid/a:stable", Metadata: map[string]any{"pushed": true, "sourceStageId": "a", "sourceJobId": "build-a", "declarationIndex": 0}},
			}, nil
		}
		stage := pipeline.Stage{ID: "a", Jobs: []pipeline.Job{{ID: "deploy-a", Type: "deploy_ssh", Needs: needs, Config: map[string]any{"serverId": "srv"}}}}
		if len(needs) > 0 {
			stage.Jobs = append(stage.Jobs, pipeline.Job{ID: "build-a", Type: "git_source"})
		}
		if err := NewStageExecutor(b, nil)(context.Background(), r, stage, newPushReport()); err != nil {
			t.Fatal(err)
		}
		var selector map[string]any
		if err := json.Unmarshal([]byte(dep.gotCfg["artifactSource"]), &selector); err != nil || selector["stageId"] != "a" || selector["jobId"] != "build-a" {
			t.Fatal("executor did not pass deployment branch scope", dep.gotCfg)
		}
	}
}

func TestImagePushInspectExactRepository(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, ref, out, want string
		fail                 bool
	}{
		{"correct_after_other", "reg.invalid:5000/ns/app:stable", `{"Id":"config-id","Size":42,"RepoDigests":["other.invalid/ns/app@` + other + `","reg.invalid:5000/ns/app@` + digest + `"]}`, digest, false},
		{"array", "reg.invalid/app:latest", `[{"RepoDigests":["reg.invalid/app@` + digest + `"]}]`, digest, false},
		{"other_repository", "reg.invalid/app:stable", `{"Id":"` + digest + `","RepoDigests":["reg.invalid/other@` + digest + `"]}`, "", true},
		{"config_id_only", "reg.invalid/app:latest", `{"Id":"` + digest + `"}`, "", true},
		{"malformed_digest", "reg.invalid/app:stable", `{"RepoDigests":["reg.invalid/app@sha256:abc"]}`, "", true},
		{"ambiguous_same_repo", "reg.invalid/app:stable", `{"RepoDigests":["reg.invalid/app@` + digest + `","reg.invalid/app@` + other + `"]}`, "", true},
		{"duplicate_same_digest", "localhost/app:stable", `{"RepoDigests":["localhost/app@` + digest + `","localhost/app@` + digest + `"]}`, digest, false},
		{"local_config_compatibility", "pipewright/app:local", `{"Id":"config-id"}`, "config-id", false},
		{"bad_json", "reg.invalid/app:stable", "garbage", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newFakeCommander()
			cmd.script("inspect", fakeCmd{stdoutLines: []string{tc.out}})
			d := &shellDriver{bin: "docker", cmdr: cmd}
			got, _, err := d.InspectImage(context.Background(), tc.ref)
			if got != tc.want || (err != nil) != tc.fail {
				t.Fatal("incorrect repository digest", got, err)
			}
		})
	}
}

type noManifestDriver struct{ *pushTestDriver }

func (*noManifestDriver) InspectImage(context.Context, string) (string, int64, error) {
	return "", 0, nil
}

func TestImagePushMissingManifestCannotPublishSuccess(t *testing.T) {
	d := &pushTestDriver{}
	b, r := pushFixture(d, "")
	b.driver = &noManifestDriver{d}
	rep := newPushReport()
	if err := NewStageExecutor(b, nil)(context.Background(), r, imageAndPushStage(map[string]any{"registry": "reg.invalid"}), rep); !errors.Is(err, ErrBuildFailed) || len(rep.arts) != 0 || rep.done["push"] != run.StepFailed {
		t.Fatal("missing manifest digest reported success", err, rep.arts, rep.done)
	}
	if len(d.operations("push")) != 1 {
		t.Fatal("did not exercise successful push followed by missing digest")
	}
}
