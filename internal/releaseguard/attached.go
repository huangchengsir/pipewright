package releaseguard

import (
	"context"
	"encoding/json"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
)

// A prepared script plan binds the build/run policy, not an image guessed from
// a current container. Only the deployment's durable pre-activation target can
// replace this placeholder with a complete local image expectation.
func pendingLocalExpected(e deployattempt.ExpectedImage) bool {
	return e.IdentitySource == deployattempt.IdentityScriptPreActivation && e.DigestKind == deployattempt.DigestConfig &&
		e.Digest == "" && e.Reference == "" && e.Repository == "" && e.Platform == (deployattempt.Platform{})
}

func sameExpectedImage(a, b deployattempt.ExpectedImage) bool { return a == b }

func localActivationRecorded(t deployattempt.Target, containerID string) bool {
	for _, receipt := range t.Receipts {
		a := receipt.ActualImage
		if receipt.Kind == deployattempt.Activated && a.ContainerID == containerID && configDigest.MatchString(a.ConfigID) &&
			platformPart.MatchString(a.Platform.OS) && platformPart.MatchString(a.Platform.Architecture) &&
			(a.Platform.Variant == "" || platformPart.MatchString(a.Platform.Variant)) {
			return true
		}
	}
	return false
}

type attachedKey struct{}
type attachedHandle struct{ project, id string }

// Attached consumes consent before the existing, independently authorized
// deployment handler runs. A duplicate request is rejected BEFORE side effects;
// it must not replay a deployment merely because its guard is idempotent.
func (s *Service) Attached(ctx context.Context, project string, in StartInput, runID, artifactID string, serverIDs []string, cfg map[string]string, parameters ...DeploymentParameters) (context.Context, *Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.plan(ctx, project, in.PlanID)
	if err != nil {
		return ctx, nil, err
	}
	if p.Mode != "attached" || p.RunID != runID || p.ArtifactID != artifactID || p.ConfigHash != configHash(runID, artifactID, serverIDs, cfg, parameters...) {
		return ctx, nil, ErrConflict
	}
	var used string
	if e := s.db.QueryRowContext(ctx, `SELECT id FROM release_guards WHERE project_id=? AND idempotency_key=?`, project, in.IdempotencyKey).Scan(&used); e == nil {
		return ctx, nil, ErrConflict
	}
	r, err := s.startLocked(ctx, project, in, true)
	if err != nil {
		return ctx, nil, err
	}
	d, err := s.load(ctx, project, r.ID)
	if err != nil {
		return ctx, nil, err
	}
	for i := range d.Report.Targets {
		d.Report.Targets[i].Deadline = p.ExpiresAt
	}
	if err = s.save(ctx, d, "attached_prepared", nil); err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, attachedKey{}, attachedHandle{project, r.ID}), &d.Report, nil
}

func (s *Service) DeploymentStarted(ctx context.Context, attemptID string) {
	h, ok := ctx.Value(attachedKey{}).(attachedHandle)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(ctx, h.project, h.id)
	if err != nil || s.closed || s.deleting[h.project] || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" {
		return
	}
	a, err := s.ledger.Get(ctx, attemptID)
	if err != nil || a.Source.ProjectID != h.project || a.Source.RunID != d.Report.RunID {
		return
	}
	if d.Report.AttemptID != "" && d.Report.AttemptID != attemptID {
		return
	}
	d.Report.AttemptID = attemptID
	d.Report.Timeline = append(d.Report.Timeline, TimelineItem{ID: newEvidenceID(), At: a.CreatedAt, Kind: "deployment_started", Summary: attemptID, EvidenceIDs: []string{}})
	_ = s.save(ctx, d, "deployment_started", nil)
}

