package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
)

type KubernetesVerifier func(context.Context, string) (kubernetes.Operation, error)

func WithKubernetesVerifier(f KubernetesVerifier) Option {
	return func(s *Service) { s.kubernetesVerifier = f }
}

type kubernetesChecksKey struct{}
type kubernetesCheck struct {
	Operation kubernetes.Operation
	Verified  bool
}

func (s *Service) kubernetesCheckContext(ctx context.Context, runID string) (context.Context, error) {
	if _, ok := ctx.Value(kubernetesChecksKey{}).(map[string]kubernetesCheck); ok {
		return ctx, nil
	}
	checks := map[string]kubernetesCheck{}
	rows, e := s.q(ctx).QueryContext(ctx, `SELECT id FROM k8s_operations WHERE retired=0 AND run_id IN (SELECT run_id FROM run_retry_attempts WHERE root_id=(SELECT root_id FROM run_retry_attempts WHERE run_id=?)) ORDER BY created_at,id LIMIT 500`, runID)
	if e != nil {
		return ctx, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return ctx, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return ctx, e
	}
	for _, id := range ids {
		entry := kubernetesCheck{}
		if s.kubernetesVerifier != nil {
			entry.Operation, e = s.kubernetesVerifier(ctx, id)
			entry.Verified = e == nil
		}
		checks[id] = entry
	}
	return context.WithValue(ctx, kubernetesChecksKey{}, checks), nil
}
func (s *Service) checkedKubernetes(ctx context.Context, operationID string) (kubernetes.Operation, bool) {
	checks, _ := ctx.Value(kubernetesChecksKey{}).(map[string]kubernetesCheck)
	v, ok := checks[operationID]
	if !ok || !v.Verified {
		return kubernetes.Operation{}, false
	}
	var revision int64
	var retired bool
	if s.q(ctx).QueryRowContext(ctx, "SELECT revision,retired FROM k8s_operations WHERE id=?", operationID).Scan(&revision, &retired) != nil || revision != v.Operation.Revision || retired {
		return kubernetes.Operation{}, false
	}
	return v.Operation, true
}
func (s *Service) checkedKubernetesForNode(ctx context.Context, attempt, node string) (kubernetes.Operation, bool) {
	var id string
	e := s.q(ctx).QueryRowContext(ctx, "SELECT id FROM k8s_operations WHERE run_id=? AND node_id=? ORDER BY attempt DESC LIMIT 1", attempt, node).Scan(&id)
	if e != nil {
		return kubernetes.Operation{}, false
	}
	return s.checkedKubernetes(ctx, id)
}
func (s *Service) kubernetesBoundary(ctx context.Context, root string) (string, error) {
	var pending int
	e := s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_operations WHERE run_id IN (SELECT run_id FROM run_retry_attempts WHERE root_id=?) AND retired=0 AND (cleanup_state<>'verified' OR owner_fenced=0 OR business_state IN ('submitted','outcome_unknown'))`, root).Scan(&pending)
	if e != nil {
		return "", e
	}
	if pending > 0 {
		return "kubernetes_operation_unsettled", nil
	}
	return "", nil
}
func (s *Service) validateKubernetesReceipt(ctx context.Context, attempt string, n NodeRef, o Outcome) error {
	if n.Type != "deploy_kubernetes" {
		if o.Kubernetes != nil {
			return ErrInvalid
		}
		return nil
	}
	if o.Kubernetes == nil {
		if o.Status == "success" {
			return ErrUnavailable
		}
		return nil
	}
	ref := o.Kubernetes
	var actualAttempt, node, hash, business, cleanup string
	var revision int64
	var fenced, retired bool
	e := s.q(ctx).QueryRowContext(ctx, `SELECT run_id,node_id,intent_hash,revision,business_state,cleanup_state,owner_fenced,retired FROM k8s_operations WHERE id=?`, ref.OperationID).Scan(&actualAttempt, &node, &hash, &revision, &business, &cleanup, &fenced, &retired)
	if e != nil || actualAttempt != ref.SourceAttemptID || node != n.Key || hash != ref.IntentHash || revision != ref.Revision || business != string(ref.Business) || cleanup != string(ref.Cleanup) || retired {
		return ErrUnavailable
	}
	if actualAttempt != attempt {
		var same int
		e = s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM run_retry_attempts a JOIN run_retry_attempts b ON a.root_id=b.root_id WHERE a.run_id=? AND b.run_id=? AND a.revision<b.revision`, actualAttempt, attempt).Scan(&same)
		if e != nil || same != 1 {
			return ErrInvalid
		}
	}
	if o.Status == "success" && (!fenced || cleanup != "verified" || (business != "ready" && business != "unchanged") || o.ExternalOutcome != "verified" || o.ExecutionMode != "real") {
		return ErrUnavailable
	}
	if (business == "submitted" || business == "outcome_unknown") && o.ExternalOutcome != "unknown" {
		return ErrUnknown
	}
	return nil
}

