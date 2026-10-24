package releaseguard

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/target"
)

func (s *Service) launchLocked(id, project string) {
	if s.closed {
		return
	}
	if _, ok := s.workers[id]; ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.workers[id] = cancel
	s.wg.Add(1)
	go s.work(ctx, project, id)
}

func (s *Service) collectLogs(ctx context.Context, project, id, targetID string) {
	s.mu.Lock()
	d, err := s.load(ctx, project, id)
	if err != nil || !d.Report.Plan.IncludeLogs || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" || !s.planRevisionCurrent(ctx, d.Report.Plan) {
		s.mu.Unlock()
		return
	}
	cap, ok := d.Private.Bindings[targetID]
	if !ok {
		s.mu.Unlock()
		return
	}
	now := s.now().UTC()
	from := d.Private.LastLogs[targetID]
	if now.Sub(from) < time.Minute {
		s.mu.Unlock()
		return
	}
	if d.Private.LogBytes[targetID] >= 256<<10 || d.Private.EvidenceBytes >= maxLogEvidenceBytes {
		s.logCapacityLocked(ctx, d, targetID)
		s.mu.Unlock()
		return
	}
	gap := now.Sub(from) > time.Minute+time.Second
	if gap {
		from = now.Add(-time.Minute)
	}
	var cid string
	for _, t := range d.Report.Targets {
		if t.ID == targetID && !t.Terminated {
			cid = t.ContainerID
		}
	}
	if cid == "" {
		s.mu.Unlock()
		return
	}
	attemptID := d.Report.AttemptID
	pt := PlanTarget{}
	for _, t := range d.Report.Targets {
		if t.ID == targetID {
			pt = t.PlanTarget
		}
	}
	// Reserve this window before dispatch. A discarded reply or collection error
	// must not cause another read on the next 30-second status tick.
	d.Private.LastLogs[targetID] = now
	if err = s.save(ctx, d, "logs_requested", nil); err != nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if s.superseded(ctx, pt, attemptID) {
		return
	}
	select {
	case s.sshSem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	text, truncated, reason := s.observer.Logs(ctx, cap, cid, from, now)
	<-s.sshSem
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err = s.load(ctx, project, id)
	if err != nil || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" || !s.planRevisionCurrent(ctx, d.Report.Plan) {
		return
	}
	remaining := (256 << 10) - d.Private.LogBytes[targetID]
	// Reserve evidence capacity for mandatory typed status facts.
	if d.Private.EvidenceBytes >= maxLogEvidenceBytes {
		s.logCapacityLocked(ctx, d, targetID)
		return
	}
	text = s.scrub(text)
	limit := max(0, min(remaining, 32<<10))
	e := Evidence{ID: newEvidenceID(), Sequence: d.Report.EventsCursor + 1, TargetID: targetID, Kind: "logs", EventAt: now, CollectedAt: now, Reason: safeReason(reason), Summary: "untrusted_windowed_logs"}
	for {
		e.Truncated = truncated
		e.Value = map[string]any{"from": from, "until": now, "text": text, "gap": gap}
		encoded, _ := json.Marshal(e)
		if len(text) <= limit && len(encoded) <= maxEvidenceSize {
			break
		}
		truncated = true
		// JSON can expand HTML/control bytes sixfold. Recheck the entire saved
		// evidence, including its real sequence, after every bounded reduction.
		cut := min(limit, len(text)/2)
		text = s.scrub(text[:cut]) // Repairs a cut UTF-8 suffix and masks secret prefixes.
	}
	d.Private.LogBytes[targetID] += len(text)
	if d.Private.LogBytes[targetID] >= 256<<10 {
		truncated = true
		for i := range d.Report.Targets {
			if d.Report.Targets[i].ID == targetID {
				d.Report.Targets[i].Limitations = appendUnique(d.Report.Targets[i].Limitations, "logs_capacity_reached")
			}
		}
	}
	d.Private.LastLogs[targetID] = now
	if gap {
		for i := range d.Report.Targets {
			if d.Report.Targets[i].ID == targetID {
				d.Report.Targets[i].Limitations = appendUnique(d.Report.Targets[i].Limitations, "log_time_gap")
			}
		}
	}
	e.Truncated = truncated
	if err = s.save(ctx, d, "logs_collected", &e); err != nil {
		// The reserved window remains consumed even if persistence fails. Record
		// a gap when storage becomes available rather than inventing evidence.
		current, loadErr := s.load(ctx, project, id)
		if loadErr != nil || terminal(current.Report.ExecutionStatus) || current.Report.ExecutionStatus == "stopping" {
			return
		}
		for i := range current.Report.Targets {
			if current.Report.Targets[i].ID == targetID {
				current.Report.Targets[i].Limitations = appendUnique(current.Report.Targets[i].Limitations, "log_time_gap")
			}
		}
		_ = s.save(ctx, current, "logs_unavailable", nil)
	}
}

