package releaseguard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

type Options struct {
	Model              ExplanationModel
	Masker             *mask.Masker
	CompareSource      func(context.Context, string, string, string) SourceComparison
	DB                 *sql.DB
	Ledger             *deployattempt.Store
	Runs               run.Service
	Targets            target.Service
	Observer           Observer
	Now                func() time.Time
	ResolveImageTarget func(run.Artifact, map[string]string) (string, deployattempt.ExpectedImage, error)
}

type Service struct {
	model              ExplanationModel
	masker             *mask.Masker
	analysisCancels    map[string]context.CancelFunc
	analysisProjects   map[string]string
	compareSource      func(context.Context, string, string, string) SourceComparison
	db                 *sql.DB
	ledger             *deployattempt.Store
	runs               run.Service
	targets            target.Service
	observer           Observer
	now                func() time.Time
	mu                 sync.Mutex
	workers            map[string]context.CancelFunc
	reads              map[string]*guardReads
	finishing          map[string]guardFinish
	wg                 sync.WaitGroup
	sshSem             chan struct{}
	closed             bool
	deleting           map[string]bool
	resolveImageTarget func(run.Artifact, map[string]string) (string, deployattempt.ExpectedImage, error)
}

type guardReads struct {
	project string
	cancels map[string]context.CancelFunc
}

