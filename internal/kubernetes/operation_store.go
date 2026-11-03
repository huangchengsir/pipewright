package kubernetes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type BusinessState string
type CleanupState string

const (
	NotDispatched   BusinessState = "not_dispatched"
	Submitted       BusinessState = "submitted"
	Ready           BusinessState = "ready"
	Unchanged       BusinessState = "unchanged"
	Failed          BusinessState = "failed"
	Superseded      BusinessState = "superseded"
	OutcomeUnknown  BusinessState = "outcome_unknown"
	CleanupPending  CleanupState  = "pending"
	CleanupVerified CleanupState  = "verified"
	CleanupBlocked  CleanupState  = "blocked"
)

// ImageSource is a reference to already verified pipeline push evidence. The
// build integration, not a job/user DTO, constructs this structure.
type ImageSource struct {
	RunID            string `json:"runId"`
	StageID          string `json:"stageId"`
	JobID            string `json:"jobId"`
	DeclarationIndex int    `json:"declarationIndex"`
	Commit           string `json:"commit"`
	ArtifactID       string `json:"artifactId"`
}
type Intent struct {
	Authorization  Authorization `json:"authorization"`
	Image          string        `json:"image"`
	Source         ImageSource   `json:"source"`
	TimeoutSeconds int           `json:"timeoutSeconds"`
}
type OperationInput struct {
	RunID      string `json:"runId"`
	NodeID     string `json:"nodeId"`
	Attempt    int    `json:"attempt"`
	OwnerEpoch string `json:"ownerEpoch"`
	Intent     Intent `json:"intent"`
}

// Receipt only contains whitelisted facts; never raw Deployment JSON or errors.
type Receipt struct {
	// DeploymentAt is the immutable local dispatch/no-op event, never an observation timestamp.
	DeploymentAt            int64  `json:"deploymentAt,omitempty"`
	UID                     string `json:"uid"`
	ResourceVersion         string `json:"resourceVersion"`
	Generation              int64  `json:"generation"`
	ObservedGeneration      int64  `json:"observedGeneration"`
	Revision                string `json:"revision,omitempty"`
	Image                   string `json:"image"`
	Marker                  string `json:"marker"`
	TemplateHash            string `json:"templateHash"`
	DesiredReplicas         int64  `json:"desiredReplicas"`
	TotalReplicas           int64  `json:"totalReplicas"`
	UpdatedReplicas         int64  `json:"updatedReplicas"`
	AvailableReplicas       int64  `json:"availableReplicas"`
	Paused                  bool   `json:"paused"`
	PreparedUID             string `json:"preparedUid,omitempty"`
	PreparedResourceVersion string `json:"preparedResourceVersion,omitempty"`
	PreparedTemplateHash    string `json:"preparedTemplateHash,omitempty"`
	ExpectedTemplateHash    string `json:"expectedTemplateHash,omitempty"`
	Reason                  string `json:"reason,omitempty"`
}
type Operation struct {
	ID string `json:"id"`
	OperationInput
	Revision       int64         `json:"revision"`
	Business       BusinessState `json:"business"`
	Cleanup        CleanupState  `json:"cleanup"`
	OwnerFenced    bool          `json:"ownerFenced"`
	Retired        bool          `json:"retired"`
	DispatchFenced bool          `json:"dispatchFenced"`
	IntentHash     string        `json:"intentHash"`
	ApprovedHash   string        `json:"approvedHash"`
	ReasonCode     string        `json:"reasonCode,omitempty"`
	Receipt        *Receipt      `json:"receipt,omitempty"`
	CreatedAt      int64         `json:"createdAt"`
	UpdatedAt      int64         `json:"updatedAt"`
}

func (o Operation) Successful() bool {
	return (o.Business == Ready || o.Business == Unchanged) && o.Cleanup == CleanupVerified && !o.Retired
}
func validIntent(i Intent) bool {
	return validateTarget(i.Authorization.Target) == nil && fingerprintWithoutAuthHash(i.Authorization) == i.Authorization.Fingerprint && ValidImageReference(i.Image) && i.TimeoutSeconds >= 30 && i.TimeoutSeconds <= 1800 && validID(i.Source.RunID) && validID(i.Source.StageID) && validID(i.Source.JobID) && validID(i.Source.ArtifactID) && i.Source.DeclarationIndex >= 0 && i.Source.DeclarationIndex < 1024 && (len(i.Source.Commit) == 40 || len(i.Source.Commit) == 64) && allHex(i.Source.Commit)
}
func allHex(s string) bool {
	for _, v := range s {
		if !(v >= '0' && v <= '9') && !(v >= 'a' && v <= 'f') {
			return false
		}
	}
	return len(s) > 0
}

