package build

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

func (b *Builder) SuspendKubernetesRun(ctx context.Context, r *run.Run, stages map[string]string) error {
	ex, ok := runretry.ExecutionFrom(ctx)
	if !ok {
		return runretry.ErrUnavailable
	}
	frontier, ok := ex.(interface {
		Frontier(context.Context) (map[string]string, error)
	})
	if !ok {
		return runretry.ErrUnavailable
	}
	nodes, e := frontier.Frontier(ctx)
	if e != nil {
		return e
	}
	records, e := b.kubernetes.ListReleases(ctx, r.ID)
	if e != nil {
		return e
	}
	paused := false
	for _, record := range records {
		if record.State == "paused" {
			paused = true
			continue
		}
		if record.State != "running" || record.Plan.Mode != "progressive" || nodes[record.NodeKey] != "waiting_action" {
			continue
		}
		for _, status := range stages {
			if status == "failed" || status == "canceled" {
				_, e = b.kubernetes.SetReleaseTerminal(ctx, record.ID, b.kubernetesOwner, record.Revision, "partial_failed", "parallel_branch_failed")
				if e != nil {
					return e
				}
				_ = b.kubernetes.ReleaseGates(ctx, record.ID, b.kubernetesOwner)
				return kubernetes.ErrRollout
			}
		}
		_, e = b.kubernetes.SuspendRelease(ctx, record.ID, b.kubernetesOwner, record.Revision, kubernetes.ReleaseSuspension{Version: 2, RunID: r.ID, NodeKey: record.NodeKey, PlanID: record.PlanID, Cursor: record.Cursor + 1, Stages: stages, Nodes: nodes})
		if e != nil {
			return e
		}
		paused = true
	}
	if !paused {
		return runretry.ErrUnavailable
	}
	return b.kubernetes.PreserveReleaseSuspension(ctx, r.ID)
}
