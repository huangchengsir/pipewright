package target

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net"
	"strings"
	"time"
)

// An assignment is authored by the instance administrator. Neither a team role
// nor an editable server address can create or widen this execution boundary.
type ExecutionAssignment struct {
	TeamID                string `json:"teamId"`
	HostKey               string `json:"hostKey"`
	ConnectionFingerprint string `json:"connectionFingerprint"`
	AssignedAt            int64  `json:"assignedAt"`
}
type ExecutionAssignmentService interface {
	GetExecutionAssignment(context.Context, string) (*ExecutionAssignment, error)
	AssignExecution(context.Context, string, string) (*ExecutionAssignment, error)
}

func identityKey(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + ":" + value))
	return hex.EncodeToString(sum[:])
}
func endpointIdentity(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		host = ip.String()
	}
	return identityKey("host", host)
}
func baseConnectionFingerprint(s ConnectionSnapshot) string {
	s.ExecutionHostKey = ""
	s.ExecutionBindingVersion = ""
	return s.Fingerprint()
}
func (s *service) readExecutionAssignment(ctx context.Context, id string) (*ExecutionAssignment, string, error) {
	var raw sql.NullString
	if err := configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT execution_binding_json FROM servers WHERE id=?`, id).Scan(&raw); err != nil {
		return nil, "", err
	}
	if !raw.Valid || raw.String == "" {
		return nil, "", nil
	}
	var assignment ExecutionAssignment
	if len(raw.String) > 4096 || json.Unmarshal([]byte(raw.String), &assignment) != nil || assignment.TeamID == "" || !validHostFingerprint(assignment.HostKey) || len(assignment.ConnectionFingerprint) != 64 {
		return nil, "", tenancy.ErrConflict
	}
	return &assignment, identityKey("binding", raw.String), nil
}
func (s *service) GetExecutionAssignment(ctx context.Context, id string) (*ExecutionAssignment, error) {
	principal, on, err := tenancy.CurrentPrincipal(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if !on || !principal.InstanceAdmin {
		return nil, tenancy.ErrForbidden
	}
	if err := tenancy.Check(ctx, s.db, "server.assign", "server", id); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	a, _, err := s.readExecutionAssignment(ctx, id)
	return a, err
}
func (s *service) AssignExecution(ctx context.Context, id, hostKey string) (result *ExecutionAssignment, err error) {
	if hostKey != "" && !validHostFingerprint(hostKey) {
		return nil, ErrHostIdentity
	}
	err = s.mutate(ctx, func(txctx context.Context) error {
		principal, on, e := tenancy.CurrentPrincipal(txctx, s.db)
		if e != nil {
			return e
		}
		if !on || !principal.InstanceAdmin {
			return tenancy.ErrForbidden
		}
		if e = tenancy.Check(txctx, s.db, "server.assign", "server", id); e != nil {
			return e
		}
		snapshot, e := s.Capture(txctx, id)
		if e != nil {
			return e
		}
		if hostKey == "" {
			_, e = configstate.Queryer(txctx, s.db).ExecContext(txctx, `UPDATE servers SET execution_binding_json=NULL WHERE id=?`, id)
			if e != nil {
				return e
			}
			return audit.AppendLocalTx(txctx, configstate.Queryer(txctx, s.db), mask.NewMasker(), audit.Entry{Action: "execution_node_revoke", TargetType: "server", TargetID: id})
		}
		if strings.EqualFold(strings.TrimSpace(snapshot.Server.User), "root") {
			return tenancy.ErrForbidden
		}
		q := configstate.Queryer(txctx, s.db)
		var team, credentialTeam string
		if e = q.QueryRowContext(txctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='server' AND resource_id=?`, id).Scan(&team); e != nil {
			return e
		}
		if e = q.QueryRowContext(txctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='credential' AND resource_id=?`, snapshot.Server.CredentialID).Scan(&credentialTeam); e != nil {
			return e
		}
		if team == "" || credentialTeam != team {
			return tenancy.ErrForbidden
		}
		for _, key := range []string{identityKey("ssh-key", hostKey), endpointIdentity(snapshot.Server.Host)} {
			var existing string
			e = q.QueryRowContext(txctx, `SELECT tenant_id FROM access_execution_nodes WHERE identity_key=?`, key).Scan(&existing)
			if errors.Is(e, sql.ErrNoRows) {
				_, e = q.ExecContext(txctx, `INSERT INTO access_execution_nodes(identity_key,tenant_id,created_at) VALUES(?,?,?)`, key, team, time.Now().Unix())
			} else if e == nil && existing != team {
				return tenancy.ErrForbidden
			}
			if e != nil {
				return e
			}
		}
		result = &ExecutionAssignment{TeamID: team, HostKey: hostKey, ConnectionFingerprint: baseConnectionFingerprint(snapshot), AssignedAt: time.Now().Unix()}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		_, e = q.ExecContext(txctx, `UPDATE servers SET execution_binding_json=? WHERE id=?`, string(raw), id)
		if e != nil {
			return e
		}
		if e = s.approveMetrics(txctx, id); e != nil {
			return e
		}
		return audit.AppendLocalTx(txctx, q, mask.NewMasker(), audit.Entry{Action: "execution_node_assign", TargetType: "server", TargetID: id})
	})
	return
}
func (s *service) executionHostKey(ctx context.Context, server *Server) (string, error) {
	principal, on, err := tenancy.CurrentPrincipal(ctx, s.db)
	if err != nil || !on {
		return "", err
	}
	assignment, _, err := s.readExecutionAssignment(ctx, server.ID)
	if err != nil {
		return "", err
	}
	if assignment == nil {
		if principal.InstanceAdmin {
			return "", nil
		}
		return "", tenancy.ErrForbidden
	}
	snapshot, err := s.Capture(ctx, server.ID)
	if err != nil {
		return "", err
	}
	if snapshot.Server.Host != server.Host || snapshot.Server.Port != server.Port || snapshot.Server.User != server.User || snapshot.Server.CredentialID != server.CredentialID {
		return "", ErrTargetChanged
	}
	if assignment.ConnectionFingerprint != baseConnectionFingerprint(snapshot) {
		return "", tenancy.ErrConflict
	}
	var team string
	q := configstate.Queryer(ctx, s.db)
	if err = q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='server' AND resource_id=?`, server.ID).Scan(&team); err != nil {
		return "", err
	}
	if assignment.TeamID != team || (!principal.InstanceAdmin && principal.TenantID != team) {
		return "", tenancy.ErrForbidden
	}
	for _, key := range []string{identityKey("ssh-key", assignment.HostKey), endpointIdentity(server.Host)} {
		var owner string
		if err = q.QueryRowContext(ctx, `SELECT tenant_id FROM access_execution_nodes WHERE identity_key=?`, key).Scan(&owner); err != nil {
			return "", err
		}
		if owner != team {
			return "", tenancy.ErrForbidden
		}
	}
	return assignment.HostKey, nil
}
