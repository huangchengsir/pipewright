package build

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

const checkoutTestSHA = "abcdef0123456789abcdef0123456789abcdef01"

type resolvedEvidenceCloner struct{ full, short string }

func (c resolvedEvidenceCloner) Clone(context.Context, string, string, string, string, string, string) (*CloneResolved, error) {
	return &CloneResolved{CommitFull: c.full, CommitShort: c.short}, nil
}

func TestBuildPathsRecordFullResolvedCommit(t *testing.T) {
	for _, mode := range []string{"local", "dag-stage", "dag-job", "remote"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var got []string
			recorder := func(_ context.Context, runID, full string) {
				mu.Lock()
				defer mu.Unlock()
				if runID != "resolved-run" {
					t.Errorf("run ID=%q", runID)
				}
				got = append(got, full)
			}
			r := &run.Run{ID: "resolved-run", ProjectID: "p1", Trigger: run.Trigger{Commit: "untrusted-trigger"}}
			cloner := resolvedEvidenceCloner{full: checkoutTestSHA, short: checkoutTestSHA[:7]}
			if mode == "local" {
				cmdr := newFakeCommander()
				cmdr.script("build", fakeCmd{exitCode: 0})
				cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:abc","RepoDigests":[],"Size":1}`}, exitCode: 0})
				b := newTestBuilder(t, cmdr, &project.Project{ID: "p1", Name: "Source", RepoURL: "https://example.com/repo"}, imageSettings(nil), nil)
				b.cloner, b.recordCommit = cloner, recorder
				sink := newFakeSink()
				if err := b.Run(context.Background(), r, sink); err != nil {
					t.Fatal(err)
				}
				if len(sink.artifacts) != 1 || !strings.HasSuffix(sink.artifacts[0].Reference, ":"+checkoutTestSHA[:7]) {
					t.Fatalf("artifact tags must stay short: %+v", sink.artifacts)
				}
			} else {
				b := newDAGTestBuilder(&recordingDriver{}, cloner)
				b.recordCommit = recorder
				if mode == "dag-job" {
					_, tag, cleanup, err := b.cloneJobWorkspace(context.Background(), r, &project.Project{RepoURL: "https://example.com/repo"}, &fakeReporter{})
					defer cleanup()
					if err != nil || tag != checkoutTestSHA[:7] {
						t.Fatalf("job workspace: tag=%q err=%v", tag, err)
					}
				} else {
					execute := NewStageExecutor(b, nil)
					if mode == "remote" {
						execute = NewStageExecutorWithRunner(b, nil, fakeRunnerLookup{serverID: "srv-1"}, &fakeRemoteTarget{})
					}
					if err := execute(context.Background(), r, scriptStage(scriptJob("build", "busybox", "true")), &fakeReporter{}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(got) != 1 || got[0] != checkoutTestSHA {
				t.Fatalf("recorder got=%v, want actual full checkout", got)
			}
		})
	}
}

func TestLegacyShortCloneIsNotExpandedToResolvedEvidence(t *testing.T) {
	b := newDAGTestBuilder(&recordingDriver{}, resolvedEvidenceCloner{short: "abc1234"})
	b.recordCommit = func(context.Context, string, string) { t.Fatal("short-only result is not full checkout evidence") }
	_, tag, cleanup, err := b.cloneJobWorkspace(context.Background(), &run.Run{ID: "legacy"}, &project.Project{RepoURL: "https://example.com/repo"}, &fakeReporter{})
	defer cleanup()
	if err != nil || tag != "abc1234" {
		t.Fatalf("legacy short artifact tag: tag=%q err=%v", tag, err)
	}
}
