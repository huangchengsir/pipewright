package kubernetes

import (
	"context"
	"encoding/json"
	"time"
)

type ReleaseCheckReceipt struct {
	Related    []map[string]any  `json:"related,omitempty"`
	Resource   ReleaseResourceID `json:"resource"`
	UID        string            `json:"uid"`
	Object     map[string]any    `json:"object"`
	Health     ReleaseHealth     `json:"health"`
	ObservedAt int64             `json:"observedAt"`
}

func (s *Service) RecordReleaseChecks(ctx context.Context, id, owner string, checks []ReleaseCheckReceipt) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		parent, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if parent.OwnerEpoch != owner || parent.OwnerFenced || parent.State != "running" {
			return ErrOwner
		}
		if e = s.validateReleaseAuthorization(ctx, q, parent.Plan.Authorization); e != nil {
			return e
		}
		for index, check := range checks {
			found := false
			for _, resource := range parent.Plan.Verify {
				if resource.ID == check.Resource && resource.ExpectedUID == check.UID && (EvaluateReleaseHealth(resource, check.Object, check.Related).State == "ready" || EvaluateReleaseHealth(resource, check.Object, check.Related).State == "declared") {
					checks[index].Health = EvaluateReleaseHealth(resource, check.Object, check.Related)
					found = true
					break
				}
			}
			if !found {
				return ErrConflict
			}
		}
		var actionID string
		if e = q.QueryRowContext(ctx, `SELECT id FROM k8s_release_actions WHERE release_id=? AND state='verified' ORDER BY created_at,id LIMIT 1`, id).Scan(&actionID); e != nil {
			return e
		}
		action, e := s.action(ctx, q, actionID)
		if e != nil {
			return e
		}
		if action.Receipt == nil {
			return ErrInvalid
		}
		action.Receipt.Checks = checks
		return s.saveObservedReceipt(ctx, q, &action)
	})
}
func (s *Service) saveObservedReceipt(ctx context.Context, q dbtx, action *ReleaseAction) error {
	expected := action.Revision
	action.Revision++
	body, e := json.Marshal(action.Receipt)
	if e != nil {
		return e
	}
	defer clear(body)
	cipher, e := s.seal(releaseActionReceiptPurpose, action.ID, action.Revision, body)
	if e != nil {
		return e
	}
	return requireOne(q.ExecContext(ctx, `UPDATE k8s_release_actions SET revision=?,receipt_cipher=?,updated_at=? WHERE id=? AND revision=?`, action.Revision, cipher, time.Now().UnixMilli(), action.ID, expected))
}
func (s *Service) RecordReleaseDeletion(ctx context.Context, id, owner string, expected int64) (out ReleaseAction, resultErr error) {
	resultErr = s.write(ctx, func(ctx context.Context, q dbtx) error {
		action, e := s.action(ctx, q, id)
		if e != nil {
			return e
		}
		parent, e := s.release(ctx, q, action.ReleaseID)
		if e != nil {
			return e
		}
		if parent.OwnerEpoch != owner || parent.OwnerFenced || parent.State != "running" {
			return ErrOwner
		}
		if action.State != "verified" || action.Revision != expected || action.Intent.Verb != "delete" || action.Receipt == nil || action.Receipt.UID != action.Intent.ExpectedUID {
			return ErrConflict
		}
		action.Receipt.Deleted = true
		action.Receipt.ObservedAt = s.now().UnixMilli()
		action.Receipt.Health = ReleaseHealth{State: "declared", Reason: "resource_deleted"}
		if e = s.saveObservedReceipt(ctx, q, &action); e != nil {
			return e
		}
		out = action
		return nil
	})
	return out, resultErr
}
