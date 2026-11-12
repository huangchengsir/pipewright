package runretry

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"strings"
	"time"
)

// SuspendNode preserves a known incomplete action, never a reusable success.
func (e *execution) SuspendNode(ctx context.Context, n NodeRef) error {
	if !e.valid(n) {
		return ErrInvalid
	}
	return configstate.WithWriteTxContext(ctx, e.svc.db, func(ctx context.Context, q configstate.DBTX) error {
		if err := e.svc.lock(ctx, q, e.l); err != nil {
			return err
		}
		result, err := q.ExecContext(ctx, `UPDATE run_retry_nodes SET status='waiting_action',complete=0,reusable=0,reason_code='waiting_explicit_action',updated_at=? WHERE attempt_id=? AND node_key=? AND owner_epoch=? AND status='running'`, time.Now().UnixMilli(), e.runID, n.Key, e.svc.owner)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Service) sameAttemptContinuation(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_continuations WHERE run_id=? AND state='running'`, id).Scan(&count)
	return count > 0, err
}
func (e *execution) Frontier(ctx context.Context) (map[string]string, error) {
	records, err := e.svc.records(ctx, e.runID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, node := range e.l.graph.Nodes {
		record, ok := records[node.Key]
		if !ok {
			return nil, ErrUnavailable
		}
		out[node.Key] = record.status
	}
	return out, nil
}

// prepareContinuationNodes resumes only the frozen release frontier. A crash
// after activation may leave the release node running, despite all private
// resources having been positively drained. Completed nodes remain immutable;
// unrelated running business work never acquires a replay permission here.
func (s *Service) prepareContinuationNodes(ctx context.Context, run string, l lineage) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		var unsafe int
		if err := q.QueryRowContext(tx, `SELECT (SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND status='outcome_unknown')+(SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=? AND state<>'drained')+(SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown'))+(SELECT COUNT(*) FROM k8s_release_actions a JOIN k8s_releases r ON r.id=a.release_id WHERE r.run_id=? AND a.state IN ('submitted','outcome_unknown'))`, run, run, run, run).Scan(&unsafe); err != nil {
			return err
		}
		if unsafe != 0 {
			return ErrUnknown
		}
		rows, err := q.QueryContext(tx, `SELECT r.node_key FROM k8s_release_continuations c JOIN k8s_releases r ON r.id=c.release_id WHERE c.run_id=? AND c.state='running' AND r.state IN ('prepared','awaiting_approval','running') AND r.cleanup_state='verified' AND r.owner_fenced=0`, run)
		if err != nil {
			return err
		}
		active := map[string]bool{}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			active[key] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return ErrConflict
		}
		stages := map[string]bool{}
		allowed := map[string]bool{}
		for _, n := range l.graph.Nodes {
			if active[n.Key] {
				if n.Type != "deploy_kubernetes" {
					return ErrUnavailable
				}
				allowed[n.Key] = true
				stages[n.ExecutionStageID] = true
			}
		}
		for _, n := range l.graph.Nodes {
			if stages[n.ExecutionStageID] {
				switch n.ControlKey {
				case "init", "gate", "cleanup", "barrier":
					allowed[n.Key] = true
				}
			}
		}
		rows, err = q.QueryContext(tx, `SELECT node_key FROM run_retry_nodes WHERE attempt_id=? AND status='running'`, run)
		if err != nil {
			return err
		}
		reset := []string{}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			if !allowed[key] {
				rows.Close()
				return ErrUnknown
			}
			reset = append(reset, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		for _, key := range reset {
			if _, err = q.ExecContext(tx, `UPDATE run_retry_nodes SET status='pending',owner_epoch='',updated_at=? WHERE attempt_id=? AND node_key=? AND status='running'`, now, run, key); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(tx, `UPDATE run_retry_nodes SET status='pending',owner_epoch='',updated_at=? WHERE attempt_id=? AND status='waiting_action'`, now, run)
		return err
	})
}

// Preserve the exact durable continuation frontier during the generic startup
// audit. The Kubernetes controller still proves owner departure/cleanup and
// activates it before Prepare can reset any preserved running node.
func (s *Service) continuationRecoveryNodes(ctx context.Context) (map[string]map[string]bool, error) {
	rows, e := s.q(ctx).QueryContext(ctx, `SELECT c.run_id,r.node_key FROM k8s_release_continuations c JOIN k8s_releases r ON r.id=c.release_id WHERE c.state IN ('pending','delivering','running') AND NOT EXISTS(SELECT 1 FROM k8s_release_actions a WHERE a.release_id=r.id AND a.state IN ('submitted','outcome_unknown')) ORDER BY c.run_id,r.node_key`+store.Limit(store.DialectOf(s.db), "501"))
	if e != nil {
		return nil, e
	}
	active := map[string]map[string]bool{}
	count := 0
	for rows.Next() {
		var run, key string
		if e = rows.Scan(&run, &key); e != nil {
			rows.Close()
			return nil, e
		}
		count++
		if count > 500 {
			rows.Close()
			return nil, ErrUnavailable
		}
		if active[run] == nil {
			active[run] = map[string]bool{}
		}
		active[run][key] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for run, keys := range active {
		l, e := s.load(ctx, run)
		if e != nil {
			return nil, e
		}
		stages := map[string]bool{}
		for _, n := range l.graph.Nodes {
			if keys[n.Key] {
				if n.Type != "deploy_kubernetes" {
					return nil, ErrUnavailable
				}
				stages[n.ExecutionStageID] = true
			}
		}
		for _, n := range l.graph.Nodes {
			if stages[n.ExecutionStageID] {
				switch n.ControlKey {
				case "init", "gate", "cleanup", "barrier":
					keys[n.Key] = true
				}
			}
		}
	}
	return active, nil
}
func (s *Service) markStartupNodes(ctx context.Context, q configstate.DBTX, frontier map[string]map[string]bool) error {
	now := time.Now().UnixMilli()
	runs := []string{}
	for run := range frontier {
		runs = append(runs, run)
	}
	query := `UPDATE run_retry_nodes SET status='outcome_unknown',updated_at=? WHERE status='running' AND owner_epoch<>?`
	args := []any{now, s.owner}
	if len(runs) > 0 {
		query += ` AND attempt_id NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(runs)), ",") + `)`
		for _, run := range runs {
			args = append(args, run)
		}
	}
	if _, e := q.ExecContext(ctx, query, args...); e != nil {
		return e
	}
	for run, keys := range frontier {
		query := `UPDATE run_retry_nodes SET status='outcome_unknown',updated_at=? WHERE status='running' AND owner_epoch<>? AND attempt_id=?`
		args := []any{now, s.owner, run}
		if len(keys) > 0 {
			query += ` AND node_key NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",") + `)`
			for key := range keys {
				args = append(args, key)
			}
		}
		if _, e := q.ExecContext(ctx, query, args...); e != nil {
			return e
		}
	}
	return nil
}
