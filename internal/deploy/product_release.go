package deploy

import (
	"context"
	"slices"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

func pinnedProductArtifact(a run.Artifact, current string) bool {
	if a.RunID != current || a.Type != run.ArtifactImage || a.Metadata["pushed"] != true {
		return false
	}
	ref, err := imageDeploymentReference(a)
	repo, _ := a.Metadata["digestRepository"].(string)
	digest, _ := a.Metadata["digest"].(string)
	return err == nil && ref == repo+"@"+digest
}

type productImageIdentity struct {
	ID           string   `json:"id"`
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant"`
	RepoDigests  []string `json:"repoDigests"`
}

const productImageProjection = `{` + dockerImageIdentityFields + `,"repoDigests":{{json .RepoDigests}}}`
const productContainerProjection = `{"id":{{json .Id}},"image":{{json .Image}},"createdAt":{{json .Created}},"startedAt":{{json .State.StartedAt}},"running":{{json .State.Running}}}`

func (s *service) productInspect(ctx context.Context, server string, args []string, result any) bool {
	executor, ok := s.targets.(target.LimitedExecutor)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	approved, err := executor.Capture(ctx, server)
	if err != nil {
		return false
	}
	out, err := executor.ExecLimited(ctx, approved, args, target.ExecutionLimits{Bytes: 2048, Lines: 4})
	return err == nil && out != nil && !out.Truncated && out.ExitCode == 0 && decodeLocalProjection(out.Stdout, result)
}
func (s *service) inspectProductImage(ctx context.Context, server, ref string) (productImageIdentity, bool) {
	var x productImageIdentity
	valid := s.productInspect(ctx, server, []string{"docker", "image", "inspect", "--format", productImageProjection, ref}, &x) &&
		validLocalImageIdentity(localDockerImageIdentity{ID: x.ID, OS: x.OS, Architecture: x.Architecture, Variant: x.Variant}) &&
		slices.Contains(x.RepoDigests, ref)
	return x, valid
}
func (s *service) verifyProductContainer(ctx context.Context, server, id string, st imageState) bool {
	var x productContainerIdentity
	if st.productImage == nil || st.receipt == nil || !s.productInspect(ctx, server, []string{"docker", "container", "inspect", "--format", productContainerProjection, id}, &x) ||
		x.ID != id || x.Image != st.productImage.ID || !x.Running || len(x.CreatedAt) > 64 || len(x.StartedAt) > 64 {
		return false
	}
	st.receipt.actual = deployattempt.ActualImage{
		ContainerID: id, ConfigID: x.Image, ContainerCreatedAt: x.CreatedAt, ContainerStartedAt: x.StartedAt,
		RepoDigests: slices.Clone(st.productImage.RepoDigests),
		Platform:    deployattempt.Platform{OS: st.productImage.OS, Architecture: st.productImage.Architecture, Variant: st.productImage.Variant},
	}
	return true
}

type productContainerIdentity struct {
	ID        string `json:"id"`
	Image     string `json:"image"`
	CreatedAt string `json:"createdAt"`
	StartedAt string `json:"startedAt"`
	Running   bool   `json:"running"`
}

func productPinnedStart(command []string) []string {
	return append([]string{"docker", "run", "--pull=never"}, command[2:]...)
}
