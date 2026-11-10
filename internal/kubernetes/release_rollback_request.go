package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type ReleaseRollbackRequest struct {
	SourceID         string `json:"-"`
	ExpectedRevision int64  `json:"expectedRevision"`
	PlanID           string `json:"planId"`
	RequestID        string `json:"requestId"`
}
type ReleaseRollbackStart struct {
	RunID   string
	Release ReleaseRecord
}
type ReleaseRollbackCommit func(context.Context, func(context.Context) error) error
type ReleaseRollbackStarter func(context.Context, ReleaseRollbackRequest, ReleaseRollbackCommit) (ReleaseRollbackStart, error)

func releaseRollbackRequestKey(in ReleaseRollbackRequest) (string, error) {
	if !validID(in.SourceID) || !validID(in.RequestID) || in.ExpectedRevision < 1 || !cleanText(in.PlanID, 128) || in.PlanID == "" {
		return "", ErrInvalid
	}
	return releaseByteDigest([]byte(in.SourceID + "\x00" + in.RequestID)), nil
}
func (s *Service) LookupReleaseRollback(ctx context.Context, in ReleaseRollbackRequest) (ReleaseRollbackStart, error) {
	key, e := releaseRollbackRequestKey(in)
	if e != nil {
		return ReleaseRollbackStart{}, e
	}
	var source, plan, id, run string
	var revision int64
	e = configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT source_id,source_revision,source_plan_id,release_id,run_id FROM k8s_release_rollback_requests WHERE id=?`, key).Scan(&source, &revision, &plan, &id, &run)
	if e != nil {
		return ReleaseRollbackStart{}, rowError(e)
	}
	if source != in.SourceID || revision != in.ExpectedRevision || plan != in.PlanID {
		return ReleaseRollbackStart{}, ErrConflict
	}
	r, e := s.GetRelease(ctx, id)
	return ReleaseRollbackStart{RunID: run, Release: r}, e
}

// RecordReleaseRollbackRequest must share the transaction that creates the
// queued run and its immutable capture. The dispatcher never invents a run.
func (s *Service) RecordReleaseRollbackRequest(ctx context.Context, in ReleaseRollbackRequest, r ReleaseRecord) error {
	key, e := releaseRollbackRequestKey(in)
	if e != nil {
		return e
	}
	q := configstate.Queryer(ctx, s.db)
	if q == s.db {
		return ErrOwner
	}
	fresh, e := s.release(ctx, q, r.ID)
	if e != nil {
		return e
	}
	if fresh.State != "prepared" || fresh.Plan.RollbackOf != in.SourceID || fresh.Plan.RollbackRevision != in.ExpectedRevision || fresh.Plan.RollbackPlanID != in.PlanID || !fresh.Plan.ForceApproval {
		return ErrConflict
	}
	var status string
	if e = q.QueryRowContext(ctx, `SELECT status FROM pipeline_runs WHERE id=?`, r.RunID).Scan(&status); e != nil {
		return e
	}
	if status != "queued" || fresh.ReaderOwnerEpoch == "" {
		return ErrOwner
	}
	now := s.now().UnixMilli()
	if _, e = q.ExecContext(ctx, `INSERT INTO k8s_release_rollback_requests(id,source_id,source_revision,source_plan_id,release_id,run_id,created_at) VALUES(?,?,?,?,?,?,?)`, key, in.SourceID, in.ExpectedRevision, in.PlanID, r.ID, r.RunID, now); e != nil {
		return e
	}
	id := "kcont_" + uuid.NewString()
	cp := ReleaseSuspension{Version: 2, RunID: r.RunID, NodeKey: r.NodeKey, PlanID: r.PlanID}
	raw, _ := json.Marshal(cp)
	defer clear(raw)
	cipher, e := s.seal(releaseCheckpointPurpose, id, 1, raw)
	if e != nil {
		return e
	}
	_, e = q.ExecContext(ctx, `INSERT INTO k8s_release_continuations(id,release_id,run_id,release_revision,action,state,checkpoint_cipher,created_at,updated_at) VALUES(?,?,?,?,'rollback','pending',?,?,?)`, id, r.ID, r.RunID, r.Revision, cipher, now, now)
	return e
}

// FindReleaseRollbackRequest is a read-only lost-ack lookup. Absence does not
// authorize a new request ID or a replay of an in-flight preparation.
func (s *Service) FindReleaseRollbackRequest(ctx context.Context, source, id string) (ReleaseRollbackStart, error) {
	in := ReleaseRollbackRequest{SourceID: source, RequestID: id, ExpectedRevision: 1, PlanID: "lookup"}
	key, e := releaseRollbackRequestKey(in)
	if e != nil {
		return ReleaseRollbackStart{}, e
	}
	e = configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT source_revision,source_plan_id FROM k8s_release_rollback_requests WHERE id=? AND source_id=?`, key, source).Scan(&in.ExpectedRevision, &in.PlanID)
	if e != nil {
		return ReleaseRollbackStart{}, rowError(e)
	}
	return s.LookupReleaseRollback(ctx, in)
}