func (s *Service) logCapacityLocked(ctx context.Context, d *document, targetID string) {
	for i := range d.Report.Targets {
		t := &d.Report.Targets[i]
		if t.ID != targetID {
			continue
		}
		before := len(t.Limitations)
		t.Limitations = appendUnique(t.Limitations, "logs_capacity_reached")
		if len(t.Limitations) != before {
			_ = s.save(ctx, d, "logs_capacity_reached", nil)
		}
		return
	}
}

func (s *Service) work(ctx context.Context, project, id string) {
	defer func() {
		s.mu.Lock()
		delete(s.workers, id)
		s.finishPendingLocked(project, id)
		s.mu.Unlock()
		s.wg.Done()
	}()
	for {
		s.mu.Lock()
		d, err := s.load(context.Background(), project, id)
		if err != nil {
			s.mu.Unlock()
			return
		}
		if terminal(d.Report.ExecutionStatus) {
			s.mu.Unlock()
			return
		}
		if ctx.Err() != nil || d.Report.ExecutionStatus == "stopping" || s.closed {
			status, reason := "interrupted", "service_stopped"
			if d.Report.ExecutionStatus == "stopping" {
				status, reason = "stopped", "user_stopped"
			}
			s.finishLocked(context.Background(), d, status, reason)
			s.mu.Unlock()
			return
		}
		if err = s.projectExists(ctx, project); err != nil || !s.planRevisionCurrent(ctx, d.Report.Plan) {
			s.finishLocked(context.Background(), d, "blocked", "project_configuration_changed")
			s.mu.Unlock()
			return
		}
		now := s.now().UTC()
		due := []string{}
		allDone := true
		for i := range d.Report.Targets {
			t := &d.Report.Targets[i]
			if t.Terminated || t.Completed {
				continue
			}
			if now.After(t.Deadline.Add(5 * time.Second)) {
				t.Completed = true
				continue
			}
			allDone = false
			if t.ContainerID != "" && !now.Before(d.Private.Schedule[t.ID]) {
				due = append(due, t.ID)
			}
		}
		if allDone {
			for i := range d.Report.Targets {
				if err := s.deploymentFactsLocked(ctx, d, &d.Report.Targets[i], false); err != nil {
					s.finishLocked(context.Background(), d, "tool_error", "deployment_evidence_unavailable")
					s.mu.Unlock()
					return
				}
			}
			s.finishLocked(context.Background(), d, "completed", "observation_window_ended")
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		for _, targetID := range due {
			if ctx.Err() != nil {
				break
			}
			s.sample(ctx, project, id, targetID)
			if ctx.Err() == nil {
				s.collectLogs(ctx, project, id, targetID)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Service) superseded(ctx context.Context, t PlanTarget, attemptID string) bool {
	at, _, err := s.replacementAt(ctx, t, attemptID)
	return err != nil || at != nil
}

// Server-side receipt time bounds replacement. Host FinishedAt is not used
// to order a concurrent deployment. The query cannot miss a replacement just
// because more than one page of newer attempts has accumulated.
func (s *Service) replacementAt(ctx context.Context, t PlanTarget, attemptID string) (*time.Time, string, error) {
	original, err := s.ledger.GetTarget(ctx, t.ID)
	if err != nil {
		return nil, "deployment_lineage_unavailable", err
	}
	if original.AttemptID != attemptID {
		return nil, "deployment_lineage_unavailable", ErrConflict
	}
	var earliest *time.Time
	var activationStart, activated time.Time
	for _, r := range original.Receipts {
		if r.Kind == deployattempt.ActivationStarted && (activationStart.IsZero() || r.At.Before(activationStart)) {
			activationStart = r.At
		}
		if r.Kind == deployattempt.Activated && (activated.IsZero() || r.At.Before(activated)) {
			activated = r.At
		}
		if r.Kind == deployattempt.RolledBack || r.Kind == deployattempt.OutcomeUnknown {
			at := r.At
			if earliest == nil || at.Before(*earliest) {
				earliest = &at
			}
		}
	}
	if activationStart.IsZero() {
		activationStart = activated
	}
	if activationStart.IsZero() {
		if earliest != nil {
			return earliest, "superseded_by_deployment", nil
		}
		return nil, "deployment_lineage_unavailable", ErrUnavailable
	}
	// A target can spend arbitrarily long pulling an image before activation.
	// Its creation time does not order writes to a shared workload. Later or
	// overlapping activation receipts end this observation, including an older
	// activation still in flight. An entirely completed prior activation does not.
	rows, err := s.db.QueryContext(ctx, `SELECT r.receipt_json,t.outcome,COALESCE(activation.receipt_json,'') FROM deployment_attempt_receipts r JOIN deployment_attempt_targets t ON t.id=r.target_id LEFT JOIN deployment_attempt_receipts activation ON activation.target_id=t.id AND activation.kind='activation_started' WHERE t.workload_key=? AND t.id<>? AND r.kind IN ('activation_started','activated','rolled_back','outcome_unknown','deploy_failed') AND (r.created_at>=? OR (t.outcome='activation_started' AND r.kind='activation_started'))`, t.WorkloadKey, t.ID, activationStart.UnixMilli())
	if err != nil {
		return nil, "deployment_lineage_unavailable", err
	}
	defer rows.Close()
	for rows.Next() {
		var body, outcome, activationBody string
		var receipt deployattempt.Receipt
		if err = rows.Scan(&body, &outcome, &activationBody); err != nil {
			return nil, "deployment_lineage_unavailable", err
		}
		if json.Unmarshal([]byte(body), &receipt) != nil || receipt.At.IsZero() {
			return nil, "deployment_lineage_unavailable", ErrUnavailable
		}
		if receipt.Kind == deployattempt.DeployFailed {
			// A failed pull cannot replace a workload. Once activation starts,
			// however, rm/run may have changed it before docker run fails. A
			// failed interval ending after our activation began is overlapping;
			// its start, not its failure acknowledgement, bounds uncertainty.
			if receipt.At.Before(activationStart) || activationBody == "" {
				continue
			}
			var start deployattempt.Receipt
			if json.Unmarshal([]byte(activationBody), &start) != nil || start.Kind != deployattempt.ActivationStarted || start.At.IsZero() {
				return nil, "deployment_lineage_unavailable", ErrUnavailable
			}
			if earliest == nil || start.At.Before(*earliest) {
				at := start.At
				earliest = &at
			}
			continue
		}
		// SQL stores milliseconds; the immutable receipt retains nanoseconds.
		// Compare the full server timestamp so an older receipt in the same
		// millisecond cannot supersede a newer, non-overlapping activation.
		if receipt.At.Before(activationStart) && !(outcome == deployattempt.ActivationStarted && receipt.Kind == deployattempt.ActivationStarted) {
			continue
		}
		at := receipt.At
		if earliest == nil || at.Before(*earliest) {
			earliest = &at
		}
	}
	return earliest, "superseded_by_deployment", rows.Err()
}

func (s *Service) inspect(ctx context.Context, cap target.ConnectionSnapshot, id string, baseline bool) RuntimeSnapshot {
	cancelled := func() RuntimeSnapshot {
		return RuntimeSnapshot{Outcome: "unavailable", Reason: "inspection_cancelled", CollectedAt: s.now().UTC(), RepoDigests: []string{}}
	}
	if ctx.Err() != nil {
		return cancelled()
	}
	select {
	case s.sshSem <- struct{}{}:
		defer func() { <-s.sshSem }()
	case <-ctx.Done():
		return cancelled()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return cancelled()
	}
	if baseline {
		return s.observer.Baseline(ctx, cap, id)
	}
	return s.observer.Inspect(ctx, cap, id)
}

func (s *Service) sample(ctx context.Context, project, id, targetID string) {
	s.mu.Lock()
	d, err := s.load(ctx, project, id)
	if err != nil || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" {
		s.mu.Unlock()
		return
	}
	var pt PlanTarget
	var cap target.ConnectionSnapshot
	found := false
	for _, t := range d.Report.Targets {
		if t.ID == targetID && !t.Terminated {
			pt = t.PlanTarget
			cap, found = d.Private.Bindings[targetID]
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return
	}
	attemptID := d.Report.AttemptID
	s.mu.Unlock()
	changeAt, changeReason, lineageErr := s.replacementAt(ctx, pt, attemptID)
	v := RuntimeSnapshot{Outcome: "target_changed", Reason: changeReason, CollectedAt: s.now().UTC(), RepoDigests: []string{}}
	if lineageErr != nil {
		v.Outcome = "unavailable"
	}
	if changeAt == nil && lineageErr == nil {
		v = s.inspect(ctx, cap, pt.ContainerID, false)
	}
	if changeAt == nil && lineageErr == nil {
		at, reason, e := s.replacementAt(ctx, pt, attemptID)
		if e != nil {
			v = RuntimeSnapshot{Outcome: "unavailable", Reason: reason, CollectedAt: s.now().UTC(), RepoDigests: []string{}}
		} else if at != nil {
			if v.CollectedAt.Before(*at) && v.Outcome == "ok" {
				v.TargetChanged = true
			} else {
				v = RuntimeSnapshot{Outcome: "target_changed", Reason: reason, CollectedAt: s.now().UTC(), RepoDigests: []string{}}
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err = s.load(ctx, project, id)
	if err != nil || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" {
		return
	}
	if s.projectExists(ctx, project) != nil || !s.planRevisionCurrent(ctx, d.Report.Plan) {
		return
	}
	for i := range d.Report.Targets {
		t := &d.Report.Targets[i]
		if t.ID != targetID || t.Terminated {
			continue
		}
		// Allocate the evidence identity before evaluating the rule; save keeps
		// that same ID so every condition citation resolves.
		e := Evidence{TargetID: targetID, Kind: "runtime", EventAt: v.CollectedAt, CollectedAt: s.now().UTC(), Reason: safeReason(v.Reason), Truncated: v.Truncated, Summary: "typed_runtime_sample", Value: v}
		if err = s.recordSampleLocked(ctx, d, t, v, &e); err != nil {
			if old, loadErr := s.load(context.Background(), project, id); loadErr == nil {
				retainDeploymentHealthFailure(old, d)
				reason := "evidence_persistence_unavailable"
				if errors.Is(err, ErrCapacity) {
					reason = "evidence_capacity_reached"
					for i := range old.Report.Targets {
						old.Report.Targets[i].Limitations = appendUnique(old.Report.Targets[i].Limitations, reason)
					}
				}
				s.finishLocked(context.Background(), old, "tool_error", reason)
			}
		}
		return
	}
}

func (s *Service) recordSampleLocked(ctx context.Context, d *document, t *TargetObservation, v RuntimeSnapshot, e *Evidence) error {
	if err := s.deploymentFactsLocked(ctx, d, t, false); err != nil {
		return err
	}
	e.ID = newEvidenceID()
	unknownHealth := t.HealthRequired == nil
	applySample(t, v, e.ID, v.CollectedAt, time.Duration(d.Report.Plan.DurationSeconds)*time.Second, 30*time.Second)
	if unknownHealth && t.HealthRequired != nil && d.Report.ParentGuardID != "" {
		var previous []TargetObservation
		if parent, err := s.load(ctx, d.Report.ProjectID, d.Report.ParentGuardID); err == nil {
			previous = parent.Report.Targets
		}
		d.Report.Comparison = compareObservedHealth(d.Report.Comparison, previous, *t)
	}
	d.Private.Schedule[t.ID] = v.CollectedAt.Add(30 * time.Second)
	if t.ReadyAt != nil {
		slot := int(v.CollectedAt.Sub(*t.ReadyAt)/(30*time.Second)) + 1
		next := t.ReadyAt.Add(time.Duration(slot) * 30 * time.Second)
		if next.After(t.Deadline) {
			next = t.Deadline
		}
		if !next.After(v.CollectedAt) {
			t.Completed = true
		} else {
			d.Private.Schedule[t.ID] = next
		}
	}
	d.Report.ExecutionStatus = "observing"
	aggregate(&d.Report, false)
	if v.Outcome == "target_changed" {
		d.Report.Timeline = append(d.Report.Timeline, TimelineItem{ID: e.ID, At: v.CollectedAt, Kind: "target_changed", TargetID: t.ID, Summary: v.Reason, EvidenceIDs: []string{e.ID}})
	}
	buildInvestigation(&d.Report)
	if err := s.save(ctx, d, "runtime_sample", e); err != nil {
		return err
	}
	if d.Private.EvidenceBytes >= regularEvidenceBytes {
		for i := range d.Report.Targets {
			d.Report.Targets[i].Limitations = appendUnique(d.Report.Targets[i].Limitations, "evidence_capacity_reached")
		}
		s.finishLocked(ctx, d, "tool_error", "evidence_capacity_reached")
	}
	return nil
}

func buildInvestigation(r *Report) {
	if r.Investigation.Status == "completed" && r.Investigation.InputHash != investigationInput(*r) {
		r.Investigation.Status = "stale"
	}
	if r.Investigation.SourceComparisons == nil {
		r.Investigation.SourceComparisons = []SourceComparison{}
	}
	if r.Investigation.Baselines == nil {
		r.Investigation.Baselines = []TargetBaseline{}
	}
	r.Investigation.Candidates = []Candidate{}
	for _, t := range r.Targets {
		for _, c := range t.Conditions {
			if c.Result != "failed" {
				continue
			}
			counter := []string{}
			for _, v := range r.Timeline {
				if c.Name == "liveness" && v.Kind == "baseline_already_failing" && baselineTargetMatches(r.Plan, t, Evidence{TargetID: v.TargetID}) {
					counter = append(counter, v.EvidenceIDs...)
				}
			}
			r.Investigation.Candidates = append(r.Investigation.Candidates, Candidate{ID: t.ID + ":" + c.Name, Title: c.Reason, SupportingEvidenceIDs: c.EvidenceIDs, CounterEvidenceIDs: counter, MissingEvidence: []string{"causation_not_proven", "business_effect_not_observed"}, NextReadOnlyCheck: "review_scoped_evidence_and_prior_deployment"})
		}
	}
	if len(r.Investigation.Candidates) > 0 {
		r.AttributionStatus = "undetermined"
	}
}

func refreshBaselineStatus(r *Report) {
	verified := 0
	for _, t := range r.Targets {
		for _, event := range r.Timeline {
			if (event.Kind == "baseline_verified" || event.Kind == "baseline_already_failing") && baselineTargetMatches(r.Plan, t, Evidence{TargetID: event.TargetID}) {
				verified++
				break
			}
		}
	}
	switch {
	case verified == 0:
		r.Investigation.BaselineStatus = "unavailable"
	case verified == len(r.Targets):
		r.Investigation.BaselineStatus = "verified_immediate"
	default:
		r.Investigation.BaselineStatus = "partial_immediate"
	}
}
