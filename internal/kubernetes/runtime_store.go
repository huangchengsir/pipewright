package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"

	"github.com/huangchengsir/pipewright/internal/configstate"
)

type RuntimeLease struct {
	Purpose            string                     `json:"purpose,omitempty"`
	SourceOperationID  string                     `json:"sourceOperationId,omitempty"`
	Authorization      *Authorization             `json:"authorization,omitempty"`
	SSHSnapshot        *target.ConnectionSnapshot `json:"sshSnapshot,omitempty"`
	ToolImageID        string                     `json:"toolImageId,omitempty"`
	ClientMinor        int                        `json:"clientMinor,omitempty"`
	CreateRequested    bool                       `json:"createRequested,omitempty"`
	CredentialInjected bool                       `json:"credentialInjected,omitempty"`
	OperationID        string                     `json:"operationId"`
	OwnerEpoch         string                     `json:"ownerEpoch"`
	Revision           int64                      `json:"revision"`
	ContainerName      string                     `json:"containerName"`
	EngineID           string                     `json:"engineId"`
	ContainerID        string                     `json:"containerId"`
	Cleanup            CleanupState               `json:"cleanup"`
	Proof              *RuntimeProof              `json:"proof,omitempty"`
	CleanupEvidence    *CleanupEvidence           `json:"cleanupEvidence,omitempty"`
	CreatedAt          int64                      `json:"createdAt"`
	UpdatedAt          int64                      `json:"updatedAt"`
}
type CleanupEvidence struct {
	CreationNotRequested bool   `json:"creationNotRequested,omitempty"`
	ContainerName        string `json:"containerName"`
	ContainerID          string `json:"containerId"`
	EngineID             string `json:"engineId"`
	OwnerEpoch           string `json:"ownerEpoch"`
	LabelsMatched        bool   `json:"labelsMatched"`
	WriterExited         bool   `json:"writerExited"`
	ContainerAbsent      bool   `json:"containerAbsent"`
}

func RuntimeContainerName(operationID string) string {
	return "pw-k8s-" + strings.ReplaceAll(strings.TrimPrefix(operationID, "kop_"), "-", "")
}
func (s *Service) runtimeLease(ctx context.Context, q dbtx, id string) (RuntimeLease, error) {
	var l RuntimeLease
	var body string
	e := q.QueryRowContext(ctx, `SELECT lease_json,revision,cleanup_state FROM k8s_runtime_leases WHERE operation_id=?`, id).Scan(&body, &l.Revision, &l.Cleanup)
	if e != nil {
		return l, rowError(e)
	}
	var saved RuntimeLease
	if json.Unmarshal([]byte(body), &saved) != nil || saved.OperationID != id || saved.Revision != l.Revision || saved.Cleanup != l.Cleanup {
		return l, ErrInvalid
	}
	return saved, nil
}
func (s *Service) GetRuntimeLease(ctx context.Context, id string) (RuntimeLease, error) {
	return s.runtimeLease(ctx, configstate.Queryer(ctx, s.db), id)
}

