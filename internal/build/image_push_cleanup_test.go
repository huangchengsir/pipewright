package build

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

type releasingPushDriver struct{ *pushTestDriver }

func (d *releasingPushDriver) releaseImageTag(ctx context.Context, ref string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, err := d.call(pushCall{op: "release", dest: ref}, nil)
	return err
}

func TestImagePushCleanupOnlyFailedUnpublishedIntermediates(t *testing.T) {
	for _, mode := range []string{"unpublished", "published", "persistent_local", "lister_failure", "disabled", "success"} {
		t.Run(mode, func(t *testing.T) {
			d := &pushTestDriver{failOp: "push", failErr: errors.New("push failed")}
			if mode == "success" {
				d.failOp = ""
			}
			b, r := pushFixture(d, "")
			stage := imageAndPushStage(map[string]any{"registry": "reg.invalid"})
			stage.Jobs = append(stage.Jobs, pipeline.Job{ID: "unconsumed-b", Type: "build_image"})
			proj, _, resolveErr := b.resolve(context.Background(), r)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			local := imageJobLocalTag(proj, r.ID, stage.ID, "image", "abc1234")
			b.driver = &releasingPushDriver{d}
			b.disableImageGC = mode == "disabled"
			b.artifactLister = func(context.Context, string) ([]run.Artifact, error) {
				if mode == "lister_failure" {
					return nil, errors.New("storage unavailable")
				}
				if mode == "published" {
					return []run.Artifact{{Reference: "reg.invalid/app:stable", Metadata: map[string]any{"localReference": local, "pushed": true}}}, nil
				}
				if mode == "persistent_local" {
					return []run.Artifact{{Reference: local}}, nil
				}
				return nil, nil
			}
			err := NewStageExecutor(b, nil)(context.Background(), r, stage, newPushReport())
			if (err == nil) != (mode == "success") {
				t.Fatal(err)
			}
			released := d.operations("release")
			if mode != "unpublished" {
				if len(released) != 0 {
					t.Fatal("persistent/published/unknown/disabled/success image deleted", released)
				}
			} else {
				if len(released) != 1 || released[0].dest != local {
					t.Fatal("unpublished hidden image not released", released)
				}
			}
		})
	}
}

func TestImagePushCleanupNonForceExactTagOnly(t *testing.T) {
	cmd := newFakeCommander()
	d := &shellDriver{bin: "docker", cmdr: cmd}
	ref := "pipewright/app:abc1234-0123456789abcdef"
	if err := d.releaseImageTag(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"reg.invalid/app:stable", "pipewright/app:old", "--force", "pipewright/app:abc1234-0123456789abcdef\n--all"} {
		if err := d.releaseImageTag(context.Background(), bad); err == nil {
			t.Fatal("non-owned tag accepted", bad)
		}
	}
	if len(cmd.execs) != 1 || strings.Join(cmd.execs[0].args, " ") != "image rm "+ref {
		t.Fatal("cleanup broadened beyond owned non-force tag", cmd.execs)
	}
}
