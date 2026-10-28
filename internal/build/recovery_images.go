package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/runretry"
)

type recoveryImageInspector interface {
	ImageIdentity(context.Context, string) (runretry.ImageRef, error)
}

func (d *shellDriver) ImageIdentity(ctx context.Context, ref string) (runretry.ImageRef, error) {
	out := runretry.ImageRef{Ref: ref}
	if !validLocalImageReference(ref) {
		return out, runretry.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	runtime, _, code, err := d.cmdr.Run(ctx, d.bin, []string{"info", "--format", "{{.ID}}"}, "")
	if err != nil || code != 0 || len(runtime) > 4096 || strings.TrimSpace(runtime) == "" || strings.Contains(runtime, "<no value>") {
		return out, runretry.ErrUnavailable
	}
	data, _, code, err := d.cmdr.Run(ctx, d.bin, []string{"image", "inspect", ref, "--format", "{{json .}}"}, "")
	if err != nil || code != 0 || len(data) > 1<<20 {
		return out, runretry.ErrUnavailable
	}
	var image struct {
		ID          string          `json:"Id"`
		Config      json.RawMessage `json:"Config"`
		RepoDigests []string        `json:"RepoDigests"`
	}
	if json.Unmarshal([]byte(data), &image) != nil || len(image.ID) != 71 || !strings.HasPrefix(image.ID, "sha256:") {
		return out, runretry.ErrUnavailable
	}
	runtimeHash := sha256.Sum256([]byte(d.bin + "\x00" + strings.TrimSpace(runtime)))
	configHash := sha256.Sum256(image.Config)
	out.RuntimeID = hex.EncodeToString(runtimeHash[:])
	out.ImageID = image.ID
	out.ConfigSHA = hex.EncodeToString(configHash[:])
	for _, digest := range image.RepoDigests {
		if i := strings.LastIndex(digest, "@sha256:"); i >= 0 {
			out.Digest = digest[i+1:]
			break
		}
	}
	return out, nil
}
func (b *Builder) checkpointImage(ctx context.Context, ref string) (runretry.ImageRef, error) {
	inspector, ok := b.driver.(recoveryImageInspector)
	if !ok {
		return runretry.ImageRef{}, runretry.ErrUnavailable
	}
	return inspector.ImageIdentity(ctx, ref)
}

// ValidateCheckpointImage is a read-only preflight and is repeated at the last
// boundary before a consumer can cause an external effect. A moved tag never
// substitutes another image even when its human-readable reference is equal.
func (b *Builder) ValidateCheckpointImage(ctx context.Context, want runretry.ImageRef) error {
	got, err := b.checkpointImage(ctx, want.Ref)
	if err != nil {
		return err
	}
	if want.RuntimeID == "" || want.ImageID == "" || want.ConfigSHA == "" || got.RuntimeID != want.RuntimeID || got.ImageID != want.ImageID || got.ConfigSHA != want.ConfigSHA {
		return runretry.ErrUnavailable
	}
	if want.Digest != "" && got.Digest != want.Digest {
		return runretry.ErrUnavailable
	}
	return nil
}
func (b *Builder) validateRecoveryImages(ctx context.Context, images map[string]runretry.ImageRef) error {
	for _, key := range runretry.SortedKeys(images) {
		if err := b.ValidateCheckpointImage(ctx, images[key]); err != nil {
			return err
		}
	}
	return nil
}

func (d *shellDriver) ReadyService(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		out, _, code, err := d.cmdr.Run(ctx, d.bin, []string{"inspect", "--format", "{{json .State}}", name}, "")
		if err != nil || code != 0 || len(out) > 64<<10 {
			return runretry.ErrUnavailable
		}
		var state struct {
			Running bool
			Health  *struct{ Status string }
		}
		if json.Unmarshal([]byte(out), &state) != nil || !state.Running {
			return runretry.ErrUnavailable
		}
		if state.Health == nil || state.Health.Status == "healthy" {
			return nil
		}
		if state.Health.Status == "unhealthy" {
			return runretry.ErrUnavailable
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// ReleaseCheckpointImage is only called after retention has released all pins.
// It never force-removes an image or broadens cleanup to a repository/runtime.
func (b *Builder) ReleaseCheckpointImage(ctx context.Context, image runretry.ImageRef) error {
	if !ownedImageJobTag.MatchString(image.Ref) || image.Metadata["pushed"] == true {
		return runretry.ErrInvalid
	}
	if local, _ := image.Metadata["localReference"].(string); local != "" && local != image.Ref {
		return runretry.ErrInvalid
	}
	if err := b.ValidateCheckpointImage(ctx, image); err != nil {
		return err
	}
	cleaner, ok := b.driver.(imageTagReleaser)
	if !ok {
		return runretry.ErrUnavailable
	}
	return cleaner.releaseImageTag(ctx, image.Ref)
}