type guardFinish struct{ status, reason string }

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Ledger == nil {
		return nil, ErrUnavailable
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Service{db: o.DB, ledger: o.Ledger, runs: o.Runs, targets: o.Targets, observer: o.Observer, now: o.Now, workers: map[string]context.CancelFunc{}, sshSem: make(chan struct{}, 2), deleting: map[string]bool{}, resolveImageTarget: o.ResolveImageTarget}
	s.reads = map[string]*guardReads{}
	s.finishing = map[string]guardFinish{}
	s.model = o.Model
	s.masker = o.Masker
	s.analysisCancels = map[string]context.CancelFunc{}
	s.analysisProjects = map[string]string{}
	s.compareSource = o.CompareSource
	if err := s.recover(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

var validKey = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func configHash(runID, artifactID string, ids []string, cfg map[string]string, extra ...DeploymentParameters) string {
	ids = append([]string{}, ids...)
	sort.Strings(ids)
	p := DeploymentParameters{}
	if len(extra) > 0 {
		p = extra[0]
	}
	if p.Strategy == "" {
		p.Strategy = "rolling"
	}
	if p.HealthCheck != nil && (p.HealthCheck.Type == "" || p.HealthCheck.Type == "none") {
		p.HealthCheck = nil
	}
	return digestJSON([]any{runID, artifactID, ids, cfg, p})
}

func (s *Service) Prepare(ctx context.Context, project string, in PrepareInput) (*Plan, error) {
	if err := s.projectExists(ctx, project); err != nil {
		return nil, err
	}
	if in.DurationSeconds == 0 {
		in.DurationSeconds = 600
	}
	if in.DurationSeconds != 300 && in.DurationSeconds != 600 && in.DurationSeconds != 900 {
		return nil, ErrInvalid
	}
	if in.Mode != "from_now" && in.Mode != "attached" {
		return nil, ErrInvalid
	}
	p := &Plan{ID: uuid.NewString(), ExpiresAt: s.now().UTC().Add(15 * time.Minute), ProjectID: project, Mode: in.Mode, AttemptID: in.AttemptID, RunID: in.RunID, ArtifactID: in.ArtifactID, DurationSeconds: in.DurationSeconds, IncludeLogs: in.IncludeLogs, ParentGuardID: in.ParentGuardID, Targets: []PlanTarget{}, Limitations: []string{"host_identity_unverified", "sampled_basic_runtime_only", "no_automatic_remediation"}}
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM project_config_versions WHERE project_id=? AND deleted_at IS NULL`, project).Scan(&p.ConfigRevision); err != nil {
		return nil, ErrUnavailable
	}
	if in.ParentGuardID != "" {
		if _, e := s.Snapshot(ctx, project, in.ParentGuardID); e != nil {
			return nil, e
		}
	}
	if in.Mode == "from_now" {
		a, err := s.ledger.Get(ctx, in.AttemptID)
		if err != nil {
			return nil, ErrNotFound
		}
		if a.Source.ProjectID != project {
			return nil, ErrNotFound
		}
		p.RunID = a.Source.RunID
		p.Environment = a.Source.Environment
		p.Limitations = append(p.Limitations, "before_start_not_observed", "no_immediate_predeployment_baseline")
		for _, t := range a.Targets {
			pt := PlanTarget{ID: t.ID, ServerID: t.ServerID, ServerName: t.ServerName, ContainerName: t.ContainerName, WorkloadKey: t.WorkloadKey, ConnectionFingerprint: t.ConnectionFingerprint, ExpectedImage: t.ExpectedImage, Availability: "identity_unavailable", Limitations: []string{}}
			for _, r := range t.Receipts {
				if r.Kind == deployattempt.Activated && deployattempt.ValidContainerID(r.ActualImage.ContainerID) {
					pt.ContainerID = r.ActualImage.ContainerID
				}
			}
			if pt.ContainerID != "" {
				pt.Availability = "available"
			} else {
				pt.Limitations = append(pt.Limitations, "activation_receipt_missing")
			}
			if deployattempt.ValidLocalExpected(t.ExpectedImage) && !localActivationRecorded(t, pt.ContainerID) {
				pt.Availability = "identity_unavailable"
				pt.Limitations = appendUnique(pt.Limitations, "activation_receipt_missing")
			}
			if pt.ConnectionFingerprint == "" {
				pt.Availability = "identity_unavailable"
				pt.Limitations = append(pt.Limitations, "deployment_connection_identity_unavailable")
			}
			if (t.ExpectedImage.DigestKind == deployattempt.DigestConfig && !deployattempt.ValidLocalExpected(t.ExpectedImage)) || t.ExpectedImage.Digest == "" {
				pt.Availability = "unsupported"
				pt.Limitations = append(pt.Limitations, "repository_digest_unavailable")
			}
			if t.Outcome == deployattempt.RolledBack || t.Outcome == deployattempt.OutcomeUnknown || t.Outcome == deployattempt.DeployFailed {
				pt.Limitations = append(pt.Limitations, "deployment_outcome_"+t.Outcome)
				pt.Availability = "identity_unavailable"
			}
			p.Targets = append(p.Targets, pt)
		}
	} else {
		if s.runs == nil || s.targets == nil || in.RunID == "" || in.ArtifactID == "" || len(in.ServerIDs) == 0 || len(in.ServerIDs) > 5 {
			return nil, ErrInvalid
		}
		rn, err := s.runs.Get(ctx, in.RunID)
		if err != nil || rn.ProjectID != project {
			return nil, ErrNotFound
		}
		if rn.Status != run.StatusSuccess {
			return nil, ErrConflict
		}
		p.Environment = rn.Trigger.ResolvedEnvironment
		arts, err := s.runs.ListArtifacts(ctx, in.RunID)
		if err != nil {
			return nil, err
		}
		var a *run.Artifact
		for i := range arts {
			if arts[i].ID == in.ArtifactID {
				a = &arts[i]
				break
			}
		}
		if a == nil {
			return nil, ErrInvalid
		}
		repo, _ := a.Metadata["digestRepository"].(string)
		dg, _ := a.Metadata["digest"].(string)
		name := in.DeployConfig["containerName"]
		if name == "" {
			name = a.Name
		}
		expected := deployattempt.ExpectedImage{Reference: repo + "@" + dg, Repository: repo, Digest: dg, DigestKind: deployattempt.DigestRepositoryUnknown}
		if s.resolveImageTarget != nil {
			var e error
			name, expected, e = s.resolveImageTarget(*a, in.DeployConfig)
			if e != nil {
				return nil, ErrInvalid
			}
		}
		localPending := pendingLocalExpected(expected)
		if localPending {
			if (a.Type != run.ArtifactArchive && a.Type != run.ArtifactDist) || in.DeployConfig["localDocker"] != "true" || in.Strategy == "blue_green" {
				return nil, ErrInvalid
			}
		} else if a.Type != run.ArtifactImage {
			return nil, ErrInvalid
		} else if expected.IdentitySource != "" || expected.Repository == "" || !configDigest.MatchString(expected.Digest) || expected.DigestKind == deployattempt.DigestConfig {
			return nil, ErrUnavailable
		}
		if !containerName.MatchString(name) {
			return nil, ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range in.ServerIDs {
			if seen[id] {
				return nil, ErrInvalid
			}
			seen[id] = true
			srv, e := s.targets.Get(ctx, id)
			if e != nil {
				return nil, ErrNotFound
			}
			limitations := []string{"runtime_identity_pending_activation"}
			if localPending {
				limitations = append(limitations, "local_image_identity_pending_build")
			}
			p.Targets = append(p.Targets, PlanTarget{ID: id, ServerID: id, ServerName: srv.Name, ContainerName: name, WorkloadKey: deployattempt.WorkloadKey(project, p.Environment, id, name), ExpectedImage: expected, Availability: "available", Limitations: limitations})
		}
		p.ConfigHash = configHash(in.RunID, in.ArtifactID, in.ServerIDs, in.DeployConfig, DeploymentParameters{in.Strategy, in.HealthCheck})
		if s.observer == nil {
			return nil, ErrUnavailable
		}
		for i := range p.Targets {
			cap, e := s.observer.Capture(ctx, p.Targets[i].ServerID)
			if e != nil {
				return nil, ErrUnavailable
			}
			p.Targets[i].ConnectionFingerprint = cap.Fingerprint()
		}
	}
	if len(p.Targets) == 0 || len(p.Targets) > 5 {
		return nil, ErrInvalid
	}
	p.Hash = digestJSON(*p)
	body, _ := json.Marshal(p)
	_, err := s.db.ExecContext(ctx, `INSERT INTO release_guard_plans(id,project_id,expires_at,plan_json) VALUES(?,?,?,?)`, p.ID, p.ProjectID, p.ExpiresAt.UnixMilli(), string(body))
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) Start(ctx context.Context, project string, in StartInput) (*Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked(ctx, project, in, false)
}

func (s *Service) startLocked(ctx context.Context, project string, in StartInput, attached bool) (*Report, error) {
	if !validKey.MatchString(in.IdempotencyKey) || in.PlanID == "" || in.PlanHash == "" {
		return nil, ErrInvalid
	}
	if s.closed || s.observer == nil || s.deleting[project] {
		return nil, ErrUnavailable
	}
	if err := s.projectExists(ctx, project); err != nil {
		return nil, err
	}
	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM release_guards WHERE project_id=? AND idempotency_key=?`, project, in.IdempotencyKey).Scan(&existing)
	if err == nil {
		d, e := s.load(ctx, project, existing)
		if e != nil {
			return nil, e
		}
		if d.Report.Plan.ID != in.PlanID || d.Report.Plan.Hash != in.PlanHash {
			return nil, ErrConflict
		}
		return s.responseReport(d.Report), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	p, err := s.plan(ctx, project, in.PlanID)
	if err != nil {
		return nil, err
	}
	if p.Hash != in.PlanHash || !s.now().Before(p.ExpiresAt) || (p.Mode == "attached") != attached {
		return nil, ErrConflict
	}
	if !s.planRevisionCurrent(ctx, *p) {
		return nil, ErrConflict
	}
	if len(s.workers) >= 2 {
		return nil, ErrCapacity
	}
	var active int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM release_guards WHERE status IN ('queued','binding','observing','stopping')`).Scan(&active)
	if err != nil {
		return nil, err
	}
	if active >= 2 {
		return nil, ErrCapacity
	}
	now := s.now().UTC()
	d := &document{Report: Report{ID: uuid.NewString(), ProjectID: project, RunID: p.RunID, AttemptID: p.AttemptID, Plan: *p, ExecutionStatus: "binding", AttributionStatus: "not_investigated", CreatedAt: now, StartedAt: &now, IdentityTrust: "host_identity_unverified", Targets: []TargetObservation{}, Timeline: []TimelineItem{}, Investigation: Investigation{BaselineStatus: "unavailable", Candidates: []Candidate{}, Limitations: []string{"temporal_relation_is_not_causation", "source_diff_not_collected"}}, EvidenceExpiresAt: now.Add(7 * 24 * time.Hour), ParentGuardID: p.ParentGuardID, Comparison: Comparison{SameConditions: true, Changes: []string{}}, EngineVersion: EngineVersion}, Private: privateState{Bindings: map[string]target.ConnectionSnapshot{}, LastLogs: map[string]time.Time{}, LogBytes: map[string]int{}, Schedule: map[string]time.Time{}}}
	for _, pt := range p.Targets {
		to := TargetObservation{PlanTarget: pt, Conditions: []Condition{}, Deadline: now.Add(120 * time.Second)}
		if attached {
			condition(&to, "deployment_completion", "pending", "deployment_completion_pending", "")
		}
		if pt.Availability != "available" {
			to.Terminated = true
		}
		// Capture performs only local reads. It cannot open a connection.
		cap, e := s.observer.Capture(ctx, pt.ServerID)
		if e != nil {
			to.Terminated = true
			to.Limitations = appendUnique(to.Limitations, "connection_snapshot_unavailable")
		} else if pt.ConnectionFingerprint == "" || cap.Fingerprint() != pt.ConnectionFingerprint {
			to.Terminated = true
			to.Limitations = appendUnique(to.Limitations, "target_configuration_changed")
		} else {
			d.Private.Bindings[pt.ID] = cap
		}
		d.Private.Schedule[pt.ID] = now
		d.Private.LastLogs[pt.ID] = now
		d.Report.Targets = append(d.Report.Targets, to)
	}
	if p.ParentGuardID != "" {
		if parent, e := s.load(ctx, project, p.ParentGuardID); e == nil {
			d.Report.Comparison = comparePlans(parent.Report.Plan, *p)
		}
	}
	body, _ := json.Marshal(d)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE release_guard_plans SET consumed_by=? WHERE id=? AND project_id=? AND consumed_by='' AND expires_at>?`, d.Report.ID, p.ID, project, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO release_guards(id,project_id,run_id,attempt_id,plan_id,idempotency_key,status,created_at,evidence_expires_at,report_expires_at,record_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, d.Report.ID, project, p.RunID, p.AttemptID, p.ID, in.IdempotencyKey, d.Report.ExecutionStatus, now.UnixMilli(), d.Report.EvidenceExpiresAt.UnixMilli(), now.Add(30*24*time.Hour).UnixMilli(), string(body))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if !attached {
		s.launchLocked(d.Report.ID, project)
	}
	return s.responseReport(d.Report), nil
}

func comparePlans(a, b Plan) Comparison {
	c := Comparison{SameConditions: true, Changes: []string{}}
	if a.DurationSeconds != b.DurationSeconds {
		c.Changes = append(c.Changes, "duration_changed")
	}
	if a.IncludeLogs != b.IncludeLogs {
		c.Changes = append(c.Changes, "log_scope_changed")
	}
	keys := func(p Plan) []string {
		out := []string{}
		for _, t := range p.Targets {
			out = append(out, t.WorkloadKey)
		}
		sort.Strings(out)
		return out
	}
	if digestJSON(keys(a)) != digestJSON(keys(b)) {
		c.Changes = append(c.Changes, "targets_changed")
	}
	if a.Environment != b.Environment {
		c.Changes = append(c.Changes, "environment_changed")
	}
	c.SameConditions = len(c.Changes) == 0
	return c
}

// Docker health participation is first known from a trusted runtime sample,
// rather than from the deployment plan. Compare the parent's frozen observation
// without changing either report's verification rules or historical result.
func compareObservedHealth(c Comparison, parents []TargetObservation, current TargetObservation) Comparison {
	reason := "health_condition_comparison_unavailable"
	if current.WorkloadKey != "" && current.HealthRequired != nil {
		matches := 0
		var previous *bool
		for _, parent := range parents {
			if parent.WorkloadKey == current.WorkloadKey {
				matches++
				previous = parent.HealthRequired
			}
		}
		if matches == 1 && previous != nil {
			reason = ""
			if *previous != *current.HealthRequired {
				reason = "docker_health_condition_changed"
			}
		}
	}
	if reason != "" {
		c.Changes = appendUnique(c.Changes, reason)
		c.SameConditions = false
	}
	return c
}

func (s *Service) Stop(ctx context.Context, project, id string) (*Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if terminal(d.Report.ExecutionStatus) {
		return s.responseReport(d.Report), nil
	}
	// Cancellation is immediate even if persisting the stop acknowledgement
	// fails. A storage error must not leave an authorized observation dispatching.
	if reads := s.reads[id]; reads != nil {
		for _, cancel := range reads.cancels {
			cancel()
		}
	}
	if cancel := s.workers[id]; cancel != nil {
		cancel()
	}
	d.Report.ExecutionStatus = "stopping"
	d.Report.Reason = "user_stopped"
	if err = s.save(ctx, d, "stop_requested", nil); err != nil {
		return nil, err
	}
	s.finishLocked(ctx, d, "stopped", "user_stopped")
	return s.responseReport(d.Report), nil
}

func (s *Service) Delete(ctx context.Context, project, id string) error {
	if _, e := s.Stop(ctx, project, id); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.workers[id]; running || s.hasReadsLocked(id) {
		return ErrConflict
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM release_guard_analyses WHERE guard_id=? AND status='sending'`, id)
	if err != nil {
		return err
	}
	pending := []string{}
	for rows.Next() {
		var aid string
		if err = rows.Scan(&aid); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, aid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, aid := range pending {
		if cancel := s.analysisCancels[aid]; cancel != nil {
			cancel()
		}
	}
	if len(pending) > 0 {
		return ErrConflict
	}
	_, e := s.db.ExecContext(ctx, `DELETE FROM release_guards WHERE id=? AND project_id=?`, id, project)
	return e
}

func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.workers {
		cancel()
	}
	for id, reads := range s.reads {
		for _, cancel := range reads.cancels {
			cancel()
		}
		if d, err := s.load(context.Background(), reads.project, id); err == nil {
			s.finishLocked(context.Background(), d, "interrupted", "service_stopped")
		}
	}
	for _, cancel := range s.analysisCancels {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) finishLocked(ctx context.Context, d *document, status, reason string) {
	if terminal(d.Report.ExecutionStatus) {
		return
	}
	if pending, ok := s.finishing[d.Report.ID]; ok {
		status, reason = pending.status, pending.reason
	}
	_, worker := s.workers[d.Report.ID]
	if worker || s.hasReadsLocked(d.Report.ID) {
		s.finishing[d.Report.ID] = guardFinish{status, reason}
		if reads := s.reads[d.Report.ID]; reads != nil {
			for _, cancel := range reads.cancels {
				cancel()
			}
		}
		if cancel := s.workers[d.Report.ID]; cancel != nil {
			cancel()
		}
		d.Report.ExecutionStatus = "stopping"
		d.Report.Reason = reason
		_ = s.save(ctx, d, "waiting_for_observation_exit", nil)
		return
	}
	delete(s.finishing, d.Report.ID)
	d.Report.ExecutionStatus = status
	d.Report.Reason = reason
	now := s.now().UTC()
	d.Report.FinishedAt = &now
	aggregate(&d.Report, true)
	d.Private.Bindings = map[string]target.ConnectionSnapshot{}
	buildInvestigation(&d.Report)
	_ = s.save(ctx, d, "finished", nil)
}

// A deployment keeps its own authorization and context. Only this child read is
// cancelled when the guard stops; every concurrent host is registered until the
// observer has actually returned, including time spent waiting for the semaphore.
func (s *Service) beginReadLocked(ctx context.Context, project, id string) (context.Context, func()) {
	readCtx, cancel := context.WithCancel(ctx)
	reads := s.reads[id]
	if reads == nil {
		reads = &guardReads{project: project, cancels: map[string]context.CancelFunc{}}
		s.reads[id] = reads
	}
	key := newEvidenceID()
	reads.cancels[key] = cancel
	s.wg.Add(1)
	return readCtx, func() {
		cancel()
		s.mu.Lock()
		delete(reads.cancels, key)
		if len(reads.cancels) == 0 {
			delete(s.reads, id)
		}
		s.finishPendingLocked(project, id)
		s.mu.Unlock()
		s.wg.Done()
	}
}

func (s *Service) hasReadsLocked(id string) bool {
	return s.reads[id] != nil && len(s.reads[id].cancels) > 0
}

func (s *Service) finishPendingLocked(project, id string) {
	if _, worker := s.workers[id]; worker || s.hasReadsLocked(id) {
		return
	}
	if pending, ok := s.finishing[id]; ok {
		if d, err := s.load(context.Background(), project, id); err == nil {
			s.finishLocked(context.Background(), d, pending.status, pending.reason)
		}
	}
}

// Response expiry is independent of the hourly physical purge. Copy the target
// slice because projection must not mutate the document that preserves history.
func (s *Service) responseReport(r Report) *Report {
	if r.EvidenceExpired || !s.now().Before(r.EvidenceExpiresAt) {
		r.Targets = append([]TargetObservation{}, r.Targets...)
		expireReport(&r)
	}
	return &r
}

func trimID(s string) bool { return len(s) > 0 && len(s) <= 64 && !strings.ContainsAny(s, "\x00\r\n") }
