package build

import (
	"context"
	"regexp"
	"time"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

var ownedImageJobTag = regexp.MustCompile(`^pipewright/[a-z0-9]+(?:-[a-z0-9]+)*:[A-Za-z0-9_][A-Za-z0-9_.-]*-[a-f0-9]{16}$`)

type imageTagReleaser interface {
	releaseImageTag(context.Context, string) error
}

func (d *shellDriver) releaseImageTag(ctx context.Context, ref string) error {
	if !ownedImageJobTag.MatchString(ref) || !validLocalImageReference(ref) {
		return ErrBuildFailed
	}
	_, _, code, err := d.cmdr.Run(ctx, d.bin, []string{"image", "rm", ref}, "")
	if err != nil || code != 0 {
		return ErrBuildFailed
	}
	return nil
}

// A failed stage has joined all jobs. Release only its hidden, unpublished inputs;
// published or persistent artifacts may still have consumers and must be retained.
func (b *Builder) releaseFailedStageImages(ctx context.Context, r *run.Run, stage pipeline.Stage, images *stageImageArtifacts, rep dagrun.StageReporter) {
	cleaner, ok := b.driver.(imageTagReleaser)
	if !ok || b.disableImageGC || b.artifactLister == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	persisted, err := b.artifactLister(cleanupCtx, r.ID)
	if err != nil {
		return // Without storage evidence, ownership/consumer safety cannot be proven.
	}
	images.mu.Lock()
	arts := append([]run.Artifact(nil), images.arts...)
	images.mu.Unlock()
	seen := map[string]bool{}
	for _, a := range arts {
		local := metadataString(a, "localReference")
		if seen[local] || !ownedImageJobTag.MatchString(local) || a.Reference != local || metadataString(a, "sourceStageId") != stage.ID {
			continue
		}
		consumed := false
		for _, job := range stage.Jobs {
			if job.ID == metadataString(a, "sourceJobId") && isBuildImageJob(job.Type) && imageBuildConsumedByPush(stage, job) {
				consumed = true
				break
			}
		}
		if !consumed {
			continue
		}
		protected := false
		for _, p := range persisted {
			if p.Reference == local || metadataString(p, "localReference") == local {
				protected = true
				break
			}
		}
		if !protected {
			seen[local] = true
			if err := cleaner.releaseImageTag(cleanupCtx, local); err != nil {
				_ = rep.Log(cleanupCtx, streamStderr, "unpublished image cleanup failed; retained for safe manual cleanup")
			}
		}
	}
}
