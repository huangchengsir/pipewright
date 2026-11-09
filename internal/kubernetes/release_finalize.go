package kubernetes

import (
	"context"
	"errors"
)

// FinalizeReleaseRun fences preparations/writers on terminal run paths. Durable
// paused work is handled by the suspension coordinator and keeps its gates.
func (s *Service) FinalizeReleaseRun(ctx context.Context, run string) error {
	records, e := s.ListReleases(ctx, run)
	if e != nil {
		return e
	}
	var result error
	for _, record := range records {
		if record.OwnerFenced || record.State == "paused" || record.State == "resume_requested" {
			continue
		}
		state := "failed"
		facts, e := s.ListReleaseActions(ctx, record.ID)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		for _, fact := range facts {
			if fact.State == "verified" {
				state = "partial_failed"
			}
			if fact.State == "outcome_unknown" || fact.State == "submitted" {
				state = "outcome_unknown"
				break
			}
		}
		record, e = s.SetReleaseTerminal(ctx, record.ID, record.OwnerEpoch, record.Revision, state, "run_settled_before_completion")
		result = errors.Join(result, e)
		if e == nil && record.Cleanup == CleanupVerified && state != "outcome_unknown" {
			result = errors.Join(result, s.ReleaseGates(ctx, record.ID, record.OwnerEpoch))
		}
	}
	return result
}
