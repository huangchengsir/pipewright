package releaseguard

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
)

var sourceFullSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var sourceReason = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)

func ValidSourceChangePath(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return name != "" && len(name) <= 512 && name != "." && path.Clean(name) == name && !strings.HasPrefix(name, "/") && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00\r\n\t")
}

func investigationInput(r Report) string {
	return digestJSON([]any{r.ProjectID, r.ID, r.Plan.Hash, r.AttemptID, r.EvidenceSetHash, r.Plan.ConfigRevision, r.Targets})
}

// Investigate is an explicit, local-only evidence investigation. No remote
// observer or model is called. The shared deadline applies to all targets.
func (s *Service) Investigate(ctx context.Context, project, id string) (*Report, error) {
	release, admissionErr := maintenance.Enter(ctx, s.db, "guard_investigation")
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	s.mu.Lock()
	if s.closed || s.deleting[project] {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	if err := s.projectExists(ctx, project); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	d, err := s.load(ctx, project, id)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if !s.now().Before(d.Report.EvidenceExpiresAt) || !s.planRevisionCurrent(ctx, d.Report.Plan) {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	input := investigationInput(d.Report)
	if d.Report.Investigation.Status == "completed" && d.Report.Investigation.InputHash == input && !retryableInvestigation(d.Report.Investigation) {
		s.mu.Unlock()
		return &d.Report, nil
	}
	revision, planHash, evidenceHash := d.Private.Revision, d.Report.Plan.Hash, d.Report.EvidenceSetHash
	asOf := s.now().UTC()
	if d.Report.FinishedAt != nil {
		asOf = *d.Report.FinishedAt
	}
	s.mu.Unlock()
	workCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	evidence, err := s.investigationEvidence(workCtx, id)
	if err != nil {
		return nil, err
	}
	result := Investigation{InputHash: input, Status: "completed", BaselineStatus: "unavailable", SourceComparisons: []SourceComparison{}, Baselines: []TargetBaseline{}, Candidates: []Candidate{}, Limitations: []string{"temporal_relation_is_not_causation", "business_effect_not_observed", "source_comparison_is_direct_trees"}}
	timeline := append([]TimelineItem{}, d.Report.Timeline...)
	seenTimeline := map[string]bool{}
	for _, item := range timeline {
		seenTimeline[item.ID] = true
	}
	currentTargets, _ := s.ledger.ListTargets(workCtx, d.Report.AttemptID)
	verified, withHistory, addedTimeline := 0, false, 0
	for _, observed := range d.Report.Targets {
		baseline := TargetBaseline{TargetID: observed.ID, Status: "unavailable", MissingEvidence: []string{}, HistoricalCandidates: []HistoricalBaseline{}}
		comparison := SourceComparison{TargetID: observed.ID, WorkloadKey: observed.WorkloadKey, Reason: "immediate_baseline_unavailable", ComparisonKind: "direct_trees", Files: []SourceFileChange{}}
		var current *deployattempt.Target
		for i := range currentTargets {
			v := &currentTargets[i]
			if sameInvestigationScope(d.Report, observed, *v) && v.ID == observed.ID && v.AttemptID == d.Report.AttemptID && v.Source.RunID == d.Report.RunID {
				if current != nil {
					current = nil
					break
				}
				current = v
			}
		}
		history, herr := s.ledger.ListByWorkload(workCtx, observed.WorkloadKey, 10)
		if herr != nil {
			baseline.MissingEvidence = append(baseline.MissingEvidence, "deployment_history_unavailable")
			history = nil
		}
		var activation time.Time
		if current != nil {
			bound := false
			for _, receipt := range current.Receipts {
				if receipt.At.After(asOf) {
					continue
				}
				if receipt.Kind == deployattempt.ActivationStarted && (activation.IsZero() || receipt.At.Before(activation)) {
					activation = receipt.At
				}
				if receipt.Kind == deployattempt.Activated && deployattempt.ValidContainerID(observed.ContainerID) && receipt.ActualImage.ContainerID == observed.ContainerID {
					bound = true
				}
			}
			if !bound {
				current = nil
				activation = time.Time{}
			}
		}
		// Timeline facts cite immutable ledger receipt identities, rather than
		// inventing guard evidence IDs that would not resolve in its evidence API.
		for _, prior := range history {
			if !sameInvestigationScope(d.Report, observed, prior) {
				continue
			}
			for _, receipt := range prior.Receipts {
				if receipt.At.After(asOf) {
					continue
				}
				key := "deployment_receipt:" + receipt.ID
				if !seenTimeline[key] && addedTimeline < 100 {
					timeline = append(timeline, TimelineItem{ID: key, At: receipt.At, Kind: "deployment_" + receipt.Kind, TargetID: observed.ID, Summary: receipt.Kind + "; attempt=" + prior.AttemptID + "; run=" + prior.Source.RunID, EvidenceIDs: []string{}})
					seenTimeline[key] = true
					addedTimeline++
				}
				priorBoundary := activation
				if priorBoundary.IsZero() {
					priorBoundary = d.Report.CreatedAt
				}
				if prior.AttemptID != d.Report.AttemptID && receipt.Kind == deployattempt.Activated && receipt.At.Before(priorBoundary) && len(baseline.HistoricalCandidates) < 3 {
					baseline.HistoricalCandidates = append(baseline.HistoricalCandidates, HistoricalBaseline{AttemptID: prior.AttemptID, RunID: prior.Source.RunID, At: receipt.At, AgeSeconds: max(int64(0), int64(asOf.Sub(receipt.At).Seconds())), Status: "historical_candidate_not_immediate"})
				}
			}
		}
		if len(baseline.HistoricalCandidates) > 0 {
			withHistory = true
		}
		var immediate *Evidence
		var snapshot RuntimeSnapshot
		if d.Report.Plan.Mode == "attached" && !activation.IsZero() {
			for i := range evidence {
				e := &evidence[i]
				if e.Kind != "baseline" || !baselineTargetMatches(d.Report.Plan, observed, *e) || e.Truncated || !e.CollectedAt.Before(activation) || !e.EventAt.Before(activation) {
					continue
				}
				body, _ := json.Marshal(e.Value)
				var v RuntimeSnapshot
				if json.Unmarshal(body, &v) != nil || v.Outcome != "ok" || v.Truncated || v.TargetChanged || !deployattempt.ValidContainerID(v.ContainerID) || v.CollectedAt.IsZero() || !v.CollectedAt.Before(activation) {
					continue
				}
				if immediate == nil || immediate.CollectedAt.Before(e.CollectedAt) {
					immediate = e
					snapshot = v
				}
			}
		}
		var previous *deployattempt.Target
		if immediate != nil {
			baseline.EvidenceID = immediate.ID
			baseline.AgeSeconds = max(int64(0), int64(asOf.Sub(immediate.CollectedAt).Seconds()))
			baseline.Status = "immediate_identity_unlinked"
			matches := 0
			for i := range history {
				prior := &history[i]
				if prior.AttemptID == d.Report.AttemptID || !sameInvestigationScope(d.Report, observed, *prior) {
					continue
				}
				for _, receipt := range prior.Receipts {
					if receipt.Kind != deployattempt.Activated || receipt.ActualImage.ContainerID != snapshot.ContainerID || !receipt.At.Before(immediate.EventAt) {
						continue
					}
					if receipt.ActualImage.ContainerCreatedAt != "" && receipt.ActualImage.ContainerCreatedAt != snapshot.CreatedAt {
						continue
					}
					previous = prior
					matches++
					break
				}
			}
			if matches == 1 {
				baseline.Status = "verified_immediate"
				baseline.PriorAttemptID = previous.AttemptID
				baseline.PriorRunID = previous.Source.RunID
				verified++
			} else {
				previous = nil
				baseline.MissingEvidence = append(baseline.MissingEvidence, "prior_activation_not_uniquely_identified")
			}
		} else {
			baseline.MissingEvidence = append(baseline.MissingEvidence, "immediate_predeployment_evidence_unavailable")
		}
		if previous != nil && current != nil {
			base, baseReason := s.verifiedSource(workCtx, project, previous.Source)
			next, nextReason := s.verifiedSource(workCtx, project, current.Source)
			comparison.BaseSHA, comparison.CurrentSHA = base, next
			switch {
			case baseReason != "":
				comparison.Reason = baseReason
			case nextReason != "":
				comparison.Reason = nextReason
			case previous.Source.ConfigRevision != current.Source.ConfigRevision || current.Source.ConfigRevision != d.Report.Plan.ConfigRevision:
				comparison.Reason = "repository_revision_not_comparable"
			case s.compareSource == nil:
				comparison.Reason = "source_cache_unavailable"
			case workCtx.Err() != nil:
				comparison.Reason = "source_comparison_cancelled"
			default:
				comparison = validatedSourceComparison(s.compareSource(workCtx, project, base, next), observed, base, next)
			}
		} else if current == nil {
			comparison.Reason = "current_deployment_identity_unavailable"
		}
		for _, c := range observed.Conditions {
			if c.Result != "failed" {
				continue
			}
			counter := []string{}
			if immediate != nil && ((c.Name == "liveness" && (!snapshot.Running || snapshot.OOMKilled)) || (c.Name == "health" && snapshot.Health == "unhealthy")) {
				counter = append(counter, immediate.ID)
			}
			missing := []string{"causation_not_proven", "business_effect_not_observed"}
			if immediate == nil {
				missing = append(missing, "immediate_baseline_unavailable")
			}
			if !comparison.Available {
				missing = append(missing, comparison.Reason)
			}
			result.Candidates = append(result.Candidates, Candidate{ID: observed.ID + ":" + c.Name, Title: c.Reason, SupportingEvidenceIDs: append([]string{}, c.EvidenceIDs...), CounterEvidenceIDs: counter, MissingEvidence: missing, NextReadOnlyCheck: "review_this_workload_baseline_and_deployment_evidence"})
		}
		result.Baselines = append(result.Baselines, baseline)
		result.SourceComparisons = append(result.SourceComparisons, comparison)
	}
	if verified == len(d.Report.Targets) && verified > 0 {
		result.BaselineStatus = "verified_immediate"
	} else if verified > 0 {
		result.BaselineStatus = "partial_immediate"
	} else if withHistory {
		result.BaselineStatus = "historical_candidates_only"
	}
	if addedTimeline == 100 {
		result.Limitations = append(result.Limitations, "deployment_timeline_limited")
	}
	sort.SliceStable(timeline, func(i, j int) bool {
		if timeline[i].At.Equal(timeline[j].At) {
			return timeline[i].ID < timeline[j].ID
		}
		return timeline[i].At.Before(timeline[j].At)
	})
	now := s.now().UTC()
	result.InvestigatedAt = &now
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, err := s.load(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if err = s.projectExists(ctx, project); err != nil {
		return nil, err
	}
	if !s.now().Before(latest.Report.EvidenceExpiresAt) || s.deleting[project] || s.closed {
		return nil, ErrUnavailable
	}
	if latest.Report.Investigation.Status == "completed" && latest.Report.Investigation.InputHash == input && latest.Private.Revision != revision {
		return &latest.Report, nil
	}
	if latest.Private.Revision != revision || latest.Report.Plan.Hash != planHash || latest.Report.EvidenceSetHash != evidenceHash || !s.planRevisionCurrent(ctx, latest.Report.Plan) {
		latest.Report.Investigation.Status = "stale"
		latest.Report.Investigation.Limitations = appendUnique(latest.Report.Investigation.Limitations, "investigation_input_changed")
		if err = s.save(ctx, latest, "investigation_stale", nil); err != nil {
			return nil, err
		}
		return &latest.Report, ErrConflict
	}
	if sameInvestigationFacts(latest.Report.Investigation, result) && digestJSON(latest.Report.Timeline) == digestJSON(timeline) {
		return &latest.Report, nil
	}
	latest.Report.Investigation = result
	latest.Report.Timeline = timeline
	if len(result.Candidates) > 0 {
		latest.Report.AttributionStatus = "undetermined"
	} else {
		latest.Report.AttributionStatus = "not_investigated"
	}
	if err = s.save(ctx, latest, "investigated", nil); err != nil {
		return nil, err
	}
	return &latest.Report, nil
}

func retryableInvestigation(i Investigation) bool {
	for _, c := range i.SourceComparisons {
		if c.Available {
			continue
		}
		switch c.Reason {
		case "source_cache_unavailable", "source_cache_missing", "source_cache_unreadable", "source_cache_busy", "base_commit_not_cached", "current_commit_not_cached", "source_comparison_cancelled", "source_tree_unavailable":
			return true
		}
	}
	return false
}

func sameInvestigationFacts(a, b Investigation) bool {
	a.InvestigatedAt = nil
	b.InvestigatedAt = nil
	return digestJSON(a) == digestJSON(b)
}

func sameInvestigationScope(r Report, observed TargetObservation, t deployattempt.Target) bool {
	return t.Source.ProjectID == r.ProjectID && t.Source.Environment == r.Plan.Environment && t.WorkloadKey == observed.WorkloadKey && t.ServerID == observed.ServerID && t.ContainerName == observed.ContainerName
}

func baselineTargetMatches(p Plan, t TargetObservation, e Evidence) bool {
	if e.TargetID == t.ID {
		return true
	}
	for _, pt := range p.Targets {
		if pt.WorkloadKey == t.WorkloadKey && pt.ServerID == t.ServerID && pt.ID == e.TargetID {
			return true
		}
	}
	return false
}

func (s *Service) investigationEvidence(ctx context.Context, id string) ([]Evidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT evidence_json FROM release_guard_evidence WHERE guard_id=? ORDER BY sequence LIMIT 1001`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Evidence{}
	for rows.Next() {
		if len(items) == 1000 {
			return nil, ErrCapacity
		}
		var body string
		var e Evidence
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(body), &e) != nil {
			return nil, ErrUnavailable
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (s *Service) verifiedSource(ctx context.Context, project string, source deployattempt.Source) (string, string) {
	if s.runs == nil || !sourceFullSHA.MatchString(source.ResolvedFullSHA) {
		return "", "resolved_source_unavailable"
	}
	r, err := s.runs.Get(ctx, source.RunID)
	if err != nil || r.ProjectID != project || r.Trigger.ResolvedEnvironment != source.Environment {
		return "", "source_run_unavailable"
	}
	if r.ResolvedCommitConflict {
		return "", "source_revision_conflict"
	}
	if r.ResolvedCommitSHA != source.ResolvedFullSHA {
		return "", "source_revision_not_confirmed"
	}
	return source.ResolvedFullSHA, ""
}

func validatedSourceComparison(c SourceComparison, t TargetObservation, base, current string) SourceComparison {
	c.TargetID = t.ID
	c.WorkloadKey = t.WorkloadKey
	if !c.Available {
		// An unavailable adapter need not repeat the endpoints. They remain
		// authoritative caller facts, never fields supplied by the adapter.
		c.BaseSHA = base
		c.CurrentSHA = current
		c.ComparisonKind = "direct_trees"
		c.Files = []SourceFileChange{}
		if !sourceReason.MatchString(c.Reason) {
			c.Reason = "source_comparison_unavailable"
		}
		return c
	}
	if c.BaseSHA != base || c.CurrentSHA != current || c.ComparisonKind != "direct_trees" || len(c.Files) > 40 {
		return SourceComparison{TargetID: t.ID, WorkloadKey: t.WorkloadKey, BaseSHA: base, CurrentSHA: current, ComparisonKind: "direct_trees", Reason: "source_comparison_identity_invalid", Files: []SourceFileChange{}}
	}
	if !sourceReason.MatchString(c.Reason) && c.Reason != "" {
		c.Reason = "source_comparison_unavailable"
	}
	seen := map[string]bool{}
	for _, f := range c.Files {
		if !ValidSourceChangePath(f.Path) || seen[f.Path] || (f.Status != "added" && f.Status != "modified" && f.Status != "deleted") {
			c.Available = false
			c.Reason = "source_comparison_invalid"
			c.Files = []SourceFileChange{}
			return c
		}
		seen[f.Path] = true
	}
	return c
}