const intentPurpose = "pipewright.k8s.intent.v1"
const receiptPurpose = "pipewright.k8s.receipt.v1"

func (s *Service) CreateOperation(ctx context.Context, in OperationInput) (Operation, error) {
	var out Operation
	if !validID(in.RunID) || !cleanText(in.NodeID, 128) || in.Attempt < 1 || in.Attempt > 100000 || !validID(in.OwnerEpoch) || !validIntent(in.Intent) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := s.validateAuthorization(ctx, q, in.Intent.Authorization); e != nil {
			return e
		}
		var count int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_operations WHERE run_id=? AND node_id=? AND attempt=?`, in.RunID, in.NodeID, in.Attempt).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}
		id := "kop_" + uuid.NewString()
		now := s.now().UnixMilli()
		body, _ := json.Marshal(in.Intent)
		cipher, e := s.seal(intentPurpose, id, 1, body)
		if e != nil {
			return e
		}
		out = Operation{ID: id, OperationInput: in, Revision: 1, Business: NotDispatched, Cleanup: CleanupVerified, IntentHash: fingerprint(in.Intent), CreatedAt: now, UpdatedAt: now}
		_, e = q.ExecContext(ctx, `INSERT INTO k8s_operations(id,run_id,project_id,node_id,attempt,connection_id,credential_id,owner_epoch,revision,business_state,cleanup_state,intent_cipher,intent_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, in.RunID, in.Intent.Authorization.Target.ProjectID, in.NodeID, in.Attempt, in.Intent.Authorization.Target.ConnectionID, in.Intent.Authorization.Target.CredentialID, in.OwnerEpoch, 1, out.Business, out.Cleanup, cipher, out.IntentHash, now, now)
		return e
	})
	return out, e
}
func (s *Service) operation(ctx context.Context, q dbtx, id string) (Operation, error) {
	var o Operation
	var intentCipher, receiptCipher []byte
	e := q.QueryRowContext(ctx, `SELECT id,run_id,node_id,attempt,owner_epoch,revision,business_state,cleanup_state,owner_fenced,retired,dispatch_fenced,intent_cipher,receipt_cipher,intent_hash,approved_hash,reason_code,created_at,updated_at FROM k8s_operations WHERE id=?`, id).Scan(&o.ID, &o.RunID, &o.NodeID, &o.Attempt, &o.OwnerEpoch, &o.Revision, &o.Business, &o.Cleanup, &o.OwnerFenced, &o.Retired, &o.DispatchFenced, &intentCipher, &receiptCipher, &o.IntentHash, &o.ApprovedHash, &o.ReasonCode, &o.CreatedAt, &o.UpdatedAt)
	if e != nil {
		return o, rowError(e)
	}
	body, e := s.open(intentPurpose, id, 1, intentCipher)
	if e != nil {
		return o, e
	}
	defer clear(body)
	if json.Unmarshal(body, &o.Intent) != nil || fingerprint(o.Intent) != o.IntentHash {
		return o, ErrDecrypt
	}
	if len(receiptCipher) > 0 {
		r, e := s.open(receiptPurpose, id, o.Revision, receiptCipher)
		if e != nil {
			return o, e
		}
		defer clear(r)
		if json.Unmarshal(r, &o.Receipt) != nil || o.Receipt == nil {
			return o, ErrDecrypt
		}
	}
	return o, nil
}
func (s *Service) GetOperation(ctx context.Context, id string) (Operation, error) {
	return s.operation(ctx, configstate.Queryer(ctx, s.db), id)
}
func (s *Service) ListOperations(ctx context.Context, runID string) ([]Operation, error) {
	if !validID(runID) {
		return nil, ErrInvalid
	}
	return s.queryOperations(ctx, `SELECT id FROM k8s_operations WHERE run_id=? ORDER BY created_at,id LIMIT 500`, runID)
}

