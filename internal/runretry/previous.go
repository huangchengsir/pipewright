package runretry

import "context"

func isDeployment(n NodeRef) bool {
	return n.ControlKey == "" && n.JobID != "" && (n.Type == "deploy_ssh" || n.Type == "deploy_frontend")
}

func validDeploymentNoDispatch(n NodeRef, o Outcome) bool {
	return isDeployment(n) && o.Status == "failed" && o.ExecutionMode == "real" && o.ExternalOutcome == "none" && !o.Complete && !o.Reusable && len(o.Targets) == 0 && o.ExternalID == "" && len(o.Outputs.Files) == 0 && len(o.Outputs.Images) == 0 && len(o.Outputs.Env) == 0
}

func knownNoDeploymentAction(n NodeRef, r nodeRecord, o Outcome, outcomeErr error) bool {
	if !isDeployment(n) || r.sourceKey != n.Key || r.source == "" {
		return false
	}
	if outcomeErr != nil {
		return r.owner == "" && len(r.cipher) == 0 && (r.status == "pending" || r.status == "skipped")
	}
	return o.DeploymentNotDispatched && validDeploymentNoDispatch(n, o) || o.Status == "skipped" && o.ExecutionMode == "real" && o.ExternalOutcome == "none" && len(o.Targets) == 0 && o.ExternalID == "" && len(o.Outputs.Files) == 0 && len(o.Outputs.Images) == 0 && len(o.Outputs.Env) == 0
}

// Even a sealed no-dispatch/skipped result must not conceal an independently
// reserved external action. Count all rows, including known-completed resources.
func (s *Service) noOwnedNodeEffects(ctx context.Context, attempt, node string) error {
	var count int
	err := s.q(ctx).QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND node_key=?)+(SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=? AND node_key=?)`, attempt, node, attempt, node).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrUnknown
	}
	return nil
}

func (s *Service) nodeRecord(ctx context.Context, attempt, key string) (nodeRecord, error) {
	var r nodeRecord
	var complete, reusable int
	err := s.q(ctx).QueryRowContext(ctx, `SELECT status,origin,source_attempt_id,source_node_key,complete,reusable,valid_until,ciphertext,step_id,step_ordinal,owner_epoch,reason_code,resources_drained FROM run_retry_nodes WHERE attempt_id=? AND node_key=?`, attempt, key).Scan(&r.status, &r.origin, &r.source, &r.sourceKey, &complete, &reusable, &r.until, &r.cipher, &r.stepID, &r.ordinal, &r.owner, &r.reason, &r.drained)
	r.complete, r.reusable = complete == 1, reusable == 1
	return r, err
}

// previousBusinessOutcome follows at most the persisted 256-attempt budget.
// A newer attempt which never dispatched this deployment does not erase the
// last actual success/partial failure, its receipts, or its producer identity.
func (e *execution) previousBusinessOutcome(ctx context.Context, n NodeRef) (Outcome, string, error) {
	q := e.svc.q(ctx)
	var currentRoot, parent string
	var revision int64
	if err := q.QueryRowContext(ctx, "SELECT root_id,parent_id,revision FROM run_retry_attempts WHERE run_id=?", e.runID).Scan(&currentRoot, &parent, &revision); err != nil {
		return Outcome{}, "", err
	}
	if currentRoot != e.l.root || parent == "" {
		return Outcome{}, "", ErrUnavailable
	}
	var ancestry []string
	positions := map[string]int{}
	for parent != "" {
		if len(ancestry) >= 256 {
			return Outcome{}, "", ErrUnavailable
		}
		if _, exists := positions[parent]; exists {
			return Outcome{}, "", ErrUnavailable
		}
		var root, next string
		var ancestorRevision int64
		if err := q.QueryRowContext(ctx, "SELECT root_id,parent_id,revision FROM run_retry_attempts WHERE run_id=?", parent).Scan(&root, &next, &ancestorRevision); err != nil {
			return Outcome{}, "", ErrUnavailable
		}
		if root != e.l.root || ancestorRevision >= revision || ancestorRevision < 0 || (next == "" && parent != root) {
			return Outcome{}, "", ErrUnavailable
		}
		positions[parent] = len(ancestry)
		ancestry = append(ancestry, parent)
		parent, revision = next, ancestorRevision
	}
	for index := 0; index < len(ancestry); index++ {
		attempt := ancestry[index]
		r, err := e.svc.nodeRecord(ctx, attempt, n.Key)
		if err != nil || r.sourceKey != n.Key || r.source == "" {
			return Outcome{}, "", ErrUnavailable
		}
		if r.status == "running" || r.status == "outcome_unknown" {
			return Outcome{}, "", ErrUnknown
		}
		sourceIndex, exists := positions[r.source]
		if !exists || sourceIndex < index {
			return Outcome{}, "", ErrUnavailable
		}
		if r.source != attempt {
			if r.owner != "" || len(r.cipher) != 0 || (r.origin != "reuse" && r.origin != "skip" && r.origin != "blocked") {
				return Outcome{}, "", ErrUnavailable
			}
			if err = e.svc.noOwnedNodeEffects(ctx, attempt, n.Key); err != nil {
				return Outcome{}, "", err
			}
			index = sourceIndex
			attempt = ancestry[index]
			r, err = e.svc.nodeRecord(ctx, attempt, n.Key)
			if err != nil || r.source != attempt || r.sourceKey != n.Key {
				return Outcome{}, "", ErrUnavailable
			}
		}
		o, err := e.svc.outcome(ctx, e.l, n, r)
		if knownNoDeploymentAction(n, r, o, err) {
			if err = e.svc.noOwnedNodeEffects(ctx, attempt, n.Key); err != nil {
				return Outcome{}, "", err
			}
			if index == len(ancestry)-1 {
				return Outcome{Status: "not_started", ReasonCode: "not_started", ExternalOutcome: "none"}, attempt, nil
			}
			continue
		}
		if err != nil {
			return Outcome{}, "", err
		}
		if o.DeploymentNotDispatched || o.ExternalOutcome == "unknown" || o.Status == "outcome_unknown" || o.Status == "running" {
			return Outcome{}, "", ErrUnknown
		}
		// Ordinary failure without receipts is not a no-dispatch proof. In
		// particular, older captures must not erase an earlier successful target
		// simply because a later input failure had no private provenance marker.
		if isDeployment(n) && (o.ExecutionMode != "real" || len(o.Targets) == 0 || (o.Status != "success" && o.Status != "failed")) {
			return Outcome{}, "", ErrUnavailable
		}
		return o, attempt, nil
	}
	return Outcome{}, "", ErrUnavailable
}
