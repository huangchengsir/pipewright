package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"sort"
	"time"
)

func (s *Service) Plan(ctx context.Context, runID string, targets []string, options ...PlanOptions) (*Plan, error) {
	var checkErr error
	ctx, checkErr = s.planCheckContext(ctx, runID)
	if checkErr != nil {
		return nil, checkErr
	}
	targets, e := cleanTargets(targets)
	if e != nil {
		return nil, e
	}
	var p *Plan
	e = configstate.WithReadTxContext(ctx, s.db, func(tx context.Context, _ configstate.DBTX) error {
		var err error
		p, err = s.plan(tx, runID, targets, options...)
		return err
	})
	if e == nil && p != nil && s.preflight != nil && p.ManifestFingerprint != "" {
		l, err := s.load(ctx, runID)
		if err != nil {
			return nil, err
		}
		reasons, err := s.preflight(ctx, l.snapshot, p)
		if err != nil {
			return nil, err
		}
		if len(reasons) > 0 {
			p.CanSubmit = false
			p.BlockingReasons = append(p.BlockingReasons, reasons...)
		}
	}
	return p, e
}
func (s *Service) plan(ctx context.Context, runID string, targets []string, options ...PlanOptions) (*Plan, error) {
	l, e := s.load(ctx, runID)
	if e != nil {
		if !errors.Is(e, ErrUnavailable) {
			return nil, e
		}
		var exists int
		if e = s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM pipeline_runs WHERE id=?", runID).Scan(&exists); e != nil {
			return nil, e
		}
		if exists == 0 {
			return nil, ErrNotFound
		}
		known, err := s.HasManifest(ctx, runID)
		if err != nil {
			return nil, err
		}
		code := "manifest_missing"
		if known {
			code = "snapshot_unavailable"
		}
		return &Plan{SchemaVersion: SchemaVersion, BaseAttemptID: runID, TargetNodeIDs: targets, BlockingReasons: []Reason{{Code: code}}}, nil
	}
	return s.makePlan(ctx, l, runID, targets, options...)
}
func (s *Service) makePlan(ctx context.Context, l lineage, runID string, targets []string, options ...PlanOptions) (*Plan, error) {
	p := &Plan{SchemaVersion: SchemaVersion, LogicalRunID: l.root, BaseAttemptID: l.head, BaseRevision: l.revision, ManifestFingerprint: l.fingerprint, TargetNodeIDs: targets, CanSubmit: true, Nodes: []PlannedNode{}, BlockingReasons: []Reason{}, Warnings: []Reason{}}
	if len(options) > 1 {
		return nil, ErrInvalid
	}
	if len(options) == 1 && len(options[0].RebuildNodeIDs) > 0 {
		var err error
		p.RebuildNodeIDs, err = cleanTargets(options[0].RebuildNodeIDs)
		if err != nil {
			return nil, err
		}
	}
	block := func(code string) {
		p.CanSubmit = false
		p.BlockingReasons = append(p.BlockingReasons, Reason{Code: code})
	}
	var gcClaims int
	if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_effects WHERE root_id=? AND state IN ('gc_claimed','gc_failed')", l.root).Scan(&gcClaims); err != nil {
		return nil, err
	}
	if gcClaims > 0 {
		block("image_cleanup_in_progress")
	}
	var rowsUsed int
	if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id IN (SELECT run_id FROM run_retry_attempts WHERE root_id=?)", l.root).Scan(&rowsUsed); err != nil {
		return nil, err
	}
	if l.revision >= 256 || rowsUsed+len(l.graph.Nodes) > 65536 {
		block("history_budget_exceeded")
	}
	if runID != l.head {
		block("attempt_superseded")
		return p, nil // Do not validate a former selection against a newer head.
	}
	if l.sourceConflict {
		block("source_conflict")
	}
	var unsafeTargets int
	if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_external_targets WHERE root_id=? AND state IN ('running','outcome_unknown')", l.root).Scan(&unsafeTargets); err != nil {
		return nil, err
	}
	if unsafeTargets > 0 {
		block("target_outcome_unknown")
	}
	var unsafeResources int
	if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_resources WHERE root_id=? AND state<>'drained'", l.root).Scan(&unsafeResources); err != nil {
		return nil, err
	}
	if unsafeResources > 0 {
		block("resource_not_drained")
	}
	if e := s.quiescent(ctx, s.q(ctx), l.head); e != nil {
		if errors.Is(e, ErrBlocked) {
			block("attempt_not_drained")
		} else {
			return nil, e
		}
	}
	if l.active != "" {
		if e := s.quiescent(ctx, s.q(ctx), l.active); e != nil {
			block("active_recovery")
		}
	}
	records, e := s.records(ctx, l.head)
	if e != nil {
		return nil, e
	}
	by := map[string]NodeRef{}
	for _, n := range l.graph.Nodes {
		by[n.Key] = n
	}
	forward, backward := map[string][]string{}, map[string][]string{}
	incomingControl := map[string][]Edge{}
	for _, edge := range l.graph.Edges {
		forward[edge.From] = append(forward[edge.From], edge.To)
		backward[edge.To] = append(backward[edge.To], edge.From)
		if edge.Kind == "control" {
			incomingControl[edge.To] = append(incomingControl[edge.To], edge)
		}
	}
	serviceStages := map[string]bool{}
	for _, st := range l.graph.Stages {
		serviceStages[st.ID] = len(st.Services) > 0
	}
	// A workspace is deliberately not snapshotted. Show the precise successful
	// producers that must be explicitly rebuilt before missing state is consumed.
	ancestors := map[string]bool{}
	work := append([]string(nil), targets...)
	for len(work) > 0 {
		k := work[0]
		work = work[1:]
		if ancestors[k] {
			continue
		}
		ancestors[k] = true
		work = append(work, backward[k]...)
	}
	candidates := map[string]bool{}
	rebuildReason := map[string]string{}
	for _, n := range l.graph.Nodes {
		r := records[n.Key]
		if n.JobID == "" || r.status != "success" || !ancestors[n.Key] {
			continue
		}
		o, err := s.outcome(ctx, l, n, r)
		code := ""
		if err == nil && o.Complete && !o.Reusable {
			code = "checkpoint_nonreusable"
		} else if err == nil && o.ValidUntil != nil && !o.ValidUntil.After(time.Now()) {
			code = "checkpoint_expired"
		} else if err != nil || !o.Complete || !o.Reusable {
			code = "checkpoint_missing"
		} else if o.WorkspaceIncomplete {
			code = "workspace_rebuild_required"
		} else if s.verifyFiles(ctx, o.Outputs.Files) != nil {
			code = "checkpoint_input_missing"
		}
		for _, image := range o.Outputs.Images {
			if s.imageVerifier == nil {
				p.Warnings = append(p.Warnings, Reason{Code: "image_runtime_unchecked", NodeKey: n.Key})
			} else if !imageChecked(ctx, image) {
				code = "image_rebuild_required"
			}
		}
		if serviceStages[n.ExecutionStageID] {
			code = "service_state_rebuild_required"
		}
		if code != "" {
			candidates[n.Key] = true
			rebuildReason[n.Key] = code
			p.RebuildGroups = append(p.RebuildGroups, RebuildGroup{NodeIDs: []string{n.Key}, ConsumerIDs: append([]string(nil), targets...), ReasonCode: code})
		}
	}
	for _, key := range p.RebuildNodeIDs {
		if !candidates[key] {
			return nil, ErrInvalid
		}
		p.Warnings = append(p.Warnings, Reason{Code: "predecessor_reexecute", NodeKey: key})
	}
	affected := map[string]bool{}
	queue := append(append([]string(nil), targets...), p.RebuildNodeIDs...)
	for _, k := range targets {
		n, ok := by[k]
		if !ok {
			return nil, ErrInvalid
		}
		r := records[k]
		if r.status == "outcome_unknown" {
			block("outcome_unknown")
		} else if r.status != "failed" {
			return nil, ErrInvalid
		}
		if n.ControlKey == "cleanup" && r.status != "failed" {
			block("cleanup_unknown")
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if affected[k] {
			continue
		}
		affected[k] = true
		queue = append(queue, forward[k]...)
	}
	required := map[string]bool{}
	queue = nil
	for k := range affected {
		queue = append(queue, k)
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if required[k] {
			continue
		}
		required[k] = true
		queue = append(queue, backward[k]...)
	}
	stageActive := map[string]bool{}
	for k := range affected {
		stageActive[by[k].ExecutionStageID] = true
	}
	for _, n := range l.graph.Nodes {
		r := records[n.Key]
		d := Decision{Status: r.status, SourceAttemptID: r.source, SourceNodeKey: r.sourceKey}
		if n.ControlKey != "" {
			if r.status == "outcome_unknown" || r.status == "running" {
				d.Action = "blocked"
				d.Reasons = []Reason{{Code: "outcome_unknown", NodeKey: n.Key}}
				block("outcome_unknown")
			} else if stageActive[n.ExecutionStageID] {
				d.Action = "control"
			} else {
				d.Action = "reuse"
			}
		} else if affected[n.Key] {
			d.Action = "execute"
			previousSuccess := r.status == "success"
			if !previousSuccess && isDeployment(n) && runID != l.root {
				o, outcomeErr := s.outcome(ctx, l, n, r)
				if knownNoDeploymentAction(n, r, o, outcomeErr) {
					if err := s.noOwnedNodeEffects(ctx, runID, n.Key); err != nil {
						return nil, err
					}
					prior, _, err := (&execution{svc: s, runID: runID, l: l}).previousBusinessOutcome(ctx, n)
					previousSuccess = err == nil && prior.Status == "success"
				}
			}
			if previousSuccess && (n.Type == "deploy_ssh" || n.Type == "deploy_frontend" || n.Type == "notify" || n.Type == "push_image") {
				p.Warnings = append(p.Warnings, Reason{Code: "side_effect_reexecute", NodeKey: n.Key})
			}
		} else if r.status == "success" {
			d.Action = "reuse"
			o, err := s.outcome(ctx, l, n, r)
			code := ""
			if rebuildReason[n.Key] != "" {
				code = rebuildReason[n.Key]
			} else if err != nil || !o.Complete || !o.Reusable {
				code = "checkpoint_missing"
			} else if o.ExecutionMode == "stub" {
				code = "execution_not_real"
			} else if o.ExternalOutcome == "unknown" {
				code = "outcome_unknown"
			} else if o.ValidUntil != nil && !o.ValidUntil.After(time.Now()) {
				code = "checkpoint_expired"
			} else if l.snapshot.CommitSHA == "" && n.Type != "git_source" {
				code = "source_unbound"
			} else if err = s.validateInputs(o.Outputs); err != nil {
				code = "checkpoint_invalid"
			}
			if code != "" {
				d.Action = "blocked"
				d.Reasons = []Reason{{Code: code, NodeKey: n.Key}}
				if required[n.Key] {
					p.CanSubmit = false
					p.BlockingReasons = append(p.BlockingReasons, d.Reasons...)
				}
			}
		} else if r.status == "skipped" {
			d.Action = "skip"
		} else {
			d.Action = "blocked"
			code := "dependency_failed"
			if r.status == "outcome_unknown" {
				code = "outcome_unknown"
			}
			d.Reasons = []Reason{{Code: code, NodeKey: n.Key}}
			if code == "outcome_unknown" && required[n.Key] {
				block(code)
			}
		}
		pn := PlannedNode{ReasonCode: r.reason, ExecutionStarted: r.owner != "", Node: n, Decision: d, StepID: r.stepID, Ordinal: r.ordinal, SourceOrdinal: -1}
		if r.source != "" {
			sourceID, ordinal, sourceErr := s.sourceStep(ctx, l, n, r)
			if sourceErr == nil {
				pn.SourceStepID = sourceID
				pn.SourceOrdinal = ordinal
			} else if errors.Is(sourceErr, ErrUnavailable) {
				pn.Decision.SourceAttemptID, pn.Decision.SourceNodeKey = "", ""
				pn.Decision.Action = "blocked"
				pn.Decision.Reasons = []Reason{{Code: "checkpoint_missing", NodeKey: n.Key}}
				if required[n.Key] {
					block("checkpoint_missing")
				}
			} else {
				return nil, sourceErr
			}
		}
		p.Nodes = append(p.Nodes, pn)
	}
	// Unselected known failed dependencies block their descendants, but do not
	// prevent an independent selected branch from being repaired this attempt.
	decisions := map[string]*Decision{}
	for i := range p.Nodes {
		decisions[p.Nodes[i].Node.Key] = &p.Nodes[i].Decision
	}
	for _, n := range p.Nodes {
		d := decisions[n.Node.Key]
		if d.Action != "execute" && d.Action != "control" {
			continue
		}
		// Lifecycle callbacks must still run after a failed sibling; their outcome
		// is recorded independently of whether the business join can proceed.
		if n.Node.ControlKey != "" && n.Node.ControlKey != "init" && n.Node.ControlKey != "gate" && n.Node.ControlKey != "services" {
			continue
		}
		for _, edge := range incomingControl[n.Node.Key] {
			up := decisions[edge.From]
			if up.Action == "blocked" || up.Action == "skip" {
				if by[edge.From].ControlKey == "barrier" && by[edge.From].AllowFailure && up.Status == "failed" {
					continue
				}
				d.Action = "blocked"
				d.Reasons = append(d.Reasons, Reason{Code: "dependency_failed", NodeKey: n.Node.Key, Related: []string{edge.From}})
			}
		}
	}
	for _, target := range targets {
		if d := decisions[target]; d.Action == "blocked" {
			p.CanSubmit = false
			p.BlockingReasons = append(p.BlockingReasons, d.Reasons...)
		}
	}
	// This digest contains only opaque identifiers, safe decisions and public SHA.
	p.PlanFingerprint = digest(struct {
		Root, Head, Manifest, SHA string
		Revision                  int64
		Targets, Rebuild          []string
		Nodes                     []PlannedNode
	}{l.root, l.head, l.fingerprint, l.snapshot.CommitSHA, l.revision, targets, p.RebuildNodeIDs, p.Nodes})
	return p, nil
}
func (s *Service) Context(ctx context.Context, runID string) (*Context, error) {
	var out *Context
	e := configstate.WithReadTxContext(ctx, s.db, func(tx context.Context, _ configstate.DBTX) error {
		l, e := s.load(tx, runID)
		if e != nil {
			return e
		}
		rs, e := s.records(tx, runID)
		if e != nil {
			return e
		}
		out = &Context{LogicalRunID: l.root, AttemptRunID: runID, HeadAttemptID: l.head, Revision: l.revision, ManifestFingerprint: l.fingerprint, CommitSHA: l.snapshot.CommitSHA, Graph: l.graph, Nodes: []PlannedNode{}}
		var unknownDispatch int
		if err := s.q(tx).QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_dispatch WHERE attempt_id=? AND state='outcome_unknown'", runID).Scan(&unknownDispatch); err != nil {
			return err
		}
		var external int
		if err := s.q(tx).QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown')", runID).Scan(&external); err != nil {
			return err
		}
		var resourcePending int
		if err := s.q(tx).QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=? AND state<>'drained'", runID).Scan(&resourcePending); err != nil {
			return err
		}
		out.OutcomeUnknown = unknownDispatch > 0 || external > 0 || resourcePending > 0
		for _, n := range l.graph.Nodes {
			r := rs[n.Key]
			if r.status == "outcome_unknown" {
				out.OutcomeUnknown = true
			}
			d := Decision{Action: r.origin, Status: r.status, SourceAttemptID: r.source, SourceNodeKey: r.sourceKey}
			pn := PlannedNode{ReasonCode: r.reason, ExecutionStarted: r.owner != "", Node: n, Decision: d, StepID: r.stepID, Ordinal: r.ordinal, SourceOrdinal: -1}
			if r.source != "" {
				id, ordinal, err := s.sourceStep(tx, l, n, r)
				if errors.Is(err, ErrUnavailable) {
					pn.Decision.SourceAttemptID, pn.Decision.SourceNodeKey = "", ""
					pn.Decision.Action = "blocked"
					pn.Decision.Reasons = []Reason{{Code: "checkpoint_missing", NodeKey: n.Key}}
				} else if err != nil {
					return err
				} else {
					pn.SourceStepID, pn.SourceOrdinal = id, ordinal
				}
			}
			out.Nodes = append(out.Nodes, pn)
		}
		return nil
	})
	return out, e
}
func (s *Service) Attempts(ctx context.Context, runID string, page, size int) (*AttemptPage, error) {
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	var root string
	if e := s.q(ctx).QueryRowContext(ctx, "SELECT root_id FROM run_retry_attempts WHERE run_id=?", runID).Scan(&root); errors.Is(e, sql.ErrNoRows) {
		return nil, ErrUnavailable
	} else if e != nil {
		return nil, e
	}
	out := &AttemptPage{Page: page, Items: []Attempt{}}
	if e := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_attempts WHERE root_id=?", root).Scan(&out.Total); e != nil {
		return nil, e
	}
	rows, e := s.q(ctx).QueryContext(ctx, `SELECT a.run_id,a.parent_id,a.revision,a.execution_kind,COALESCE(r.status,a.status),a.created_at,a.initiator,(EXISTS(SELECT 1 FROM run_retry_dispatch d WHERE d.attempt_id=a.run_id AND d.state='outcome_unknown') OR EXISTS(SELECT 1 FROM run_retry_nodes n WHERE n.attempt_id=a.run_id AND n.status='outcome_unknown') OR EXISTS(SELECT 1 FROM run_retry_external_targets t WHERE t.attempt_id=a.run_id AND t.state IN ('running','outcome_unknown')) OR EXISTS(SELECT 1 FROM run_retry_resources x WHERE x.attempt_id=a.run_id AND x.state<>'drained')) FROM run_retry_attempts a LEFT JOIN pipeline_runs r ON r.id=a.run_id WHERE a.root_id=? ORDER BY a.revision DESC LIMIT ? OFFSET ?`, root, size, (page-1)*size)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var a Attempt
		var ms int64
		if e = rows.Scan(&a.RunID, &a.ParentAttemptID, &a.Revision, &a.ExecutionKind, &a.Status, &ms, &a.Initiator, &a.OutcomeUnknown); e != nil {
			return nil, e
		}
		a.CreatedAt = time.UnixMilli(ms).UTC()
		out.Items = append(out.Items, a)
	}
	return out, rows.Err()
}
func canonicalEdges(edges []Edge, to, kind string) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.To == to && e.Kind == kind {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}