// PendingRecovery lists bounded original operations; it never executes a tool or
// takes over a lease. Fencing/cleanup require separate identity-proved actions.
func (s *Service) PendingRecovery(ctx context.Context) ([]Operation, error) {
	return s.queryOperations(ctx, `SELECT id FROM k8s_operations WHERE retired=0 AND (owner_fenced=0 OR cleanup_state<>'verified' OR business_state IN ('not_dispatched','submitted','outcome_unknown') OR EXISTS(SELECT 1 FROM k8s_workload_holds h WHERE h.operation_id=k8s_operations.id)) ORDER BY created_at,id LIMIT 500`)
}
func (s *Service) queryOperations(ctx context.Context, query string, args ...any) ([]Operation, error) {
	q := configstate.Queryer(ctx, s.db)
	rows, e := q.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []Operation{}
	for _, id := range ids {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, nil
}
func (s *Service) saveOperation(ctx context.Context, q dbtx, o *Operation, expected int64) error {
	if !validRevision(expected) {
		return ErrInvalid
	}
	o.Revision = expected + 1
	o.UpdatedAt = s.now().UnixMilli()
	var cipher []byte
	if o.Receipt != nil {
		b, _ := json.Marshal(o.Receipt)
		var e error
		cipher, e = s.seal(receiptPurpose, o.ID, o.Revision, b)
		if e != nil {
			return e
		}
	}
	return requireOne(q.ExecContext(ctx, `UPDATE k8s_operations SET revision=?,business_state=?,cleanup_state=?,owner_fenced=?,retired=?,dispatch_fenced=?,receipt_cipher=?,approved_hash=?,reason_code=?,updated_at=? WHERE id=? AND revision=?`, o.Revision, o.Business, o.Cleanup, o.OwnerFenced, o.Retired, o.DispatchFenced, cipher, o.ApprovedHash, o.ReasonCode, o.UpdatedAt, o.ID, expected))
}
func (s *Service) mutateOperation(ctx context.Context, id, owner string, expected int64, fn func(context.Context, dbtx, *Operation) error) (Operation, error) {
	var out Operation
	if !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.Revision != expected {
			return ErrConflict
		}
		if o.OwnerEpoch != owner || o.OwnerFenced || o.Retired {
			return ErrOwner
		}
		if e = fn(ctx, q, &o); e != nil {
			return e
		}
		if e = s.saveOperation(ctx, q, &o, expected); e != nil {
			return e
		}
		out = o
		return nil
	})
	return out, e
}
func (s *Service) ApproveOperation(ctx context.Context, id, owner string, expected int64, intentHash string) (Operation, error) {
	return s.mutateOperation(ctx, id, owner, expected, func(ctx context.Context, q dbtx, o *Operation) error {
		if o.DispatchFenced || o.IntentHash != intentHash {
			return ErrConflict
		}
		if e := s.validateAuthorization(ctx, q, o.Intent.Authorization); e != nil {
			return e
		}
		o.ApprovedHash = intentHash
		return nil
	})
}

type WorkloadHold struct {
	WorkloadKey   string `json:"workloadKey"`
	OperationID   string `json:"operationId"`
	OwnerEpoch    string `json:"ownerEpoch"`
	ClusterKey    string `json:"clusterKey"`
	Namespace     string `json:"namespace"`
	DeploymentUID string `json:"deploymentUid"`
	CreatedAt     int64  `json:"createdAt"`
}