func (s *Service) BeforeImageActivation(ctx context.Context, attemptID, targetID string) {
	h, ok := ctx.Value(attachedKey{}).(attachedHandle)
	if !ok {
		return
	}
	t, err := s.ledger.GetTarget(ctx, targetID)
	if err != nil || t.AttemptID != attemptID {
		return
	}
	s.mu.Lock()
	d, err := s.load(ctx, h.project, h.id)
	if err != nil || s.closed || s.deleting[h.project] || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" || d.Report.AttemptID != attemptID {
		s.mu.Unlock()
		return
	}
	var pID string
	for i := range d.Report.Targets {
		p := &d.Report.Targets[i]
		if p.WorkloadKey == t.WorkloadKey && !p.Terminated && p.ContainerID == "" && p.ConnectionFingerprint == t.ConnectionFingerprint && t.ArtifactID == d.Report.Plan.ArtifactID {
			pID = p.ID
			if p.ExpectedImage.IdentitySource == deployattempt.IdentityScriptPreActivation && (t.Outcome != deployattempt.Started || len(t.Receipts) != 0) {
				if pendingLocalExpected(p.ExpectedImage) {
					p.Terminated = true
					p.Limitations = appendUnique(p.Limitations, "local_image_identity_invalid")
					_ = s.save(ctx, d, "local_image_identity_unavailable", nil)
				}
				s.mu.Unlock()
				return
			}
			if pendingLocalExpected(p.ExpectedImage) {
				if !deployattempt.ValidLocalExpected(t.ExpectedImage) {
					p.Terminated = true
					p.Limitations = appendUnique(p.Limitations, "local_image_identity_invalid")
					_ = s.save(ctx, d, "local_image_identity_unavailable", nil)
					s.mu.Unlock()
					return
				}
				p.ExpectedImage = t.ExpectedImage
				limitations := []string{}
				for _, limitation := range p.Limitations {
					if limitation != "local_image_identity_pending_build" {
						limitations = append(limitations, limitation)
					}
				}
				p.Limitations = limitations
				e := Evidence{ID: newEvidenceID(), TargetID: p.ID, Kind: "expected_image", EventAt: t.CreatedAt, CollectedAt: s.now().UTC(), Reason: "local_image_fixed_before_activation", Summary: "server_local_build_identity", Value: t.ExpectedImage}
				if s.save(ctx, d, "local_image_fixed_before_activation", &e) != nil {
					s.mu.Unlock()
					return
				}
			}
			break
		}
	}
	cap, found := d.Private.Bindings[pID]
	if !found || pID == "" {
		s.mu.Unlock()
		return
	}
	readCtx, finished := s.beginReadLocked(ctx, h.project, h.id)
	s.mu.Unlock()
	defer finished()
	v := s.inspect(readCtx, cap, t.ContainerName, true)
	if readCtx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err = s.load(context.Background(), h.project, h.id)
	if err != nil || s.closed || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" {
		return
	}
	e := Evidence{ID: newEvidenceID(), TargetID: pID, Kind: "baseline", EventAt: v.CollectedAt, CollectedAt: s.now().UTC(), Reason: v.Reason, Truncated: v.Truncated, Summary: "immediate_predeployment_sample", Value: v}
	kind := "baseline_unavailable"
	if v.Outcome == "ok" {
		kind = "baseline_verified"
		d.Report.Investigation.BaselineStatus = "verified_immediate"
		if !v.Running || v.OOMKilled {
			kind = "baseline_already_failing"
		}
	}
	d.Report.Timeline = append(d.Report.Timeline, TimelineItem{ID: e.ID, At: v.CollectedAt, Kind: kind, TargetID: pID, Summary: v.Reason, EvidenceIDs: []string{e.ID}})
	refreshBaselineStatus(&d.Report)
	_ = s.save(context.Background(), d, kind, &e)
}

func (s *Service) AfterImageActivation(ctx context.Context, attemptID, targetID, containerID string) {
	h, ok := ctx.Value(attachedKey{}).(attachedHandle)
	if !ok || !deployattempt.ValidContainerID(containerID) {
		return
	}
	t, err := s.ledger.GetTarget(ctx, targetID)
	if err != nil || t.AttemptID != attemptID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(context.Background(), h.project, h.id)
	if err != nil || s.closed || s.deleting[h.project] || terminal(d.Report.ExecutionStatus) || d.Report.ExecutionStatus == "stopping" || d.Report.AttemptID != attemptID {
		return
	}
	for i := range d.Report.Targets {
		p := &d.Report.Targets[i]
		if p.WorkloadKey != t.WorkloadKey || p.ContainerID != "" {
			continue
		}
		if p.ConnectionFingerprint != t.ConnectionFingerprint || t.ArtifactID != d.Report.Plan.ArtifactID || !sameExpectedImage(p.ExpectedImage, t.ExpectedImage) || pendingLocalExpected(p.ExpectedImage) {
			p.Terminated = true
			p.Limitations = appendUnique(p.Limitations, "deployment_did_not_match_plan")
			break
		}
		if deployattempt.ValidLocalExpected(t.ExpectedImage) && !localActivationRecorded(*t, containerID) {
			p.Terminated = true
			p.Limitations = appendUnique(p.Limitations, "activation_receipt_missing")
			break
		}
		old := p.ID
		p.ID = t.ID
		p.ContainerID = containerID
		p.Deadline = s.now().UTC().Add(120 * time.Second)
		if cap, ok := d.Private.Bindings[old]; ok {
			d.Private.Bindings[t.ID] = cap
			delete(d.Private.Bindings, old)
		}
		d.Private.Schedule[t.ID] = s.now().UTC()
		d.Private.LastLogs[t.ID] = s.now().UTC()
		d.Report.Timeline = append(d.Report.Timeline, TimelineItem{ID: newEvidenceID(), At: s.now().UTC(), Kind: "container_activated", TargetID: t.ID, Summary: containerID, EvidenceIDs: []string{}})
		break
	}
	if s.save(context.Background(), d, "activation_bound", nil) == nil {
		s.launchLocked(h.id, h.project)
	}
}

