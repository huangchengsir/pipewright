package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"io"
)

func (b *Builder) releaseChartBytes(ctx context.Context, r *run.Run, source *pipeline.KubernetesReleaseSource, ex runretry.Execution, node runretry.NodeRef, reporter dagrun.StageReporter) ([]byte, error) {
	if source == nil {
		return nil, kubernetes.ErrManifest
	}
	switch source.Kind {
	case "git":
		if len(ex.Snapshot().CommitSHA) != 40 {
			return nil, runretry.ErrUnavailable
		}
		project, _, e := b.resolve(ctx, r)
		if e != nil {
			return nil, e
		}
		workspace, _, cleanup, e := b.cloneJobWorkspace(ctx, r, project, reporter)
		if e != nil {
			return nil, e
		}
		defer cleanup()
		return kubernetes.ReadReleaseGitChart(workspace, ex.Snapshot().CommitSHA, source.Path)
	case "artifact":
		if source.Artifact == nil || b.artStore == nil {
			return nil, runretry.ErrUnavailable
		}
		inputs, e := ex.Inputs(ctx, node)
		if e != nil {
			return nil, e
		}
		producer, ok := ex.Node(source.Artifact.StageID, source.Artifact.JobID)
		if !ok {
			return nil, runretry.ErrUnavailable
		}
		files := []runretry.FileRef{}
		for _, file := range inputs.Files {
			index, ok := sourceDeclarationIndex(run.Artifact{Metadata: file.Metadata})
			if ok && index == *source.Artifact.DeclarationIndex && file.ProducerNodeKey == producer.Key {
				files = append(files, file)
			}
		}
		if len(files) != 1 {
			return nil, runretry.ErrUnavailable
		}
		file := files[0]
		if file.Size < 0 || file.Size > kubernetes.MaxReleaseInputBytes || file.Key != file.SHA256 {
			return nil, runretry.ErrUnavailable
		}
		reader, e := b.artStore.Open(file.Key)
		if e != nil {
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(reader, kubernetes.MaxReleaseInputBytes+1))
		reader.Close()
		sum := sha256.Sum256(data)
		if e != nil || int64(len(data)) != file.Size || hex.EncodeToString(sum[:]) != file.SHA256 {
			return nil, runretry.ErrUnavailable
		}
		if source.Path == "" || source.Path == "." || source.Path == file.Path {
			return data, nil
		}
		defer clear(data)
		return kubernetes.SelectReleaseChartArchive(data, source.Path)
	}
	return nil, kubernetes.ErrManifest
}
