package kubernetes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type dbtx = configstate.DBTX

// All K8s configuration and admission writers share these locks. They perform
// no external calls while holding the transaction. MySQL uses row locks; SQLite
// uses BEGIN IMMEDIATE through the existing transaction coordinator.
func (s *Service) write(ctx context.Context, fn func(context.Context, dbtx) error) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(ctx context.Context, q dbtx) error {
		if e := configstate.LockEpochs(ctx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets); e != nil {
			return e
		}
		return fn(ctx, q)
	})
}

// Configuration changes invalidate existing review catalogs, unlike runtime facts.
func (s *Service) configWrite(ctx context.Context, fn func(context.Context, dbtx) error) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if e := configstate.BumpEpoch(ctx, q, configstate.EpochPolicy); e != nil {
			return e
		}
		return configstate.BumpEpoch(ctx, q, configstate.EpochSecrets)
	})
}
func rowError(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	return e
}
func requireOne(result sql.Result, e error) error {
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func validRevision(r int64) bool { return r > 0 && r < math.MaxInt64 }

type secretEnvelope struct {
	Purpose  string `json:"purpose"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Data     []byte `json:"data"`
}

func (s *Service) seal(purpose, id string, revision int64, data []byte) ([]byte, error) {
	if s.sealer == nil {
		return nil, ErrDecrypt
	}
	raw, e := json.Marshal(secretEnvelope{purpose, id, revision, data})
	if e != nil {
		return nil, ErrInvalid
	}
	defer clear(raw)
	v, e := s.sealer.SealSecret(raw)
	if e != nil {
		return nil, ErrDecrypt
	}
	return v, nil
}
func (s *Service) open(purpose, id string, revision int64, cipher []byte) ([]byte, error) {
	if s.sealer == nil {
		return nil, ErrDecrypt
	}
	raw, e := s.sealer.OpenSecret(cipher)
	if e != nil {
		return nil, ErrDecrypt
	}
	defer clear(raw)
	var box secretEnvelope
	if json.Unmarshal(raw, &box) != nil || box.Purpose != purpose || box.ID != id || box.Revision != revision {
		clear(box.Data)
		return nil, ErrDecrypt
	}
	return box.Data, nil
}

const credentialPurpose = "pipewright.k8s.credential.v1"

func (s *Service) credential(ctx context.Context, q dbtx, id string) (Credential, []byte, error) {
	var c Credential
	var body string
	var cipher []byte
	e := q.QueryRowContext(ctx, `SELECT id,name,revision,enabled,metadata_json,secret_cipher,created_at,updated_at FROM k8s_credentials WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Revision, &c.Enabled, &body, &cipher, &c.CreatedAt, &c.UpdatedAt)
	if e != nil {
		return c, nil, rowError(e)
	}
	if json.Unmarshal([]byte(body), &c.CredentialPreview) != nil {
		return c, nil, ErrInvalid
	}
	return c, cipher, nil
}
func (s *Service) GetCredential(ctx context.Context, id string) (Credential, error) {
	c, _, e := s.credential(ctx, configstate.Queryer(ctx, s.db), id)
	return c, e
}
func (s *Service) ListCredentials(ctx context.Context) ([]Credential, error) {
	rows, e := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT id,name,revision,enabled,metadata_json,created_at,updated_at FROM k8s_credentials ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		var c Credential
		var body string
		if e = rows.Scan(&c.ID, &c.Name, &c.Revision, &c.Enabled, &body, &c.CreatedAt, &c.UpdatedAt); e != nil {
			return nil, e
		}
		if json.Unmarshal([]byte(body), &c.CredentialPreview) != nil {
			return nil, ErrInvalid
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCredential creates (empty id, revision 0) or rotates a credential. An
// existing connection's trust identity cannot be changed by rotating its secret.
func (s *Service) SaveCredential(ctx context.Context, id string, expected int64, in CredentialInput) (Credential, error) {
	var out Credential
	data, preview, e := normalizeCredential(in)
	if e != nil {
		return out, e
	}
	defer clear(data)
	create := id == ""
	if create {
		if expected != 0 {
			return out, ErrInvalid
		}
		id = CredentialPrefix + uuid.NewString()
	} else if !IsProtectedCredentialID(id) || !validID(id) || !validRevision(expected) {
		return out, ErrInvalid
	}
	e = s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		now := s.now().UnixMilli()
		created := now
		if !create {
			old, _, e := s.credential(ctx, q, id)
			if e != nil {
				return e
			}
			if old.Revision != expected {
				return ErrConflict
			}
			created = old.CreatedAt
			var count int
			if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_connections WHERE credential_id=?`, id).Scan(&count); e != nil {
				return e
			}
			if count > 0 && (old.ClusterKey != preview.ClusterKey || old.Context != preview.Context) {
				return ErrInUse
			}
		}
		out = Credential{ID: id, Name: in.Name, Revision: expected + 1, Enabled: in.Enabled, CredentialPreview: preview, CreatedAt: created, UpdatedAt: now}
		cipher, e := s.seal(credentialPurpose, id, out.Revision, data)
		if e != nil {
			return e
		}
		metadata, _ := json.Marshal(preview)
		if create {
			_, e = q.ExecContext(ctx, `INSERT INTO k8s_credentials(id,name,revision,enabled,metadata_json,secret_cipher,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, in.Name, out.Revision, in.Enabled, string(metadata), cipher, created, now)
			return e
		}
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_credentials SET name=?,revision=?,enabled=?,metadata_json=?,secret_cipher=?,updated_at=? WHERE id=? AND revision=?`, in.Name, out.Revision, in.Enabled, string(metadata), cipher, now, id, expected))
	})
	return out, e
}

// SetCredentialEnabled retains material but binds its envelope to the new version.
// This is an administrative mutation, not a secret export API.
func (s *Service) SetCredentialEnabled(ctx context.Context, id string, expected int64, enabled bool) (Credential, error) {
	var out Credential
	if !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		old, cipher, e := s.credential(ctx, q, id)
		if e != nil {
			return e
		}
		if old.Revision != expected {
			return ErrConflict
		}
		data, e := s.open(credentialPurpose, id, expected, cipher)
		if e != nil {
			return e
		}
		defer clear(data)
		cipher, e = s.seal(credentialPurpose, id, expected+1, data)
		if e != nil {
			return e
		}
		old.Enabled = enabled
		old.Revision++
		old.UpdatedAt = s.now().UnixMilli()
		out = old
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_credentials SET revision=?,enabled=?,secret_cipher=?,updated_at=? WHERE id=? AND revision=?`, out.Revision, enabled, cipher, out.UpdatedAt, id, expected))
	})
	return out, e
}
func (s *Service) DeleteCredential(ctx context.Context, id string, expected int64) error {
	if !validRevision(expected) {
		return ErrInvalid
	}
	return s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		c, _, e := s.credential(ctx, q, id)
		if e != nil {
			return e
		}
		if c.Revision != expected {
			return ErrConflict
		}
		var refs int
		if e = q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM k8s_connections WHERE credential_id=?)+(SELECT COUNT(*) FROM k8s_operations WHERE credential_id=?)`, id, id).Scan(&refs); e != nil {
			return e
		}
		if refs > 0 {
			return ErrInUse
		}
		return requireOne(q.ExecContext(ctx, `DELETE FROM k8s_credentials WHERE id=? AND revision=?`, id, expected))
	})
}
func (s *Service) connection(ctx context.Context, q dbtx, id string) (Connection, error) {
	var c Connection
	var body string
	e := q.QueryRowContext(ctx, `SELECT connection_json,revision,enabled FROM k8s_connections WHERE id=?`, id).Scan(&body, &c.Revision, &c.Enabled)
	if e != nil {
		return c, rowError(e)
	}
	var saved Connection
	if json.Unmarshal([]byte(body), &saved) != nil || saved.ID != id || saved.Revision != c.Revision || saved.Enabled != c.Enabled {
		return c, ErrInvalid
	}
	return saved, nil
}
func (s *Service) GetConnection(ctx context.Context, id string) (Connection, error) {
	return s.connection(ctx, configstate.Queryer(ctx, s.db), id)
}
func (s *Service) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, e := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT connection_json FROM k8s_connections ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		var body string
		var c Connection
		if e = rows.Scan(&body); e != nil {
			return nil, e
		}
		if json.Unmarshal([]byte(body), &c) != nil {
			return nil, ErrInvalid
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func validateConnection(in ConnectionInput) error {
	if !cleanText(in.Name, 120) || !IsProtectedCredentialID(in.CredentialID) || !validID(in.CredentialID) || len(in.Executors) < 1 || len(in.Executors) > 32 {
		return ErrInvalid
	}
	if e := validateProfile(in.Profile); e != nil {
		return e
	}
	seen := map[Executor]bool{}
	for _, v := range in.Executors {
		if !validExecutor(v) || seen[v] {
			return ErrInvalid
		}
		seen[v] = true
	}
	return nil
}
func (s *Service) SaveConnection(ctx context.Context, id string, expected int64, in ConnectionInput) (Connection, error) {
	var out Connection
	if e := validateConnection(in); e != nil {
		return out, e
	}
	create := id == ""
	if create {
		if expected != 0 {
			return out, ErrInvalid
		}
		id = "kconn_" + uuid.NewString()
	} else if !validID(id) || !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		cred, _, e := s.credential(ctx, q, in.CredentialID)
		if e != nil {
			return e
		}
		if !cred.Enabled {
			return ErrDenied
		}
		now := s.now().UnixMilli()
		created := now
		if !create {
			old, e := s.connection(ctx, q, id)
			if e != nil {
				return e
			}
			if old.Revision != expected {
				return ErrConflict
			}
			created = old.CreatedAt
		}
		for _, v := range in.Executors {
			if v.Kind == "ssh" {
				var count int
				if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE id=?`, v.ServerID).Scan(&count); e != nil {
					return e
				}
				if count != 1 {
					return ErrNotFound
				}
			}
		}
		out = Connection{ID: id, Revision: expected + 1, ConnectionInput: in, CredentialRevision: cred.Revision, Context: cred.Context, Server: cred.Server, ClusterKey: cred.ClusterKey, ProfileFingerprint: fingerprint(in.Profile), CreatedAt: created, UpdatedAt: now}
		b, _ := json.Marshal(out)
		if create {
			_, e = q.ExecContext(ctx, `INSERT INTO k8s_connections(id,credential_id,revision,enabled,connection_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, in.CredentialID, out.Revision, in.Enabled, string(b), created, now)
			return e
		}
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_connections SET credential_id=?,revision=?,enabled=?,connection_json=?,updated_at=? WHERE id=? AND revision=?`, in.CredentialID, out.Revision, in.Enabled, string(b), now, id, expected))
	})
	return out, e
}
func (s *Service) DeleteConnection(ctx context.Context, id string, expected int64) error {
	if !validRevision(expected) {
		return ErrInvalid
	}
	return s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		c, e := s.connection(ctx, q, id)
		if e != nil {
			return e
		}
		if c.Revision != expected {
			return ErrConflict
		}
		var refs int
		if e = q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM k8s_deploy_grants WHERE connection_id=?)+(SELECT COUNT(*) FROM k8s_operations WHERE connection_id=?)`, id, id).Scan(&refs); e != nil {
			return e
		}
		if refs > 0 {
			return ErrInUse
		}
		return requireOne(q.ExecContext(ctx, `DELETE FROM k8s_connections WHERE id=? AND revision=?`, id, expected))
	})
}
func grantScope(connection string, in GrantInput) string {
	return fingerprint([]any{connection, in.ProjectID, in.Environment, in.Namespace, in.Deployment, in.Executor})
}
func validGrant(in GrantInput) bool {
	if !validID(in.ProjectID) || !cleanText(in.Environment, 128) || !validName(in.Namespace, 63) || !validDeploymentName(in.Deployment) || !validExecutor(in.Executor) || len(in.Containers) == 0 || len(in.Containers) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range in.Containers {
		if !validName(v, 63) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func (s *Service) grant(ctx context.Context, q dbtx, id string) (Grant, error) {
	var g Grant
	var body string
	e := q.QueryRowContext(ctx, `SELECT grant_json,revision,enabled FROM k8s_deploy_grants WHERE id=?`, id).Scan(&body, &g.Revision, &g.Enabled)
	if e != nil {
		return g, rowError(e)
	}
	var saved Grant
	if json.Unmarshal([]byte(body), &saved) != nil || saved.ID != id || saved.Revision != g.Revision || saved.Enabled != g.Enabled {
		return g, ErrInvalid
	}
	return saved, nil
}
func (s *Service) GetGrant(ctx context.Context, id string) (Grant, error) {
	return s.grant(ctx, configstate.Queryer(ctx, s.db), id)
}
func (s *Service) ListGrants(ctx context.Context, connectionID string) ([]Grant, error) {
	rows, e := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT grant_json FROM k8s_deploy_grants WHERE connection_id=? ORDER BY created_at,id`, connectionID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var body string
		var g Grant
		if e = rows.Scan(&body); e != nil {
			return nil, e
		}
		if json.Unmarshal([]byte(body), &g) != nil {
			return nil, ErrInvalid
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Service) SaveGrant(ctx context.Context, connectionID, id string, expected int64, in GrantInput) (Grant, error) {
	var out Grant
	if !validGrant(in) {
		return out, ErrInvalid
	}
	create := id == ""
	if create {
		if expected != 0 {
			return out, ErrInvalid
		}
		id = "kgrant_" + uuid.NewString()
	} else if !validID(id) || !validRevision(expected) {
		return out, ErrInvalid
	}
	e := s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		c, e := s.connection(ctx, q, connectionID)
		if e != nil {
			return e
		}
		allowed := false
		for _, x := range c.Executors {
			allowed = allowed || x == in.Executor
		}
		if !allowed {
			return ErrDenied
		}
		var count int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, in.ProjectID).Scan(&count); e != nil {
			return e
		}
		if count != 1 {
			return ErrNotFound
		}
		now := s.now().UnixMilli()
		created := now
		if !create {
			old, e := s.grant(ctx, q, id)
			if e != nil {
				return e
			}
			if old.Revision != expected || old.ConnectionID != connectionID {
				return ErrConflict
			}
			created = old.CreatedAt
		}
		scope := grantScope(connectionID, in)
		var existing string
		e = q.QueryRowContext(ctx, `SELECT id FROM k8s_deploy_grants WHERE scope_key=?`, scope).Scan(&existing)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if existing != "" && existing != id {
			return ErrConflict
		}
		approval := true
		if in.RequireApproval != nil {
			approval = *in.RequireApproval
		}
		out = Grant{ID: id, ConnectionID: connectionID, Revision: expected + 1, ProjectID: in.ProjectID, Environment: in.Environment, Namespace: in.Namespace, Deployment: in.Deployment, Containers: append([]string(nil), in.Containers...), Executor: in.Executor, RequireApproval: approval, Enabled: in.Enabled, CreatedAt: created, UpdatedAt: now}
		body, _ := json.Marshal(out)
		if create {
			_, e = q.ExecContext(ctx, `INSERT INTO k8s_deploy_grants(id,connection_id,scope_key,project_id,revision,enabled,grant_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, connectionID, scope, in.ProjectID, out.Revision, in.Enabled, string(body), created, now)
			return e
		}
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_deploy_grants SET scope_key=?,project_id=?,revision=?,enabled=?,grant_json=?,updated_at=? WHERE id=? AND revision=?`, scope, in.ProjectID, out.Revision, in.Enabled, string(body), now, id, expected))
	})
	return out, e
}
func (s *Service) DeleteGrant(ctx context.Context, id string, expected int64) error {
	if !validRevision(expected) {
		return ErrInvalid
	}
	return s.configWrite(ctx, func(ctx context.Context, q dbtx) error {
		g, e := s.grant(ctx, q, id)
		if e != nil {
			return e
		}
		if g.Revision != expected {
			return ErrConflict
		}
		return requireOne(q.ExecContext(ctx, `DELETE FROM k8s_deploy_grants WHERE id=? AND revision=?`, id, expected))
	})
}
func (s *Service) authorize(ctx context.Context, q dbtx, t Target) (Authorization, error) {
	var a Authorization
	if e := validateTarget(t); e != nil {
		return a, e
	}
	c, e := s.connection(ctx, q, t.ConnectionID)
	if e != nil {
		return a, e
	}
	if !c.Enabled || c.CredentialID != t.CredentialID || c.Context != t.Context {
		return a, ErrDenied
	}
	cred, _, e := s.credential(ctx, q, t.CredentialID)
	if e != nil {
		return a, e
	}
	if !cred.Enabled || cred.ClusterKey != c.ClusterKey || cred.Context != c.Context {
		return a, ErrDenied
	}
	allowed := false
	for _, x := range c.Executors {
		allowed = allowed || x == t.Executor
	}
	if !allowed {
		return a, ErrDenied
	}
	in := GrantInput{ProjectID: t.ProjectID, Environment: t.Environment, Namespace: t.Namespace, Deployment: t.Deployment, Executor: t.Executor}
	scope := grantScope(c.ID, in)
	var gid string
	e = q.QueryRowContext(ctx, `SELECT id FROM k8s_deploy_grants WHERE scope_key=?`, scope).Scan(&gid)
	if errors.Is(e, sql.ErrNoRows) {
		return a, ErrDenied
	}
	if e != nil {
		return a, e
	}
	g, e := s.grant(ctx, q, gid)
	if e != nil {
		return a, e
	}
	allowed = false
	for _, v := range g.Containers {
		allowed = allowed || v == t.Container
	}
	if !g.Enabled || !allowed {
		return a, ErrDenied
	}
	a = Authorization{Target: t, ConnectionRevision: c.Revision, CredentialRevision: cred.Revision, GrantID: g.ID, GrantRevision: g.Revision, RequireApproval: g.RequireApproval, ClusterKey: c.ClusterKey, Profile: c.Profile, ProfileFingerprint: c.ProfileFingerprint}
	a.Fingerprint = fingerprint(a)
	return a, nil
}

