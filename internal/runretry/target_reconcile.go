package runretry

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

func (s *Service) reconcileTargets(ctx context.Context, id string, l lineage, out *ReconcileResult) error {
	rows, err := s.q(ctx).QueryContext(ctx, "SELECT id,node_key FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown')", id)
	if err != nil {
		return err
	}
	type key struct{ id, node string }
	var keys []key
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.id, &k.node); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range keys {
		op, err := s.TargetOperation(ctx, id, k.node, k.id)
		if err != nil {
			return err
		}
		var exited int
		if err = s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_reader_exits WHERE attempt_id=? AND runtime_owner=?", id, op.Proof.ProcessOwner).Scan(&exited); err != nil {
			return err
		}
		if op.Proof.ProcessOwner == "" || exited == 0 {
			out.Reasons = append(out.Reasons, Reason{Code: "readers_not_exited", NodeKey: k.node})
			continue
		}
		if s.targetVerifier == nil {
			out.Reasons = append(out.Reasons, Reason{Code: "target_verifier_unavailable", NodeKey: k.node})
			continue
		}
		state, p, err := s.targetVerifier(ctx, *op)
		out.Checked++
		if err != nil || state != "success" && state != "failed" || !p.ExitKnown || state == "success" && p.ExitCode != 0 {
			out.Reasons = append(out.Reasons, Reason{Code: "target_outcome_unknown", NodeKey: k.node})
			continue
		}
		if !sameTargetBinding(op.Proof, p) {
			return ErrConflict
		}
		p.RootID, p.NodeKey, p.AttemptID, p.ServerID, p.ProcessOwner = op.Proof.RootID, op.Proof.NodeKey, op.Proof.AttemptID, op.Proof.ServerID, op.Proof.ProcessOwner
		cipher, err := s.seal("target", l.root, k.id, p)
		if err != nil {
			return err
		}
		err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
			if err := s.lock(tx, q, l); err != nil {
				return err
			}
			current, err := s.TargetOperation(tx, id, k.node, k.id)
			if err != nil {
				return err
			}
			if digest(current.Proof) != digest(op.Proof) || current.State != op.State {
				return ErrConflict
			}
			_, err = q.ExecContext(tx, "UPDATE run_retry_external_targets SET state=?,ciphertext=?,updated_at=? WHERE id=?", state, cipher, time.Now().UnixMilli(), k.id)
			return err
		})
		if err != nil {
			return err
		}
	}
	// Resolved target receipts make the previous uncertain node a known failed
	// attempt. Explicit retry may then retain those targets and continue successors.
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		records, err := s.records(tx, id)
		if err != nil {
			return err
		}
		for _, n := range l.graph.Nodes {
			r := records[n.Key]
			if n.JobID == "" || r.status != "outcome_unknown" {
				continue
			}
			o, err := s.outcome(tx, l, n, r)
			if err != nil || len(o.Targets) == 0 {
				continue
			}
			all := true
			for i, receipt := range o.Targets {
				op, err := s.TargetOperation(tx, id, n.Key, receipt.OperationID)
				if err != nil || op.ServerID != receipt.ServerID || op.State != "success" && op.State != "failed" {
					all = false
					break
				}
				o.Targets[i].Status = op.State
				o.Targets[i].SourceAttemptID = op.AttemptID
			}
			if !all {
				continue
			}
			o.Status = "failed"
			o.ExternalOutcome = "verified"
			o.Complete = false
			o.Reusable = false
			o.ReasonCode = "target_result_verified"
			cipher, err := s.seal("checkpoint", l.root, id+":"+n.Key, o)
			if err != nil {
				return err
			}
			var used int64
			if err = q.QueryRowContext(tx, "SELECT checkpoint_bytes FROM run_retry_lineages WHERE root_id=?", l.root).Scan(&used); err != nil {
				return err
			}
			if used-int64(len(r.cipher))+int64(len(cipher)) > 31<<20 {
				out.Reasons = append(out.Reasons, Reason{Code: "checkpoint_unavailable", NodeKey: n.Key})
				continue
			}
			if _, err = q.ExecContext(tx, "UPDATE run_retry_lineages SET checkpoint_bytes=? WHERE root_id=?", used-int64(len(r.cipher))+int64(len(cipher)), l.root); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, "UPDATE run_retry_nodes SET status='failed',complete=0,reusable=0,ciphertext=?,reason_code='target_result_verified' WHERE attempt_id=? AND node_key=? AND status='outcome_unknown'", cipher, id, n.Key); err != nil {
				return err
			}
		}
		return nil
	})
}
func sameTargetBinding(a, b TargetProof) bool {
	return a.RootID == b.RootID && a.NodeKey == b.NodeKey && a.AttemptID == b.AttemptID && a.ServerID == b.ServerID && a.ProcessOwner == b.ProcessOwner && a.ConfigHash == b.ConfigHash && a.ConnectionFingerprint == b.ConnectionFingerprint && a.ArtifactID == b.ArtifactID && a.ArtifactReference == b.ArtifactReference && a.Kind == b.Kind
}
