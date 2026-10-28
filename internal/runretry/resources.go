package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

// ResourceRef is private encrypted ownership evidence, never a public DTO.
type ResourceRef struct {
	CreationUnknown                                                         bool
	RemoveVolumes                                                           bool
	RootID, AttemptID, NodeKey, ProcessOwner                                string
	Kind, Name, ID, Owner, RuntimeID, ServerID, ConnectionFingerprint, Path string
}
type ResourceVerifier func(context.Context, ResourceRef) (bool, error)

func WithResourceVerifier(v ResourceVerifier) Option {
	return func(s *Service) { s.resourceVerifier = v }
}

type ResourceRecord struct {
	ID, State string
	Ref       ResourceRef
	cipher    []byte
}

func TrackResource(ctx context.Context, r ResourceRef) (string, error) {
	e, ok := ExecutionFrom(ctx)
	if !ok {
		return "", ErrUnavailable
	}
	v, ok := e.(interface {
		TrackResource(context.Context, ResourceRef) (string, error)
	})
	if !ok {
		return "", ErrUnavailable
	}
	return v.TrackResource(ctx, r)
}
func UpdateResource(ctx context.Context, id string, r ResourceRef, state string) error {
	e, ok := ExecutionFrom(ctx)
	if !ok {
		return ErrUnavailable
	}
	v, ok := e.(interface {
		UpdateResource(context.Context, string, ResourceRef, string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return v.UpdateResource(ctx, id, r, state)
}
func (e *execution) TrackResource(ctx context.Context, r ResourceRef) (string, error) {
	n, ok := NodeFrom(ctx)
	if !ok || !e.valid(n) {
		return "", ErrInvalid
	}
	r.RootID, r.AttemptID, r.NodeKey, r.ProcessOwner = e.l.root, e.runID, n.Key, e.svc.owner
	if r.Owner == "" || r.Name == "" || r.RuntimeID == "" {
		return "", ErrInvalid
	}
	id := uuid.NewString()
	cipher, err := e.svc.seal("resource", e.l.root, id, r)
	if err != nil {
		return "", err
	}
	err = configstate.WithWriteTxContext(ctx, e.svc.db, func(tx context.Context, q configstate.DBTX) error {
		if err := e.svc.lock(tx, q, e.l); err != nil {
			return err
		}
		var budget int
		if err := q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=?", e.runID).Scan(&budget); err != nil {
			return err
		}
		if budget >= 512 {
			return ErrBlocked
		}
		var owner, status string
		if err := q.QueryRowContext(tx, "SELECT owner_epoch,status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", e.runID, n.Key).Scan(&owner, &status); err != nil {
			return err
		}
		if owner != e.svc.owner || status != "running" {
			return ErrBlocked
		}
		_, err := q.ExecContext(tx, "INSERT INTO run_retry_resources(id,root_id,attempt_id,node_key,state,ciphertext,updated_at) VALUES(?,?,?,?,'allocated',?,?)", id, e.l.root, e.runID, n.Key, cipher, time.Now().UnixMilli())
		return err
	})
	return id, err
}
func (e *execution) UpdateResource(ctx context.Context, id string, r ResourceRef, state string) error {
	if state != "active" && state != "drained" && state != "outcome_unknown" {
		return ErrInvalid
	}
	records, err := e.svc.Resources(ctx, e.runID)
	if err != nil {
		return err
	}
	var old *ResourceRecord
	for i := range records {
		if records[i].ID == id {
			old = &records[i]
		}
	}
	if old == nil || old.Ref.ProcessOwner != e.svc.owner {
		return ErrConflict
	}
	r.RootID, r.AttemptID, r.NodeKey, r.ProcessOwner = old.Ref.RootID, old.Ref.AttemptID, old.Ref.NodeKey, old.Ref.ProcessOwner
	if old.State == "drained" {
		if state == "drained" && digest(old.Ref) == digest(r) {
			return nil
		}
		return ErrConflict
	}
	if old.Ref.Kind != r.Kind || old.Ref.Name != r.Name || old.Ref.Owner != r.Owner || old.Ref.RuntimeID != r.RuntimeID || old.Ref.ServerID != r.ServerID || old.Ref.Path != r.Path || old.Ref.ConnectionFingerprint != r.ConnectionFingerprint || old.Ref.ID != "" && old.Ref.ID != r.ID {
		return ErrConflict
	}
	if old.Ref.CreationUnknown && !r.CreationUnknown || old.Ref.RemoveVolumes != r.RemoveVolumes {
		return ErrConflict
	}
	cipher, err := e.svc.seal("resource", e.l.root, id, r)
	if err != nil {
		return err
	}
	_, err = e.svc.q(ctx).ExecContext(ctx, "UPDATE run_retry_resources SET state=?,ciphertext=?,updated_at=? WHERE id=? AND attempt_id=? AND state<>'drained'", state, cipher, time.Now().UnixMilli(), id, e.runID)
	return err
}
func (s *Service) Resources(ctx context.Context, id string) ([]ResourceRecord, error) {
	rows, err := s.q(ctx).QueryContext(ctx, "SELECT id,root_id,node_key,state,ciphertext FROM run_retry_resources WHERE attempt_id=? ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceRecord
	for rows.Next() {
		var r ResourceRecord
		var root, node string
		var b []byte
		if err = rows.Scan(&r.ID, &root, &node, &r.State, &b); err != nil {
			return nil, err
		}
		if err = s.open(b, "resource", root, r.ID, &r.Ref); err != nil {
			return nil, err
		}
		if r.Ref.RootID != root || r.Ref.AttemptID != id || r.Ref.NodeKey != node {
			return nil, ErrConflict
		}
		r.cipher = append([]byte(nil), b...)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkReaderExit accepts only the runtime's real process-local final-reader proof.
// Persisting it permits later read-only inspection; a restart alone cannot do so.
func (s *Service) MarkReaderExit(ctx context.Context, runID, activityID, owner string) error {
	known, err := s.HasManifest(ctx, runID)
	if err != nil || !known {
		return err
	}
	if !configstate.ActivityReadersExited(activityID, owner) {
		return ErrBlocked
	}
	q := s.q(ctx)
	var actual string
	if err = q.QueryRowContext(ctx, "SELECT owner_epoch FROM project_activity WHERE activity_id=? AND run_id=?", activityID, runID).Scan(&actual); err != nil {
		return err
	}
	if actual != owner {
		return ErrConflict
	}
	var previous string
	err = q.QueryRowContext(ctx, "SELECT owner_epoch FROM run_retry_reader_exits WHERE activity_id=?", activityID).Scan(&previous)
	if err == nil {
		if previous != owner {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO run_retry_reader_exits(activity_id,attempt_id,owner_epoch,runtime_owner,exited_at) VALUES(?,?,?,?,?)", activityID, runID, owner, s.owner, time.Now().UnixMilli())
	return err
}

type ReconcileResult struct {
	Drained bool     `json:"drained"`
	Checked int      `json:"checked"`
	Reasons []Reason `json:"reasons"`
}

// Reconcile observes existing resources only. It never creates, kills or deletes.
func (s *Service) Reconcile(ctx context.Context, id string) (*ReconcileResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out := &ReconcileResult{Reasons: []Reason{}}
	l, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	var active, unproven int
	err = s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN e.activity_id IS NULL OR e.owner_epoch<>a.owner_epoch THEN 1 ELSE 0 END),0) FROM project_activity a LEFT JOIN run_retry_reader_exits e ON e.activity_id=a.activity_id AND e.attempt_id=a.run_id WHERE a.run_id=? AND a.released_at IS NULL`, id).Scan(&active, &unproven)
	if err != nil {
		return nil, err
	}
	if unproven > 0 {
		out.Reasons = append(out.Reasons, Reason{Code: "readers_not_exited"})
		return out, nil
	}
	var status string
	if err = s.q(ctx).QueryRowContext(ctx, "SELECT status FROM pipeline_runs WHERE id=?", id).Scan(&status); err != nil || !terminal(status) {
		out.Reasons = append(out.Reasons, Reason{Code: "attempt_not_drained"})
		return out, err
	}
	resources, err := s.Resources(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = s.reconcileTargets(ctx, id, l, out); err != nil {
		return nil, err
	}
	for _, resource := range resources {
		var proof int
		if err := s.q(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM run_retry_reader_exits WHERE attempt_id=? AND runtime_owner=?", id, resource.Ref.ProcessOwner).Scan(&proof); err != nil {
			return nil, err
		}
		if proof == 0 {
			out.Reasons = append(out.Reasons, Reason{Code: "readers_not_exited"})
			return out, nil
		}
	}
	verified := map[string]bool{}
	for _, r := range resources {
		if r.State == "drained" {
			verified[r.ID] = true
			continue
		}
		if r.Ref.CreationUnknown {
			out.Reasons = append(out.Reasons, Reason{Code: "resource_creator_unknown", NodeKey: r.Ref.NodeKey})
			continue
		}
		if s.resourceVerifier == nil {
			out.Reasons = append(out.Reasons, Reason{Code: "resource_verifier_unavailable", NodeKey: r.Ref.NodeKey})
			continue
		}
		observed, err := s.resourceVerifier(ctx, r.Ref)
		out.Checked++
		if err != nil || !observed {
			out.Reasons = append(out.Reasons, Reason{Code: "resource_not_drained", NodeKey: r.Ref.NodeKey})
			continue
		}
		verified[r.ID] = true
	}
	err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		for _, r := range resources {
			if verified[r.ID] {
				if _, err := q.ExecContext(tx, "UPDATE run_retry_resources SET state='drained',updated_at=? WHERE id=? AND ciphertext=?", time.Now().UnixMilli(), r.ID, r.cipher); err != nil {
					return err
				}
			}
		}
		// Only resource controls with actual, now-absent owned resources may be
		// reconciled. Missing ownership evidence stays blocked.
		records, err := s.records(tx, id)
		if err != nil {
			return err
		}
		for _, n := range l.graph.Nodes {
			if n.ControlKey != "cleanup" && n.ControlKey != "services" && n.ControlKey != "barrier" {
				continue
			}
			r := records[n.Key]
			if r.owner == "" || r.drained || (r.status != "running" && r.status != "outcome_unknown") {
				continue // Observations cannot rewrite completed historical outcomes.
			}
			has, all := false, true
			for _, resource := range resources {
				var stage string
				for _, rn := range l.graph.Nodes {
					if rn.Key == resource.Ref.NodeKey {
						stage = rn.ExecutionStageID
						break
					}
				}
				if stage == n.ExecutionStageID {
					has = true
					all = all && verified[resource.ID]
				}
			}
			if !has || !all {
				continue
			}
			state := "failed"
			external := "none"
			if n.ControlKey == "cleanup" {
				state = "success"
				external = "verified"
			}
			if n.ControlKey == "barrier" {
				for _, jobDone := range l.graph.Nodes {
					if jobDone.ExecutionStageID == n.ExecutionStageID && jobDone.ControlKey == "jobs_done" && records[jobDone.Key].status == "success" {
						state = "success"
					}
				}
			}
			o := Outcome{Status: state, ExecutionMode: "real", ExternalOutcome: external, ReasonCode: "resources_verified", Complete: false, Reusable: false}
			cipher, err := s.seal("checkpoint", l.root, id+":"+n.Key, o)
			if err != nil {
				return err
			}
			var used int64
			if err = q.QueryRowContext(tx, "SELECT checkpoint_bytes FROM run_retry_lineages WHERE root_id=?", l.root).Scan(&used); err != nil {
				return err
			}
			if used-int64(len(r.cipher))+int64(len(cipher)) > 31<<20 {
				cipher = nil
			}
			if _, err = q.ExecContext(tx, "UPDATE run_retry_lineages SET checkpoint_bytes=checkpoint_bytes-?+? WHERE root_id=?", len(r.cipher), len(cipher), l.root); err != nil {
				return err
			}
			if _, err = q.ExecContext(tx, "UPDATE run_retry_nodes SET status=?,ciphertext=?,complete=0,reusable=0,resources_drained=1,reason_code='resources_verified' WHERE attempt_id=? AND node_key=?", state, cipher, id, n.Key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Drained, err = s.ResourcesDrained(ctx, id)
	if err != nil || !out.Drained {
		return out, err
	}
	// Clear only the interruption state specifically caused by now-verified
	// resource controls. Dispatch-acceptance uncertainty is never guessed away.
	err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		var unknown int
		if err := q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND status IN ('running','outcome_unknown')", id).Scan(&unknown); err != nil {
			return err
		}
		if unknown > 0 {
			return nil
		}
		state := "failed"
		if status == "success" {
			state = "completed"
		}
		result, err := q.ExecContext(tx, "UPDATE run_retry_requests SET state=?,reason_code='resources_verified' WHERE attempt_id=? AND state='outcome_unknown' AND reason_code='node_outcome_unverified'", state, id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count > 0 {
			_, err = q.ExecContext(tx, "UPDATE run_retry_dispatch SET state=?,updated_at=? WHERE attempt_id=? AND state='outcome_unknown'", state, time.Now().UnixMilli(), id)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	// Exit proof is bound to every exact activity owner, including deploy hooks.
	err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		rows, err := q.QueryContext(tx, `SELECT a.activity_id,a.owner_epoch FROM project_activity a JOIN run_retry_reader_exits e ON e.activity_id=a.activity_id AND e.attempt_id=a.run_id AND e.owner_epoch=a.owner_epoch WHERE a.run_id=? AND a.released_at IS NULL`, id)
		if err != nil {
			return err
		}
		type activity struct{ id, owner string }
		var items []activity
		for rows.Next() {
			var a activity
			if err = rows.Scan(&a.id, &a.owner); err != nil {
				rows.Close()
				return err
			}
			items = append(items, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, a := range items {
			if err = configstate.ReleaseActivityTx(tx, q, a.id, a.owner); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		_ = s.Settle(ctx, id)
	}
	return out, err
}
