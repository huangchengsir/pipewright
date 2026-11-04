package kubernetes

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Observation is a separate readonly authorization lifecycle. It is not a new
// deployment, cannot acquire a workload hold and never changes a source run.
func (s *Service) registerObserver(ctx context.Context, purpose, source, owner, engine string, a Authorization) (RuntimeLease, error) {
	var out RuntimeLease
	if (purpose != "probe" && purpose != "reconcile" && purpose != "reuse") || !validID(owner) || !cleanText(engine, 256) || (purpose == "probe" && source != "") || (purpose != "probe" && source == "") {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := configstate.CheckMaintenanceAdmission(ctx, q); e != nil {
			return e
		}
		if e := s.validateAuthorization(ctx, q, a); e != nil {
			return e
		}
		if source != "" {
			op, e := s.operation(ctx, q, source)
			if e != nil {
				return e
			}
			old := op.Intent.Authorization
			// Credential rotation and grant changes need new current authorization, but
			// observation may not silently move to another trust/target/tool identity.
			if old.Target != a.Target || old.ClusterKey != a.ClusterKey || old.ProfileFingerprint != a.ProfileFingerprint {
				return ErrConflict
			}
			if purpose == "reuse" && !op.Successful() {
				return ErrDenied
			}
			if purpose == "reconcile" && !op.OwnerFenced {
				return ErrOwner
			}
		}
		var count int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_runtime_leases WHERE cleanup_state<>'verified'`).Scan(&count); e != nil {
			return e
		}
		if count >= 2 {
			return ErrInUse
		}
		id := "kobs_" + uuid.NewString()
		now := s.now().UnixMilli()
		out = RuntimeLease{OperationID: id, Purpose: purpose, SourceOperationID: source, Authorization: &a, OwnerEpoch: owner, Revision: 1, EngineID: engine, ContainerName: RuntimeContainerName(id), Cleanup: CleanupPending, CreatedAt: now, UpdatedAt: now}
		body, _ := json.Marshal(out)
		var sourceID any
		if source != "" {
			sourceID = source
		}
		_, e := q.ExecContext(ctx, `INSERT INTO k8s_runtime_leases(operation_id,source_operation_id,owner_epoch,revision,container_name,engine_id,lease_json,cleanup_state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, sourceID, owner, 1, out.ContainerName, engine, string(body), out.Cleanup, now, now)
		return e
	})
	return out, e
}
func (s *Service) verifyObserverRuntime(ctx context.Context, q dbtx, p RuntimeProof, expected int64) (RuntimeLease, error) {
	l, e := s.runtimeLease(ctx, q, p.OperationID)
	if e != nil {
		return l, e
	}
	if l.Revision != expected {
		return l, ErrConflict
	}
	if l.Purpose == "writer" || l.Authorization == nil || l.OwnerEpoch != p.OwnerEpoch || l.EngineID != p.EngineID || l.Cleanup != CleanupPending || (l.ContainerID != "" && l.ContainerID != p.ContainerID) {
		return l, ErrOwner
	}
	if e = s.validateAuthorization(ctx, q, *l.Authorization); e != nil {
		return l, e
	}
	if !validRuntimeProof(p, *l.Authorization) {
		return l, ErrRuntimeUnverified
	}
	l.ContainerID = p.ContainerID
	l.Proof = &p
	e = s.saveRuntime(ctx, q, &l, expected)
	return l, e
}
func (s *Service) useObserverCredential(ctx context.Context, l RuntimeLease, p RuntimeProof, consume SecretConsumer) error {
	if l.Authorization == nil || consume == nil || !strings.HasPrefix(l.OperationID, "kobs_") {
		return ErrDenied
	}
	var data []byte
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		actual, e := s.runtimeLease(ctx, q, l.OperationID)
		if e != nil {
			return e
		}
		if actual.Purpose == "writer" || actual.Authorization == nil || actual.OwnerEpoch != p.OwnerEpoch || actual.Cleanup != CleanupPending || actual.Proof == nil || *actual.Proof != p || actual.CredentialInjected {
			return ErrDenied
		}
		a := *actual.Authorization
		if !validRuntimeProof(p, a) {
			return ErrRuntimeUnverified
		}
		if e = s.validateAuthorization(ctx, q, a); e != nil {
			return e
		}
		c, cipher, e := s.credential(ctx, q, a.Target.CredentialID)
		if e != nil {
			return e
		}
		data, e = s.open(credentialPurpose, c.ID, c.Revision, cipher)
		if e != nil {
			return e
		}
		if len(data) == 0 || len(data) > MaxCredentialBytes {
			clear(data)
			return ErrDecrypt
		}
		actual.CredentialInjected = true
		return s.saveRuntime(ctx, q, &actual, actual.Revision)
	})
	if e != nil {
		clear(data)
		return e
	}
	defer clear(data)
	return consume(ctx, data)
}