func (s *Service) FinishAttached(ctx context.Context, id string) {
	h, ok := ctx.Value(attachedKey{}).(attachedHandle)
	if !ok || h.id != id {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(context.Background(), h.project, id)
	if err != nil || terminal(d.Report.ExecutionStatus) {
		return
	}
	for i := range d.Report.Targets {
		if err := s.deploymentFactsLocked(context.Background(), d, &d.Report.Targets[i], true); err != nil {
			s.finishLocked(context.Background(), d, "tool_error", "deployment_evidence_unavailable")
			return
		}
		if d.Report.Targets[i].ContainerID == "" {
			d.Report.Targets[i].Terminated = true
			d.Report.Targets[i].Limitations = appendUnique(d.Report.Targets[i].Limitations, "activation_receipt_missing")
		}
	}
	if _, running := s.workers[id]; !running {
		s.finishLocked(context.Background(), d, "completed", "deployment_returned_without_observable_activation")
	} else {
		_ = s.save(context.Background(), d, "deployment_finished", nil)
	}
}

type AttemptView struct {
	deployattempt.Attempt
	Guards []ReportSummary `json:"guards"`
}
type ReportSummary struct {
	ID                 string    `json:"id"`
	ExecutionStatus    string    `json:"executionStatus"`
	VerificationResult *string   `json:"verificationResult"`
	CreatedAt          time.Time `json:"createdAt"`
}

func (s *Service) ListAttempts(ctx context.Context, runID string) ([]AttemptView, error) {
	if s.runs == nil {
		return nil, ErrUnavailable
	}
	rn, err := s.runs.Get(ctx, runID)
	if err != nil {
		return nil, ErrNotFound
	}
	if err = s.projectExists(ctx, rn.ProjectID); err != nil {
		return nil, err
	}
	list, err := s.ledger.ListByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := []AttemptView{}
	for _, a := range list {
		v := AttemptView{Attempt: a, Guards: []ReportSummary{}}
		rows, e := s.db.QueryContext(ctx, `SELECT record_json FROM release_guards WHERE attempt_id=? AND project_id=? ORDER BY created_at DESC LIMIT 50`, a.ID, rn.ProjectID)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var b string
			var d document
			if e = rows.Scan(&b); e != nil {
				rows.Close()
				return nil, e
			}
			if json.Unmarshal([]byte(b), &d) != nil {
				rows.Close()
				return nil, ErrUnavailable
			}
			v.Guards = append(v.Guards, ReportSummary{d.Report.ID, d.Report.ExecutionStatus, d.Report.VerificationResult, d.Report.CreatedAt})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

// StopProject is called before project deletion. It prevents new dispatch and
// waits for bounded in-flight reads to exit before the caller deletes data.
func (s *Service) StopProject(ctx context.Context, project string) error {
	s.mu.Lock()
	for _, reads := range s.reads {
		if reads.project == project {
			for _, cancel := range reads.cancels {
				cancel()
			}
		}
	}
	for id, p := range s.analysisProjects {
		if p == project {
			if cancel := s.analysisCancels[id]; cancel != nil {
				cancel()
			}
		}
	}
	s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM release_guards WHERE project_id=? AND status NOT IN ('completed','stopped','interrupted','blocked','tool_error','expired')`, project)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = s.Stop(ctx, project, id); err != nil {
			return err
		}
	}
	for {
		s.mu.Lock()
		running := false
		for _, id := range ids {
			if _, ok := s.workers[id]; ok || s.hasReadsLocked(id) {
				running = true
				break
			}
		}
		for _, reads := range s.reads {
			if reads.project == project {
				running = true
				break
			}
		}
		for _, p := range s.analysisProjects {
			if p == project {
				running = true
				break
			}
		}
		s.mu.Unlock()
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *Service) BeginProjectDeletion(ctx context.Context, project string) error {
	s.mu.Lock()
	s.deleting[project] = true
	s.mu.Unlock()
	if err := s.StopProject(ctx, project); err != nil {
		s.CancelProjectDeletion(project)
		return err
	}
	return nil
}

func (s *Service) CancelProjectDeletion(project string) {
	s.mu.Lock()
	delete(s.deleting, project)
	s.mu.Unlock()
}

func (s *Service) DeleteProjectRecords(ctx context.Context, project string) error {
	if err := s.StopProject(ctx, project); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM release_guards WHERE project_id=?`, project)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM release_guard_plans WHERE project_id=?`, project)
	return err
}