// RegisterRuntime commits a deterministic name/owner intent before Docker create.
// There is at most one runtime lease per operation. An ambiguous response must
// be recovered by GetRuntimeLease and exact-name inspection, not a new create.
func (s *Service) RegisterRuntime(ctx context.Context, id, owner, engineID string) (RuntimeLease, error) {
	var out RuntimeLease
	if !cleanText(engineID, 256) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := configstate.CheckMaintenanceAdmission(ctx, q); e != nil {
			return e
		}
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.OwnerEpoch != owner || o.OwnerFenced || o.Retired {
			return ErrOwner
		}
		if o.DispatchFenced || o.Business != NotDispatched {
			return ErrConflict
		}
		if e = s.validateAuthorization(ctx, q, o.Intent.Authorization); e != nil {
			return e
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_runtime_leases WHERE operation_id=?`, id).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_runtime_leases WHERE cleanup_state<>'verified'`).Scan(&count); e != nil {
			return e
		}
		if count >= 2 {
			return ErrInUse
		}
		now := s.now().UnixMilli()
		out = RuntimeLease{Purpose: "writer", SourceOperationID: id, OperationID: id, OwnerEpoch: owner, Revision: 1, ContainerName: RuntimeContainerName(id), EngineID: engineID, Cleanup: CleanupPending, CreatedAt: now, UpdatedAt: now}
		b, _ := json.Marshal(out)
		_, e = q.ExecContext(ctx, `INSERT INTO k8s_runtime_leases(operation_id,source_operation_id,owner_epoch,revision,container_name,engine_id,lease_json,cleanup_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, id, owner, 1, out.ContainerName, engineID, string(b), CleanupPending, now, now)
		if e != nil {
			return e
		}
		o.Cleanup = CleanupPending
		return s.saveOperation(ctx, q, &o, o.Revision)
	})
	return out, e
}
func (s *Service) saveRuntime(ctx context.Context, q dbtx, l *RuntimeLease, expected int64) error {
	l.Revision = expected + 1
	l.UpdatedAt = s.now().UnixMilli()
	b, _ := json.Marshal(l)
	return requireOne(q.ExecContext(ctx, `UPDATE k8s_runtime_leases SET revision=?,container_id=?,lease_json=?,cleanup_state=?,updated_at=? WHERE operation_id=? AND revision=?`, l.Revision, l.ContainerID, string(b), l.Cleanup, l.UpdatedAt, l.OperationID, expected))
}

// VerifyRuntime records proof after a caller has checked the real created runtime.
// It cannot replace an already pinned CID, engine, owner or authorization.
func (s *Service) VerifyRuntime(ctx context.Context, p RuntimeProof, expected int64) (RuntimeLease, error) {
	var out RuntimeLease
	if !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		if strings.HasPrefix(p.OperationID, "kobs_") {
			var e error
			out, e = s.verifyObserverRuntime(ctx, q, p, expected)
			return e
		}
		o, e := s.operation(ctx, q, p.OperationID)
		if e != nil {
			return e
		}
		if o.OwnerEpoch != p.OwnerEpoch || o.OwnerFenced || o.Retired {
			return ErrOwner
		}
		if e = s.validateAuthorization(ctx, q, o.Intent.Authorization); e != nil {
			return e
		}
		if !validRuntimeProof(p, o.Intent.Authorization) {
			return ErrRuntimeUnverified
		}
		l, e := s.runtimeLease(ctx, q, p.OperationID)
		if e != nil {
			return e
		}
		if l.Revision != expected {
			return ErrConflict
		}
		if l.OwnerEpoch != p.OwnerEpoch || l.EngineID != p.EngineID || l.Cleanup != CleanupPending || (l.ContainerID != "" && l.ContainerID != p.ContainerID) {
			return ErrRuntimeUnverified
		}
		l.ContainerID = p.ContainerID
		l.Proof = &p
		if e = s.saveRuntime(ctx, q, &l, expected); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}

// RecordCleanup never treats lease expiry or an SSH/CLI exit as container cleanup.
// Recovery may clean a fenced owner, but must prove the original exact identity.
func (s *Service) RecordCleanup(ctx context.Context, id string, expected int64, evidence CleanupEvidence) (RuntimeLease, error) {
	var out RuntimeLease
	if !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.runtimeLease(ctx, q, id)
		if e != nil {
			return e
		}
		if l.Revision != expected {
			return ErrConflict
		}
		if evidence.OwnerEpoch != l.OwnerEpoch || evidence.EngineID != l.EngineID || evidence.ContainerName != l.ContainerName || evidence.ContainerID != l.ContainerID {
			return ErrOwner
		}
		if evidence.CreationNotRequested {
			if l.CreateRequested || l.ContainerID != "" || l.Proof != nil {
				return ErrRuntimeUnverified
			}
		} else if !evidence.LabelsMatched || !evidence.WriterExited || !evidence.ContainerAbsent {
			return ErrRuntimeUnverified
		}
		l.Cleanup = CleanupVerified
		l.CleanupEvidence = &evidence
		if e = s.saveRuntime(ctx, q, &l, expected); e != nil {
			return e
		}
		if l.Purpose != "writer" {
			out = l
			return nil
		}
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		o.Cleanup = CleanupVerified
		if e = s.saveOperation(ctx, q, &o, o.Revision); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}
func (s *Service) RecordCleanupBlocked(ctx context.Context, id string, expected int64) (RuntimeLease, error) {
	var out RuntimeLease
	if !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.runtimeLease(ctx, q, id)
		if e != nil {
			return e
		}
		if l.Revision != expected || l.Cleanup == CleanupVerified {
			return ErrConflict
		}
		l.Cleanup = CleanupBlocked
		if e = s.saveRuntime(ctx, q, &l, expected); e != nil {
			return e
		}
		if l.Purpose != "writer" {
			out = l
			return nil
		}
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		o.Cleanup = CleanupBlocked
		if e = s.saveOperation(ctx, q, &o, o.Revision); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}