// PrepareRuntimeCreate is the durable once-only creation fence. SSH connection
// versions and the exact trusted image ID are saved before Docker sees a request.
func (s *Service) PrepareRuntimeCreate(ctx context.Context, id, owner string, snapshot *target.ConnectionSnapshot, toolImageID string) (RuntimeLease, error) {
	var out RuntimeLease
	if !strings.HasPrefix(toolImageID, "sha256:") || !fullHash.MatchString(strings.TrimPrefix(toolImageID, "sha256:")) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.runtimeLease(ctx, q, id)
		if e != nil {
			return e
		}
		if l.OwnerEpoch != owner || l.Cleanup != CleanupPending {
			return ErrOwner
		}
		if l.CreateRequested {
			return ErrConflict
		}
		var a Authorization
		if l.Purpose == "writer" {
			op, e := s.operation(ctx, q, id)
			if e != nil {
				return e
			}
			if op.OwnerFenced || op.DispatchFenced || op.Business != NotDispatched {
				return ErrOwner
			}
			a = op.Intent.Authorization
		} else {
			if l.Authorization == nil {
				return ErrDenied
			}
			a = *l.Authorization
		}
		if e = s.validateAuthorization(ctx, q, a); e != nil {
			return e
		}
		if a.Target.Executor.Kind == "ssh" && (snapshot == nil || snapshot.Server.ID != a.Target.Executor.ServerID) {
			return ErrRuntimeIdentity
		}
		if a.Target.Executor.Kind == "local" && snapshot != nil {
			return ErrRuntimeIdentity
		}
		l.CreateRequested = true
		l.SSHSnapshot = snapshot
		l.ToolImageID = toolImageID
		e = s.saveRuntime(ctx, q, &l, l.Revision)
		out = l
		return e
	})
	return out, e
}

// BindRuntimeContainer saves a discovered exact-label CID even if capability
// verification fails later, so cleanup never depends on a completed proof.
func (s *Service) BindRuntimeContainer(ctx context.Context, id, owner, engine, cid string) (RuntimeLease, error) {
	var out RuntimeLease
	if !imageID.MatchString(cid) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		l, e := s.runtimeLease(ctx, q, id)
		if e != nil {
			return e
		}
		if l.OwnerEpoch != owner || l.EngineID != engine || l.Cleanup != CleanupPending || !l.CreateRequested || (l.ContainerID != "" && l.ContainerID != cid) {
			return ErrOwner
		}
		l.ContainerID = cid
		e = s.saveRuntime(ctx, q, &l, l.Revision)
		out = l
		return e
	})
	return out, e
}
func (s *Service) PendingRuntimeCleanup(ctx context.Context) ([]RuntimeLease, error) {
	rows, e := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT operation_id FROM k8s_runtime_leases WHERE cleanup_state<>'verified' ORDER BY created_at LIMIT 500`)
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
	out := []RuntimeLease{}
	for _, id := range ids {
		l, e := s.GetRuntimeLease(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, l)
	}
	return out, nil
}