// Authorize freezes exact current policy without opening credentials or contacting a cluster.
func (s *Service) Authorize(ctx context.Context, t Target) (Authorization, error) {
	var a Authorization
	e := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q dbtx) error { var e error; a, e = s.authorize(ctx, q, t); return e })
	return a, e
}
func (s *Service) validateAuthorization(ctx context.Context, q dbtx, a Authorization) error {
	current, e := s.authorize(ctx, q, a.Target)
	if e != nil {
		return e
	}
	if current.Fingerprint != a.Fingerprint || fingerprintWithoutAuthHash(a) != a.Fingerprint {
		return ErrConflict
	}
	return nil
}
func fingerprintWithoutAuthHash(a Authorization) string { a.Fingerprint = ""; return fingerprint(a) }

// ValidateAuthorization must be called again at mutating admission. It rejects
// grants, trust/profile or credential revisions changed while approval waited.
func (s *Service) ValidateAuthorization(ctx context.Context, a Authorization) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error { return s.validateAuthorization(ctx, q, a) })
}
func validRuntimeProof(p RuntimeProof, a Authorization) bool {
	return validID(p.OperationID) && validID(p.OwnerEpoch) && cleanText(p.EngineID, 256) && imageID.MatchString(p.ContainerID) && p.ProfileFingerprint == a.ProfileFingerprint && p.AuthorizationFingerprint == a.Fingerprint && p.Linux && p.PrivateTmpfs && p.SwapProtected && p.CoreDisabled && p.NonRoot && p.ReadOnlyRoot && p.BootstrapVerified && p.ClientVersionVerified && (a.Target.Executor.Kind == "local" || p.HostIdentityVerified)
}

