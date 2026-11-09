package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"
)

const releaseRuntimePurpose = "pipewright.k8s.release-runtime.v2"

type ReleaseRuntimeLease struct {
	ID            string               `json:"id"`
	ReleaseID     string               `json:"releaseId,omitempty"`
	Authorization ReleaseAuthorization `json:"authorization"`
	Runtime       RuntimeLease         `json:"runtime"`
}

func (s *Service) releaseRuntime(ctx context.Context, q dbtx, id string) (ReleaseRuntimeLease, error) {
	var l ReleaseRuntimeLease
	var revision int64
	var state CleanupState
	var cipher []byte
	e := q.QueryRowContext(ctx, `SELECT revision,cleanup_state,runtime_cipher FROM k8s_release_runtime WHERE id=?`, id).Scan(&revision, &state, &cipher)
	if e != nil {
		return l, rowError(e)
	}
	b, e := s.open(releaseRuntimePurpose, id, revision, cipher)
	if e != nil {
		return l, e
	}
	defer clear(b)
	if json.Unmarshal(b, &l) != nil || l.ID != id || l.Runtime.Revision != revision || l.Runtime.Cleanup != state {
		return l, ErrDecrypt
	}
	return l, nil
}
func (s *Service) GetReleaseRuntime(ctx context.Context, id string) (ReleaseRuntimeLease, error) {
	return s.releaseRuntime(ctx, configstate.Queryer(ctx, s.db), id)
}
func (s *Service) saveReleaseRuntime(ctx context.Context, q dbtx, l *ReleaseRuntimeLease) error {
	expected := l.Runtime.Revision
	l.Runtime.Revision++
	l.Runtime.UpdatedAt = s.now().UnixMilli()
	b, _ := json.Marshal(l)
	defer clear(b)
	cipher, e := s.seal(releaseRuntimePurpose, l.ID, l.Runtime.Revision, b)
	if e != nil {
		return e
	}
	if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_runtime SET revision=?,cleanup_state=?,runtime_cipher=?,updated_at=? WHERE id=? AND revision=?`, l.Runtime.Revision, l.Runtime.Cleanup, cipher, l.Runtime.UpdatedAt, l.ID, expected)); e != nil {
		return e
	}
	if l.ReleaseID != "" {
		_, e = q.ExecContext(ctx, `UPDATE k8s_releases SET cleanup_state=?,updated_at=? WHERE id=? AND owner_epoch=?`, l.Runtime.Cleanup, l.Runtime.UpdatedAt, l.ReleaseID, l.Runtime.OwnerEpoch)
	}
	return e
}
func (s *Service) RegisterReleaseRuntime(ctx context.Context, releaseID, owner, engine string, a ReleaseAuthorization) (ReleaseRuntimeLease, error) {
	var out ReleaseRuntimeLease
	if !validID(owner) || !cleanText(engine, 256) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := configstate.CheckMaintenanceAdmission(ctx, q); e != nil {
			return e
		}
		if e := s.validateReleaseAuthorization(ctx, q, a); e != nil {
			return e
		}
		purpose := "preview"
		if releaseID != "" {
			r, e := s.release(ctx, q, releaseID)
			if e != nil {
				return e
			}
			if r.OwnerEpoch != owner || r.OwnerFenced || r.State != "running" || r.Plan.Authorization.Fingerprint != a.Fingerprint {
				return ErrOwner
			}
			if (a.RequireApproval || r.Plan.ForceApproval) && r.ApprovedID != r.PlanID {
				return ErrDenied
			}
			if r.Cleanup != CleanupVerified {
				return ErrRuntimeUnverified
			}
			purpose = "writer"
		}
		var count int
		if e := q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM k8s_release_runtime WHERE cleanup_state<>'verified')+(SELECT COUNT(*) FROM k8s_runtime_leases WHERE cleanup_state<>'verified')`).Scan(&count); e != nil {
			return e
		}
		if count >= 2 {
			return ErrInUse
		}
		id := "krt_" + uuid.NewString()
		now := s.now().UnixMilli()
		l := RuntimeLease{Purpose: purpose, OperationID: id, OwnerEpoch: owner, Revision: 1, ContainerName: RuntimeContainerName(id), EngineID: engine, Cleanup: CleanupPending, CreatedAt: now, UpdatedAt: now}
		out = ReleaseRuntimeLease{ID: id, ReleaseID: releaseID, Authorization: a, Runtime: l}
		b, _ := json.Marshal(out)
		defer clear(b)
		cipher, e := s.seal(releaseRuntimePurpose, id, 1, b)
		if e != nil {
			return e
		}
		var parent any
		if releaseID != "" {
			parent = releaseID
		}
		if _, e = q.ExecContext(ctx, `INSERT INTO k8s_release_runtime(id,release_id,owner_epoch,revision,cleanup_state,runtime_cipher,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, parent, owner, 1, CleanupPending, cipher, now, now); e != nil {
			return e
		}
		if releaseID != "" {
			_, e = q.ExecContext(ctx, `UPDATE k8s_releases SET cleanup_state='pending',updated_at=? WHERE id=? AND owner_epoch=?`, now, releaseID, owner)
		}
		return e
	})
	return out, e
}
func (s *Service) PrepareReleaseRuntimeCreate(ctx context.Context, id, owner string, expected int64, snapshot *target.ConnectionSnapshot, image string) (ReleaseRuntimeLease, error) {
	var out ReleaseRuntimeLease
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.releaseRuntime(ctx, q, id)
		if e != nil {
			return e
		}
		if l.Runtime.OwnerEpoch != owner || l.Runtime.Revision != expected || l.Runtime.CreateRequested || l.Runtime.Cleanup != CleanupPending {
			return ErrConflict
		}
		if !strings.HasPrefix(image, "sha256:") || !fullHash.MatchString(strings.TrimPrefix(image, "sha256:")) {
			return ErrInvalid
		}
		if l.Authorization.Request.Executor.Kind == "ssh" && snapshot == nil || l.Authorization.Request.Executor.Kind == "local" && snapshot != nil {
			return ErrRuntimeIdentity
		}
		l.Runtime.SSHSnapshot = snapshot
		l.Runtime.ToolImageID = image
		l.Runtime.CreateRequested = true
		if e = s.saveReleaseRuntime(ctx, q, &l); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}
func validReleaseRuntimeProof(p RuntimeProof, a ReleaseAuthorization) bool {
	return validRuntimeProof(p, Authorization{Target: Target{Executor: a.Request.Executor}, ProfileFingerprint: fingerprint(a.Profile.Profile), Fingerprint: a.Fingerprint})
}
func (s *Service) VerifyReleaseRuntime(ctx context.Context, id, owner string, expected int64, p RuntimeProof) (ReleaseRuntimeLease, error) {
	var out ReleaseRuntimeLease
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.releaseRuntime(ctx, q, id)
		if e != nil {
			return e
		}
		if l.Runtime.OwnerEpoch != owner || l.Runtime.Revision != expected || l.Runtime.Cleanup != CleanupPending || !l.Runtime.CreateRequested {
			return ErrOwner
		}
		if e = s.validateReleaseAuthorization(ctx, q, l.Authorization); e != nil {
			return e
		}
		if p.OperationID != id || p.OwnerEpoch != owner || p.EngineID != l.Runtime.EngineID || !validReleaseRuntimeProof(p, l.Authorization) {
			return ErrRuntimeUnverified
		}
		l.Runtime.ContainerID = p.ContainerID
		l.Runtime.Proof = &p
		if e = s.saveReleaseRuntime(ctx, q, &l); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}
func (s *Service) UseReleaseCredential(ctx context.Context, id, owner string, consume SecretConsumer) error {
	if consume == nil {
		return ErrInvalid
	}
	var data []byte
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.releaseRuntime(ctx, q, id)
		if e != nil {
			return e
		}
		p := l.Runtime.Proof
		if l.Runtime.OwnerEpoch != owner || l.Runtime.Cleanup != CleanupPending || l.Runtime.CredentialInjected || p == nil || !validReleaseRuntimeProof(*p, l.Authorization) {
			return ErrRuntimeUnverified
		}
		if e = s.validateReleaseAuthorization(ctx, q, l.Authorization); e != nil {
			return e
		}
		if l.ReleaseID != "" {
			r, e := s.release(ctx, q, l.ReleaseID)
			if e != nil {
				return e
			}
			if r.OwnerEpoch != owner || r.OwnerFenced || r.State != "running" || (l.Authorization.RequireApproval || r.Plan.ForceApproval) && r.ApprovedID != r.PlanID {
				return ErrDenied
			}
		}
		cred, cipher, e := s.credential(ctx, q, l.Authorization.Request.CredentialID)
		if e != nil {
			return e
		}
		data, e = s.open(credentialPurpose, cred.ID, cred.Revision, cipher)
		if e != nil {
			return e
		}
		if len(data) == 0 || len(data) > MaxCredentialBytes {
			return ErrDecrypt
		}
		l.Runtime.CredentialInjected = true
		return s.saveReleaseRuntime(ctx, q, &l)
	})
	if e != nil {
		clear(data)
		return e
	}
	defer clear(data)
	return consume(ctx, data)
}
func (s *Service) RecordReleaseRuntimeCleanup(ctx context.Context, id, owner string, expected int64, proof CleanupEvidence) (ReleaseRuntimeLease, error) {
	var out ReleaseRuntimeLease
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.releaseRuntime(ctx, q, id)
		if e != nil {
			return e
		}
		r := l.Runtime
		if r.OwnerEpoch != owner || r.Revision != expected {
			return ErrConflict
		}
		if proof.OwnerEpoch != owner || proof.EngineID != r.EngineID || proof.ContainerName != r.ContainerName || !proof.WriterExited || !proof.ContainerAbsent {
			return ErrRuntimeUnverified
		}
		if r.CreateRequested {
			if r.ContainerID == "" || proof.ContainerID != r.ContainerID || !proof.LabelsMatched {
				return ErrRuntimeUnverified
			}
		} else if !proof.CreationNotRequested {
			return ErrRuntimeUnverified
		}
		l.Runtime.Cleanup = CleanupVerified
		l.Runtime.CleanupEvidence = &proof
		if e = s.saveReleaseRuntime(ctx, q, &l); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}
func (s *Service) VerifyReleaseWriterLease(ctx context.Context, q dbtx, releaseID, owner string) error {
	rows, e := q.QueryContext(ctx, `SELECT id FROM k8s_release_runtime WHERE release_id=? AND owner_epoch=? AND cleanup_state='pending' ORDER BY created_at DESC`, releaseID, owner)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(ids) != 1 {
		return ErrRuntimeUnverified
	}
	l, e := s.releaseRuntime(ctx, q, ids[0])
	if e != nil {
		return e
	}
	if l.Runtime.Proof == nil || !l.Runtime.CredentialInjected || !validReleaseRuntimeProof(*l.Runtime.Proof, l.Authorization) {
		return ErrRuntimeUnverified
	}
	return nil
}

func (s *Service) BindReleaseRuntimeContainer(ctx context.Context, id, owner string, expected int64, engine, container string) (ReleaseRuntimeLease, error) {
	var out ReleaseRuntimeLease
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.releaseRuntime(ctx, q, id)
		if e != nil {
			return e
		}
		if l.Runtime.OwnerEpoch != owner || l.Runtime.Revision != expected || l.Runtime.EngineID != engine || !l.Runtime.CreateRequested || !imageID.MatchString(container) || l.Runtime.ContainerID != "" && l.Runtime.ContainerID != container {
			return ErrRuntimeIdentity
		}
		l.Runtime.ContainerID = container
		if e = s.saveReleaseRuntime(ctx, q, &l); e != nil {
			return e
		}
		out = l
		return nil
	})
	return out, e
}
