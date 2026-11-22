package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"unicode/utf8"
)

// PipelineSourceSnapshot is controller-only input, not a browser source DTO.
// HEAD and YAML are read from the same clone; no later active-branch fetch is
// permitted to substitute a different deployment plan.
type PipelineSourceSnapshot struct {
	CommitSHA string
	YAML      string
	Found     bool
}
type PipelineSnapshotReader interface {
	PipelineSnapshot(context.Context, string, string, string, string) (PipelineSourceSnapshot, error)
	PipelineBranchCommit(context.Context, string, string, string, string) (string, error)
}

func NewPipelineSnapshotReader() PipelineSnapshotReader { return goGitSourceReader{} }

var ErrPipelineSnapshot = errors.New("source: pipeline snapshot unavailable")

func (g goGitSourceReader) PipelineSnapshot(ctx context.Context, repoURL, username, token, branch string) (PipelineSourceSnapshot, error) {
	tree, commit, err := g.cloneSnapshot(ctx, repoURL, username, token, branch)
	if err != nil {
		return PipelineSourceSnapshot{}, ErrPipelineSnapshot
	}
	result := PipelineSourceSnapshot{CommitSHA: commit}
	info, err := tree.Lstat(".pipewright.yml")
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBlobBytes {
		return PipelineSourceSnapshot{}, ErrPipelineSnapshot
	}
	file, err := tree.Open(".pipewright.yml")
	if err != nil {
		return PipelineSourceSnapshot{}, ErrPipelineSnapshot
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBlobBytes+1))
	if err != nil || len(data) > maxBlobBytes || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return PipelineSourceSnapshot{}, ErrPipelineSnapshot
	}
	result.Found = true
	result.YAML = string(data)
	return result, nil
}
func (g goGitSourceReader) PipelineBranchCommit(ctx context.Context, repoURL, username, token, branch string) (string, error) {
	_, commit, err := g.cloneSnapshot(ctx, repoURL, username, token, branch)
	if err != nil {
		return "", ErrPipelineSnapshot
	}
	return commit, nil
}
