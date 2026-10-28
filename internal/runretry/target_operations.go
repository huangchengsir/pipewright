package runretry

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// TargetProof is encrypted and is never a public API DTO. ConfigHash can cover
// private script material; no derivative of it is exposed in plan fingerprints.
type TargetProof struct {
	ProcessOwner, ReceiptKind                                                       string
	RootID, NodeKey, AttemptID, ServerID                                            string
	Kind, ConnectionFingerprint, ConfigHash                                         string
	ArtifactID, ArtifactReference                                                   string
	ReleasePath, CurrentPath, ContainerName, ContainerCreatedAt, ContainerStartedAt string
	ContainerID, ImageID, RuntimeID                                                 string
	DeploymentTargetID, DeploymentReceiptID                                         string
	NotStarted                                                                      bool
	ExitKnown                                                                       bool
	ExitCode                                                                        int
}
type TargetOperation struct {
	ID, RootID, NodeKey, AttemptID, ServerID, State string
	Proof                                           TargetProof
}

func (s *Service) BeginTarget(ctx context.Context, runID string, n NodeRef, server string, proof TargetProof) (*TargetOperation, error) {
	if !validIdentity(server) || n.JobID == "" {
		return nil, ErrInvalid
	}
	l, err := s.load(ctx, runID)
	if err != nil {
		return nil, err
	}
	valid := false
	for _, expected := range l.graph.Nodes {
		if digest(expected) == digest(n) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, ErrInvalid
	}
	op := &TargetOperation{ID: uuid.NewString(), RootID: l.root, NodeKey: n.Key, AttemptID: runID, ServerID: server, State: "running", Proof: proof}
	proof.RootID, proof.NodeKey, proof.AttemptID, proof.ServerID = l.root, n.Key, runID, server
	proof.ProcessOwner = s.owner
	op.Proof = proof
	ciphertext, err := s.seal("target", l.root, op.ID, proof)
	if err != nil {
		return nil, err
	}
	err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		var status, owner string
		if err := q.QueryRowContext(tx, "SELECT status,owner_epoch FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", runID, n.Key).Scan(&status, &owner); err != nil {
			return err
		}
		if status != "running" || owner != s.owner {
			return ErrBlocked
		}
		var previous string
		err := q.QueryRowContext(tx, "SELECT state FROM run_retry_external_targets WHERE attempt_id=? AND node_key=? AND server_id=?", runID, n.Key, server).Scan(&previous)
		if err == nil {
			return ErrUnknown
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().UnixMilli()
		_, err = q.ExecContext(tx, "INSERT INTO run_retry_external_targets(id,root_id,node_key,attempt_id,server_id,state,ciphertext,created_at,updated_at) VALUES(?,?,?,?,?,'running',?,?,?)", op.ID, l.root, n.Key, runID, server, ciphertext, now, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return op, nil
}
func (s *Service) TargetOperation(ctx context.Context, runID, nodeKey, id string) (*TargetOperation, error) {
	l, err := s.load(ctx, runID)
	if err != nil {
		return nil, err
	}
	op := &TargetOperation{ID: id}
	var cipher []byte
	err = s.q(ctx).QueryRowContext(ctx, "SELECT root_id,node_key,attempt_id,server_id,state,ciphertext FROM run_retry_external_targets WHERE id=?", id).Scan(&op.RootID, &op.NodeKey, &op.AttemptID, &op.ServerID, &op.State, &cipher)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if op.RootID != l.root || op.NodeKey != nodeKey {
		return nil, ErrConflict
	}
	if err = s.open(cipher, "target", l.root, id, &op.Proof); err != nil {
		return nil, err
	}
	if op.Proof.RootID != op.RootID || op.Proof.NodeKey != op.NodeKey || op.Proof.AttemptID != op.AttemptID || op.Proof.ServerID != op.ServerID {
		return nil, ErrConflict
	}
	return op, nil
}
func (s *Service) FinishTarget(ctx context.Context, runID, nodeKey, id, state string, proof TargetProof) error {
	if state != "success" && state != "failed" && state != "outcome_unknown" {
		return ErrInvalid
	}
	if state != "outcome_unknown" && !proof.ExitKnown && !proof.NotStarted {
		return ErrUnknown
	}
	if state == "success" && (!proof.ExitKnown || proof.ExitCode != 0 || proof.NotStarted) {
		return ErrInvalid
	}
	op, err := s.TargetOperation(ctx, runID, nodeKey, id)
	if err != nil {
		return err
	}
	if op.AttemptID != runID {
		return ErrConflict
	}
	if proof.RootID != "" && proof.RootID != op.RootID || proof.NodeKey != "" && proof.NodeKey != op.NodeKey || proof.AttemptID != "" && proof.AttemptID != op.AttemptID || proof.ServerID != "" && proof.ServerID != op.ServerID {
		return ErrConflict
	}
	// Binding identities and private configuration cannot be swapped at Finish.
	if proof.ConfigHash != op.Proof.ConfigHash || proof.ConnectionFingerprint != op.Proof.ConnectionFingerprint || proof.ArtifactID != op.Proof.ArtifactID || proof.ArtifactReference != op.Proof.ArtifactReference || proof.Kind != op.Proof.Kind {
		return ErrConflict
	}
	proof.RootID, proof.NodeKey, proof.AttemptID, proof.ServerID = op.RootID, op.NodeKey, op.AttemptID, op.ServerID
	proof.ProcessOwner = op.Proof.ProcessOwner
	cipher, err := s.seal("target", op.RootID, id, proof)
	if err != nil {
		return err
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		var project string
		if e := q.QueryRowContext(tx, "SELECT project_id FROM run_retry_lineages WHERE root_id=?", op.RootID).Scan(&project); e != nil {
			return e
		}
		if e := s.lock(tx, q, lineage{root: op.RootID, project: project}); e != nil {
			return e
		}
		var current string
		var owner string
		if err := q.QueryRowContext(tx, "SELECT owner_epoch FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", runID, nodeKey).Scan(&owner); err != nil {
			return err
		}
		if owner != s.owner {
			return ErrConflict
		}
		if err := q.QueryRowContext(tx, "SELECT state FROM run_retry_external_targets WHERE id=?", id).Scan(&current); err != nil {
			return err
		}
		if current != "running" && current != "outcome_unknown" {
			if current == state && digest(op.Proof) == digest(proof) {
				return nil
			}
			return ErrConflict
		}
		_, err := q.ExecContext(tx, "UPDATE run_retry_external_targets SET state=?,ciphertext=?,updated_at=? WHERE id=?", state, cipher, time.Now().UnixMilli(), id)
		return err
	})
}

// ProgressTarget links an existing deployment receipt without declaring an
// operation complete. A lost final write therefore leaves durable evidence.
func (s *Service) ProgressTarget(ctx context.Context, runID, node, id string, p TargetProof) error {
	op, err := s.TargetOperation(ctx, runID, node, id)
	if err != nil {
		return err
	}
	if op.AttemptID != runID || op.Proof.ProcessOwner != s.owner {
		return ErrConflict
	}
	if op.State != "running" && op.State != "outcome_unknown" {
		return ErrConflict
	}
	if p.ConfigHash != op.Proof.ConfigHash || p.ConnectionFingerprint != op.Proof.ConnectionFingerprint || p.ArtifactID != op.Proof.ArtifactID || p.ArtifactReference != op.Proof.ArtifactReference || p.Kind != op.Proof.Kind {
		return ErrConflict
	}
	p.RootID, p.NodeKey, p.AttemptID, p.ServerID, p.ProcessOwner = op.RootID, op.NodeKey, op.AttemptID, op.ServerID, op.Proof.ProcessOwner
	cipher, err := s.seal("target", op.RootID, id, p)
	if err != nil {
		return err
	}
	l, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := s.lock(tx, q, l); err != nil {
			return err
		}
		var owner, status string
		if err := q.QueryRowContext(tx, "SELECT owner_epoch,status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", runID, node).Scan(&owner, &status); err != nil {
			return err
		}
		if owner != s.owner || status != "running" {
			return ErrBlocked
		}
		_, err := q.ExecContext(tx, "UPDATE run_retry_external_targets SET ciphertext=?,updated_at=? WHERE id=? AND state IN ('running','outcome_unknown')", cipher, time.Now().UnixMilli(), id)
		return err
	})
}

type TargetVerifier func(context.Context, TargetOperation) (string, TargetProof, error)

func WithTargetVerifier(v TargetVerifier) Option { return func(s *Service) { s.targetVerifier = v } }
