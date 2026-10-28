package run

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"time"
)

// RetryAdapters exposes a narrow transactional creator and post-commit dispatcher.
// The creator never calls Create (which would enqueue before an outer tx commits).
func RetryAdapters(svc Service) (runretry.ChildCreator, runretry.Dispatch) {
	s, ok := svc.(*service)
	if !ok {
		return nil, nil
	}
	child := func(ctx context.Context, q configstate.DBTX, in runretry.ChildInput) error {
		params, err := json.Marshal(in.Trigger.Params)
		if err != nil {
			return err
		}
		targets := in.Trigger.TargetServerIDs
		if targets == nil {
			targets = []string{}
		}
		targetJSON, err := json.Marshal(targets)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,resolved_environment,resolved_target_server_ids,params_json,chain_source_run_id,chain_depth,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.ProjectID, StatusQueued, in.Trigger.Type, in.Trigger.Branch, in.Trigger.Commit, in.Trigger.Actor, in.Trigger.Environment, string(targetJSON), string(params), in.Trigger.ChainSourceRunID, in.Trigger.ChainDepth, in.CreatedAt.UTC().Format(time.RFC3339))
		if err != nil {
			return err
		}
		_, err = configstate.AcquireActivityTx(ctx, q, s.db, configstate.Activity{ID: in.ID, ProjectID: in.ProjectID, RunID: in.ID, OwnerEpoch: s.activityOwner, Kind: "run", Phase: "queued"})
		return err
	}
	dispatch := func(ctx context.Context, id string) error {
		if err := configstate.StartActivityReaders(id, s.activityOwner); err != nil {
			return err
		}
		s.enqueueMu.RLock()
		fn := s.enqueue
		s.enqueueMu.RUnlock()
		if fn == nil {
			return runretry.NewDispatchRejected(ErrPoolStopped)
		}
		if err := fn(id); err != nil {
			if err == ErrQueueFull || err == ErrPoolStopped {
				return runretry.NewDispatchRejected(err)
			}
			return err
		}
		return nil
	}
	return child, dispatch
}

// RejectRetryQueued retires only a definitely unaccepted queued child owned by
// this process. It cannot release executing/finalizing/unknown work.
func RejectRetryQueued(svc Service) func(context.Context, string) error {
	s, ok := svc.(*service)
	if !ok {
		return nil
	}
	return func(ctx context.Context, id string) error {
		return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
			if _, err := s.lockRunProject(tx, q, id); err != nil {
				return err
			}
			if _, err := q.ExecContext(tx, "UPDATE pipeline_runs SET status='failed',finished_at=? WHERE id=? AND status='queued'", time.Now().UTC().Format(time.RFC3339), id); err != nil {
				return err
			}
			return s.releaseQueuedActivityTx(tx, q, id)
		})
	}
}
