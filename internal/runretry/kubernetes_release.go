package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"

	"github.com/huangchengsir/pipewright/internal/kubernetes"
)

type KubernetesReleaseReceipt struct {
	ReleaseID       string                  `json:"releaseId"`
	SourceAttemptID string                  `json:"sourceAttemptId"`
	NodeKey         string                  `json:"nodeKey"`
	PlanID          string                  `json:"planId"`
	Revision        int64                   `json:"revision"`
	State           string                  `json:"state"`
	Cleanup         kubernetes.CleanupState `json:"cleanup"`
}
type KubernetesReleaseVerifier func(context.Context, string) (kubernetes.ReleaseRecord, error)

func WithKubernetesReleaseVerifier(v KubernetesReleaseVerifier) Option {
	return func(s *Service) { s.kubernetesReleaseVerifier = v }
}

type releaseChecksKey struct{}
type checkedRelease struct {
	Record   kubernetes.ReleaseRecord
	Verified bool
}

func (s *Service) releaseCheckContext(ctx context.Context, run string) (context.Context, error) {
	if _, ok := ctx.Value(releaseChecksKey{}).(map[string]checkedRelease); ok {
		return ctx, nil
	}
	rows, e := s.q(ctx).QueryContext(ctx, `SELECT id FROM k8s_releases WHERE run_id IN(SELECT run_id FROM run_retry_attempts WHERE root_id=(SELECT root_id FROM run_retry_attempts WHERE run_id=?)) ORDER BY created_at,id`+store.Limit(store.DialectOf(s.db), "500"), run)
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
	checks := map[string]checkedRelease{}
	for _, id := range ids {
		entry := checkedRelease{}
		if s.kubernetesReleaseVerifier != nil {
			entry.Record, e = s.kubernetesReleaseVerifier(ctx, id)
			entry.Verified = e == nil
		}
		checks[id] = entry
	}
	return context.WithValue(ctx, releaseChecksKey{}, checks), nil
}
func (s *Service) checkedRelease(ctx context.Context, id string) (kubernetes.ReleaseRecord, bool) {
	checks, _ := ctx.Value(releaseChecksKey{}).(map[string]checkedRelease)
	entry, ok := checks[id]
	if !ok || !entry.Verified {
		return entry.Record, false
	}
	var revision int64
	if s.q(ctx).QueryRowContext(ctx, `SELECT revision FROM k8s_releases WHERE id=?`, id).Scan(&revision) != nil || revision != entry.Record.Revision {
		return entry.Record, false
	}
	return entry.Record, true
}
func (s *Service) validateReleaseReceipt(ctx context.Context, attempt string, n NodeRef, o Outcome) error {
	ref := o.KubernetesRelease
	if ref == nil {
		return ErrInvalid
	}
	if n.Type != "deploy_kubernetes" || o.Kubernetes != nil {
		return ErrInvalid
	}
	var run, node, plan, state, cleanup string
	var revision int64
	var fenced bool
	e := s.q(ctx).QueryRowContext(ctx, `SELECT run_id,node_key,plan_id,revision,state,cleanup_state,owner_fenced FROM k8s_releases WHERE id=?`, ref.ReleaseID).Scan(&run, &node, &plan, &revision, &state, &cleanup, &fenced)
	if e != nil || run != ref.SourceAttemptID || node != n.Key || node != ref.NodeKey || plan != ref.PlanID || revision != ref.Revision || state != ref.State || cleanup != string(ref.Cleanup) {
		return ErrUnavailable
	}
	if run != attempt {
		var same int
		e = s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM run_retry_attempts source JOIN run_retry_attempts target ON source.root_id=target.root_id WHERE source.run_id=? AND target.run_id=? AND source.revision<target.revision`, run, attempt).Scan(&same)
		if e != nil || same != 1 {
			return ErrInvalid
		}
	}
	if o.Status == "success" && (!fenced || (state != "ready" && state != "rolled_back") || cleanup != "verified" || o.ExecutionMode != "real" || o.ExternalOutcome != "verified") {
		return ErrUnavailable
	}
	if state == "outcome_unknown" && o.ExternalOutcome != "unknown" {
		return ErrUnknown
	}
	return nil
}
func (s *Service) checkedReleaseForNode(ctx context.Context, run, node string) (kubernetes.ReleaseRecord, bool) {
	var id string
	e := s.q(ctx).QueryRowContext(ctx, `SELECT id FROM k8s_releases WHERE run_id=? AND node_key=? ORDER BY created_at DESC`+store.Limit(store.DialectOf(s.db), "1"), run, node).Scan(&id)
	if e != nil {
		return kubernetes.ReleaseRecord{}, false
	}
	return s.checkedRelease(ctx, id)
}
func PreviousKubernetesRelease(ctx context.Context, n NodeRef) (string, error) {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return "", ErrUnavailable
	}
	e, ok := ex.(*execution)
	if !ok || !e.IsRecovery() {
		return "", ErrUnavailable
	}
	var id string
	err := e.svc.q(ctx).QueryRowContext(ctx, `SELECT r.id FROM k8s_releases r JOIN run_retry_attempts a ON a.run_id=r.run_id WHERE a.root_id=? AND a.revision<(SELECT revision FROM run_retry_attempts WHERE run_id=?) AND r.node_key=? ORDER BY a.revision DESC,r.created_at DESC`+store.Limit(store.DialectOf(e.svc.db), "1"), e.l.root, e.runID, n.Key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnavailable
	}
	return id, err
}
