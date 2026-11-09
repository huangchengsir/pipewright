package kubernetes

import (
	"context"
	"sort"
)

type ReleaseResourceResult struct {
	Deleted    bool              `json:"deleted,omitempty"`
	Resource   ReleaseResourceID `json:"resource"`
	ActionID   string            `json:"actionId,omitempty"`
	State      string            `json:"state"`
	Health     ReleaseHealth     `json:"health"`
	ObservedAt int64             `json:"observedAt,omitempty"`
}

func (s *Service) ReleaseResults(ctx context.Context, id string) ([]ReleaseResourceResult, error) {
	record, e := s.GetRelease(ctx, id)
	if e != nil {
		return nil, e
	}
	actions, e := s.ListReleaseActions(ctx, id)
	if e != nil {
		return nil, e
	}
	results := map[string]ReleaseResourceResult{}
	observations := map[string]*ReleaseActionReceipt{}
	for _, resource := range record.Plan.Resources {
		results[resource.ID.Key()] = ReleaseResourceResult{Resource: resource.ID, State: "pending", Health: ReleaseHealth{State: "pending", Reason: "resource_not_observed"}}
	}
	for _, action := range actions {
		result := ReleaseResourceResult{Resource: action.Intent.Resource, ActionID: action.ID, State: action.State, Health: ReleaseHealth{State: "pending", Reason: "resource_not_observed"}}
		if action.Receipt != nil {
			result.Health = action.Receipt.Health
			result.Deleted = action.Receipt.Deleted
			if action.Intent.Verb == "delete" {
				if previous, ok := results[action.Intent.Resource.Key()]; ok && previous.ActionID != "" && previous.Health.State != "pending" {
					result.Health = previous.Health
				}
			}
			for _, check := range action.Receipt.Checks {
				results[check.Resource.Key()] = ReleaseResourceResult{Resource: check.Resource, State: "verified", Health: check.Health, ObservedAt: check.ObservedAt}
			}
			result.ObservedAt = action.Receipt.ObservedAt
		}
		// An unknown cleanup request has no deletion evidence. It may still
		// display the last verified observation of that exact object incarnation.
		// Keep the unknown action identity/state and never invent Deleted=true.
		if (action.Intent.Verb == "delete" || action.Intent.StepKey == releaseCoreDNSFinalizeStep) && action.State == "outcome_unknown" && action.Receipt == nil {
			if previous := observations[action.Intent.Resource.Key()]; previous != nil && action.Intent.ExpectedUID != "" && previous.UID == action.Intent.ExpectedUID {
				result.Health = previous.Health
				result.ObservedAt = previous.ObservedAt
			}
		}
		if action.Intent.Verb != "delete" {
			delete(observations, action.Intent.Resource.Key())
			receipt := action.Receipt
			if action.State == "verified" && receipt != nil && receipt.Resource == action.Intent.Resource && receipt.UID != "" && receipt.ObservedAt > 0 && receipt.Health.State != "" && receipt.Health.State != "pending" {
				observations[action.Intent.Resource.Key()] = receipt
			}
		}
		results[action.Intent.Resource.Key()] = result
	}
	out := []ReleaseResourceResult{}
	for _, result := range results {
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource.Key() < out[j].Resource.Key() })
	return out, nil
}