// UseCredential checks a persisted operation and exact runtime lease as well as
// current authorization before decrypting. The buffer is wiped on return; no
// generic reveal method exists. A caller cannot use a proof from another lease.
func (s *Service) UseCredential(ctx context.Context, a Authorization, p RuntimeProof, consume SecretConsumer) error {
	if consume == nil || !validRuntimeProof(p, a) {
		return ErrRuntimeUnverified
	}
	var data []byte
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		if e := s.validateAuthorization(ctx, q, a); e != nil {
			return e
		}
		op, e := s.operation(ctx, q, p.OperationID)
		if e != nil {
			return e
		}
		if op.OwnerEpoch != p.OwnerEpoch || op.OwnerFenced || op.Retired || op.Intent.Authorization.Fingerprint != a.Fingerprint {
			return ErrOwner
		}
		if op.Business != NotDispatched {
			return ErrDenied
		}
		if op.Intent.Authorization.RequireApproval && op.ApprovedHash != op.IntentHash {
			return ErrDenied
		}
		lease, e := s.runtimeLease(ctx, q, p.OperationID)
		if e != nil {
			return e
		}
		if lease.Purpose != "writer" || lease.CredentialInjected || lease.OwnerEpoch != p.OwnerEpoch || lease.Cleanup != CleanupPending || lease.EngineID != p.EngineID || lease.ContainerID != p.ContainerID || lease.Proof == nil || *lease.Proof != p {
			return ErrRuntimeUnverified
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
		lease.CredentialInjected = true
		return s.saveRuntime(ctx, q, &lease, lease.Revision)
	})
	if e != nil {
		clear(data)
		return e
	}
	defer clear(data)
	return consume(ctx, data)
}
func sanitizeReason(reason string) string {
	if !identifier.MatchString(reason) || strings.Contains(reason, "secret") {
		return "operation_failed"
	}
	return reason
}

// AuthorizeConfigured resolves the SSH host identity from the server-owned
// connection allowlist. A job selects only kind/serverId and cannot supply or
// replace a host-key pin. Ambiguous allowlist entries fail closed.
func (s *Service) AuthorizeConfigured(ctx context.Context, t Target) (Authorization, error) {
	var out Authorization
	if t.Executor.HostKeySHA256 != "" {
		return out, ErrInvalid
	}
	e := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q dbtx) error {
		c, e := s.connection(ctx, q, t.ConnectionID)
		if e != nil {
			return e
		}
		matches := 0
		for _, x := range c.Executors {
			if x.Kind == t.Executor.Kind && x.ServerID == t.Executor.ServerID {
				t.Executor = x
				matches++
			}
		}
		if matches != 1 {
			return ErrDenied
		}
		out, e = s.authorize(ctx, q, t)
		return e
	})
	return out, e
}