// AcquireHold does not steal expired leases. The lock covers all project/worker
// users of the same canonical cluster, namespace and immutable workload UID.
func (s *Service) AcquireHold(ctx context.Context, id, owner, uid string) (WorkloadHold, error) {
	var out WorkloadHold
	if !cleanText(uid, 128) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.OwnerEpoch != owner || o.OwnerFenced || o.Retired {
			return ErrOwner
		}
		if e = s.validateAuthorization(ctx, q, o.Intent.Authorization); e != nil {
			return e
		}
		key := fingerprint([]string{o.Intent.Authorization.ClusterKey, o.Intent.Authorization.Target.Namespace, uid})
		out = WorkloadHold{key, id, owner, o.Intent.Authorization.ClusterKey, o.Intent.Authorization.Target.Namespace, uid, s.now().UnixMilli()}
		var existing, epoch string
		e = q.QueryRowContext(ctx, `SELECT operation_id,owner_epoch FROM k8s_workload_holds WHERE workload_key=?`, key).Scan(&existing, &epoch)
		if e == nil {
			if existing == id && epoch == owner {
				return nil
			}
			return ErrHeld
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var other int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_workload_holds WHERE operation_id=?`, id).Scan(&other); e != nil {
			return e
		}
		if other > 0 {
			return ErrConflict
		}
		_, e = q.ExecContext(ctx, `INSERT INTO k8s_workload_holds(workload_key,operation_id,owner_epoch,cluster_key,namespace,deployment_uid,created_at) VALUES(?,?,?,?,?,?,?)`, out.WorkloadKey, id, owner, out.ClusterKey, out.Namespace, uid, out.CreatedAt)
		return e
	})
	return out, e
}
func validReceipt(r Receipt) bool {
	if !cleanText(r.UID, 128) || !cleanText(r.ResourceVersion, 256) || r.Generation < 1 || r.ObservedGeneration < 0 || !fullHash.MatchString(r.TemplateHash) || !imageValue.MatchString(r.Image) || r.DesiredReplicas < 0 || r.TotalReplicas < 0 || r.UpdatedReplicas < 0 || r.AvailableReplicas < 0 {
		return false
	}
	if r.Marker != "" && !validID(r.Marker) {
		return false
	}
	if r.Revision != "" && !cleanText(r.Revision, 128) {
		return false
	}
	if r.Reason != "" && !validID(r.Reason) {
		return false
	}
	return true
}

// PrepareDispatch records the exact UID/RV fence and commits an unknown boundary
// BEFORE the single PATCH invocation. An interrupted caller cannot dispatch again.
func (s *Service) PrepareDispatch(ctx context.Context, id, owner string, expected int64, before Receipt) (Operation, error) {
	if !validReceipt(before) || before.Paused || before.DesiredReplicas <= 0 || !fullHash.MatchString(before.ExpectedTemplateHash) {
		return Operation{}, ErrInvalid
	}
	return s.mutateOperation(ctx, id, owner, expected, func(ctx context.Context, q dbtx, o *Operation) error {
		if o.DispatchFenced || o.Business != NotDispatched {
			return ErrConflict
		}
		if e := s.validateAuthorization(ctx, q, o.Intent.Authorization); e != nil {
			return e
		}
		if o.Intent.Authorization.RequireApproval && o.ApprovedHash != o.IntentHash {
			return ErrDenied
		}
		var uid, epoch string
		if e := q.QueryRowContext(ctx, `SELECT deployment_uid,owner_epoch FROM k8s_workload_holds WHERE operation_id=?`, id).Scan(&uid, &epoch); e != nil {
			return ErrHeld
		}
		if uid != before.UID || epoch != owner {
			return ErrHeld
		}
		lease, e := s.runtimeLease(ctx, q, id)
		if e != nil {
			return e
		}
		if lease.Proof == nil || lease.Cleanup != CleanupPending || !validRuntimeProof(*lease.Proof, o.Intent.Authorization) {
			return ErrRuntimeUnverified
		}
		before.DeploymentAt = s.now().UnixMilli()
		before.PreparedUID = before.UID
		before.PreparedResourceVersion = before.ResourceVersion
		before.PreparedTemplateHash = before.TemplateHash
		o.Receipt = &before
		o.DispatchFenced = true
		o.Business = OutcomeUnknown
		return nil
	})
}
func receiptReady(r Receipt) bool {
	return !r.Paused && r.DesiredReplicas > 0 && r.ObservedGeneration >= r.Generation && r.TotalReplicas == r.DesiredReplicas && r.UpdatedReplicas == r.DesiredReplicas && r.AvailableReplicas >= r.DesiredReplicas
}

// RecordObservation persists a whitelisted observation. Ready/unchanged require
// both complete readiness and this intent's image/identity, never CLI exit alone.
func (s *Service) RecordObservation(ctx context.Context, id, owner string, expected int64, state BusinessState, r Receipt) (Operation, error) {
	if !validReceipt(r) {
		return Operation{}, ErrInvalid
	}
	return s.mutateOperation(ctx, id, owner, expected, func(ctx context.Context, q dbtx, o *Operation) error {
		r.DeploymentAt = 0
		if state == Unchanged && o.Receipt == nil {
			r.DeploymentAt = s.now().UnixMilli()
		}
		return applyObservation(o, state, r)
	})
}
func applyObservation(o *Operation, state BusinessState, r Receipt) error {
	if !o.DispatchFenced && o.Business == Failed {
		if state != Failed {
			return ErrConflict
		}
		return nil // Preserve the original no-dispatch failure and its evidence.
	}
	switch state {
	case Submitted, Ready, Unchanged, Failed, Superseded, OutcomeUnknown:
	default:
		return ErrInvalid
	}
	if o.Receipt != nil {
		r.DeploymentAt = o.Receipt.DeploymentAt
		r.PreparedUID = o.Receipt.PreparedUID
		r.PreparedResourceVersion = o.Receipt.PreparedResourceVersion
		r.PreparedTemplateHash = o.Receipt.PreparedTemplateHash
		r.ExpectedTemplateHash = o.Receipt.ExpectedTemplateHash
	}
	if state == Ready || state == Submitted {
		if !o.DispatchFenced || r.Marker != o.ID || r.Image != o.Intent.Image || r.UID != r.PreparedUID {
			return ErrConflict
		}
	}
	if (o.Business == Submitted || o.Business == Ready || o.Business == Unchanged) && o.Receipt != nil && (r.UID != o.Receipt.UID || r.TemplateHash != o.Receipt.TemplateHash) && state != Superseded && state != OutcomeUnknown {
		return ErrConflict
	}
	if state == Ready || state == Unchanged {
		if !receiptReady(r) || r.Image != o.Intent.Image {
			return ErrConflict
		}
	}
	if state == Unchanged && o.DispatchFenced {
		return ErrConflict
	}
	if o.Business == Superseded && state != Superseded {
		return ErrConflict
	}
	if o.Business == OutcomeUnknown && state == Failed {
		return ErrConflict
	} // A lost PATCH is not disproved by a later failure.
	o.Business = state
	o.Receipt = &r
	return nil
}

// FenceOwner only fences the old writer locally. It is not cleanup evidence and
// cannot release holds, mark success or establish that a cluster request stopped.
func (s *Service) FenceOwner(ctx context.Context, id, owner string, expected int64) (Operation, error) {
	return s.mutateOperation(ctx, id, owner, expected, func(ctx context.Context, q dbtx, o *Operation) error {
		o.OwnerFenced = true
		if o.DispatchFenced && o.Business != Ready && o.Business != Unchanged && o.Business != Superseded && o.Business != Failed {
			o.Business = OutcomeUnknown
		}
		return nil
	})
}

// RecordReconciliation is for an explicitly requested read-only observer. It
// cannot create an operation/lease or dispatch a write, and preserves the source
// run's terminal status because this store never updates pipeline_runs.
func (s *Service) RecordReconciliation(ctx context.Context, id string, expected int64, state BusinessState, r Receipt) (Operation, error) {
	var out Operation
	if !validRevision(expected) || !validReceipt(r) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.Revision != expected || o.Retired {
			return ErrConflict
		}
		if !o.OwnerFenced {
			return ErrOwner
		}
		if o.Business == OutcomeUnknown && (state == Submitted || state == Ready) && (o.Receipt == nil || r.TemplateHash != o.Receipt.ExpectedTemplateHash) {
			return ErrConflict
		}
		if e = applyObservation(&o, state, r); e != nil {
			return e
		}
		if e = s.saveOperation(ctx, q, &o, expected); e != nil {
			return e
		}
		out = o
		return nil
	})
	return out, e
}
func (s *Service) ReleaseHold(ctx context.Context, id, owner string) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.OwnerEpoch != owner {
			return ErrOwner
		}
		if o.Cleanup != CleanupVerified || o.Business == OutcomeUnknown || o.Business == Submitted {
			return ErrHeld
		}
		_, e = q.ExecContext(ctx, `DELETE FROM k8s_workload_holds WHERE operation_id=? AND owner_epoch=?`, id, owner)
		return e
	})
}

// Retire ends only local tracking of an unknowable, fenced, fully cleaned-up
// operation. The caller must obtain recent-password confirmation and a fresh
// read-only observation; the original unknown outcome and receipt are retained.
func (s *Service) Retire(ctx context.Context, id string, expected int64, current Receipt) (Operation, error) {
	var out Operation
	if !validRevision(expected) || !validReceipt(current) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.Revision != expected || o.Retired {
			return ErrConflict
		}
		if !o.OwnerFenced || o.Cleanup != CleanupVerified || o.Business != OutcomeUnknown || !o.DispatchFenced || o.Receipt == nil || o.Receipt.PreparedUID == "" || o.Receipt.PreparedResourceVersion == "" || current.UID != o.Receipt.PreparedUID {
			return ErrDenied
		}
		lease, e := s.runtimeLease(ctx, q, id)
		if e != nil {
			return e
		}
		if lease.Cleanup != CleanupVerified || lease.CleanupEvidence == nil || !lease.CleanupEvidence.WriterExited {
			return ErrDenied
		}
		o.Retired = true
		if e = s.saveOperation(ctx, q, &o, expected); e != nil {
			return e
		}
		if _, e = q.ExecContext(ctx, `DELETE FROM k8s_workload_holds WHERE operation_id=?`, id); e != nil {
			return e
		}
		out = o
		return nil
	})
	return out, e
}

// FailBeforeDispatch records validation/tool failures without inventing cluster
// facts. Once dispatch is fenced the outcome must remain explicitly uncertain.
func (s *Service) FailBeforeDispatch(ctx context.Context, id, owner string, expected int64, reason string) (Operation, error) {
	if !validID(reason) {
		return Operation{}, ErrInvalid
	}
	return s.mutateOperation(ctx, id, owner, expected, func(ctx context.Context, q dbtx, o *Operation) error {
		if o.DispatchFenced || o.Business != NotDispatched {
			return ErrConflict
		}
		o.Business = Failed
		o.ReasonCode = reason
		return nil
	})
}
