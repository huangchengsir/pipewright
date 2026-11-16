package releaseguard

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// Derived history belongs to its source runs as well as the current report.
// Project this part only; the current run's observation remains readable. The
// stored report is not modified when a narrower reader opens it.
func (s *Service) scopeHistoricalReport(ctx context.Context, r *Report) {
	enabled, err := tenancy.Enabled(ctx, s.db)
	if err == nil && !enabled {
		return
	}
	visible := map[string]bool{}
	allowed := func(id string) bool {
		if id == "" {
			return false
		}
		if v, ok := visible[id]; ok {
			return v
		}
		v := err == nil && tenancy.Check(ctx, s.db, "guard.read", "run", id) == nil
		visible[id] = v
		return v
	}
	all := true
	for _, baseline := range r.Investigation.Baselines {
		if baseline.PriorRunID != "" && !allowed(baseline.PriorRunID) {
			all = false
		}
		for _, previous := range baseline.HistoricalCandidates {
			if !allowed(previous.RunID) {
				all = false
			}
		}
	}
	timeline := make([]TimelineItem, 0, len(r.Timeline))
	for _, item := range r.Timeline {
		// Older records lack a structured source identity. They cannot prove that
		// their human-readable receipt summary is inside this caller's scope.
		if strings.HasPrefix(item.Kind, "deployment_") && !allowed(item.SourceRunID) {
			continue
		}
		timeline = append(timeline, item)
	}
	r.Timeline = timeline
	if !all {
		r.Investigation = Investigation{Status: "not_investigated", BaselineStatus: "unavailable", SourceComparisons: []SourceComparison{}, Baselines: []TargetBaseline{}, Candidates: []Candidate{}, Limitations: []string{"historical_data_outside_access_scope"}}
		r.AttributionStatus = "not_investigated"
	}
}