// PreviousKubernetes resolves only a real operation within the immutable
// lineage. It leaves the old run/checkpoint untouched; the new worker may adopt
// a fresh read-only proof or create a new attempt after a known failure.
func PreviousKubernetes(ctx context.Context, n NodeRef) (string, error) {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return "", ErrUnavailable
	}
	e, ok := ex.(*execution)
	if !ok || !e.IsRecovery() {
		return "", ErrUnavailable
	}
	var id string
	err := e.svc.q(ctx).QueryRowContext(ctx, `SELECT o.id FROM k8s_operations o JOIN run_retry_attempts a ON a.run_id=o.run_id WHERE a.root_id=? AND a.revision<(SELECT revision FROM run_retry_attempts WHERE run_id=?) AND o.node_id=? AND o.retired=0 ORDER BY a.revision DESC,o.attempt DESC LIMIT 1`, e.l.root, e.runID, n.Key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnavailable
	}
	return id, err
}

func (s *Service) kubernetesOutcomeChecked(ctx context.Context, o Outcome) bool {
	if o.Kubernetes == nil {
		return false
	}
	actual, ok := s.checkedKubernetes(ctx, o.Kubernetes.OperationID)
	return ok && actual.Successful() && actual.IntentHash == o.Kubernetes.IntentHash
}

type KubernetesHistoryEntry struct{ RunID, OperationID string }

// KubernetesHistory returns only typed operation references from sealed node
// checkpoints, including explicitly inherited or adopted nodes. No raw config.
func (s *Service) KubernetesHistory(ctx context.Context, project string) ([]KubernetesHistoryEntry, error) {
	rows, e := s.q(ctx).QueryContext(ctx, `SELECT a.run_id FROM run_retry_attempts a JOIN run_retry_lineages l ON l.root_id=a.root_id JOIN pipeline_runs r ON r.id=a.run_id WHERE l.project_id=? ORDER BY a.created_at DESC LIMIT 500`, project)
	if e != nil {
		return nil, e
	}
	var runs []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	var out []KubernetesHistoryEntry
	for _, id := range runs {
		l, e := s.load(ctx, id)
		if e != nil {
			return nil, e
		}
		records, e := s.records(ctx, id)
		if e != nil {
			return nil, e
		}
		for _, n := range l.graph.Nodes {
			if n.Type != "deploy_kubernetes" {
				continue
			}
			r := records[n.Key]
			o, e := s.outcome(ctx, l, n, r)
			if e != nil || o.Kubernetes == nil {
				continue
			}
			out = append(out, KubernetesHistoryEntry{id, o.Kubernetes.OperationID})
		}
	}
	return out, nil
}

// kubernetesReference never infers adoption from origin/source_attempt_id. The
// sealed outcome names the original operation; its identity is checked against
// the durable ledger and the requesting attempt's immutable lineage. No network
// work or private authorization material enters this read-only projection.
func (s *Service) kubernetesReference(ctx context.Context, l lineage, attempt string, n NodeRef, r nodeRecord) (*KubernetesReference, error) {
	if n.Type != "deploy_kubernetes" {
		return nil, nil
	}
	out, err := s.outcome(ctx, l, n, r)
	if err != nil || out.Kubernetes == nil {
		return nil, nil
	}
	ref := out.Kubernetes
	var source, node, hash string
	err = s.q(ctx).QueryRowContext(ctx, `SELECT o.run_id,o.node_id,o.intent_hash FROM k8s_operations o
 JOIN run_retry_attempts original ON original.run_id=o.run_id
 JOIN run_retry_attempts requesting ON requesting.run_id=? AND requesting.root_id=original.root_id
 WHERE o.id=? AND original.root_id=? AND original.revision<=requesting.revision`, attempt, ref.OperationID, l.root).Scan(&source, &node, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if source != ref.SourceAttemptID || node != n.Key || hash != ref.IntentHash {
		return nil, nil
	}
	return &KubernetesReference{OperationID: ref.OperationID, SourceAttemptID: source, NodeKey: node}, nil
}
