package target

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"reflect"
	"time"
)

// MetricsAutomationService is controller-only; never accept a caller-selected
// source ID or callback from a browser. Its context grants no arbitrary Exec.
type MetricsAutomationService interface {
	CollectApproved(context.Context, func(context.Context, string, Service) error) error
	MetricsAuthorizationState(context.Context, string) (string, error)
}

func metricsDigest(ctx context.Context, q configstate.DBTX, id string) (string, error) {
	var host, credential string
	var port int
	var binding sql.NullString
	var cipher []byte
	// Runtime resource fingerprints additionally bind every server field.
	row := q.QueryRowContext(ctx, `SELECT s.host,s.port,s.credential_id,s.execution_binding_json,c.ciphertext FROM servers s JOIN credentials c ON c.id=s.credential_id WHERE s.id=?`, id)
	if e := row.Scan(&host, &port, &credential, &binding, &cipher); e != nil {
		return "", e
	}
	// Credential and server raw row fingerprints are also persisted in the cap.
	body, _ := json.Marshal([]any{id, host, port, credential, binding.String, hex.EncodeToString(cipher)})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
func (s *service) metricsCurrent(id string) func(context.Context, configstate.DBTX) (string, error) {
	return func(ctx context.Context, q configstate.DBTX) (string, error) { return metricsDigest(ctx, q, id) }
}
func (s *service) authorizeMetrics(ctx context.Context, snapshot ConnectionSnapshot) error {
	if e := tenancy.VerifyExecution(ctx); e != nil {
		return e
	}
	if e := tenancy.Check(ctx, s.db, "server.inspect", "server", snapshot.Server.ID); e != nil {
		return e
	}
	if e := vault.AuthorizeUse(ctx, s.vault, snapshot.Server.CredentialID); e != nil {
		return e
	}
	current, e := s.Capture(ctx, snapshot.Server.ID)
	if e != nil {
		return e
	}
	if current.Fingerprint() != snapshot.Fingerprint() {
		return ErrTargetChanged
	}
	return s.checkExecutionIdentity(ctx, &current.Server)
}
func (s *service) approveMetrics(ctx context.Context, id string) error {
	on, e := tenancy.Enabled(ctx, s.db)
	if e != nil || !on {
		return e
	}
	snapshot, e := s.Capture(ctx, id)
	if e != nil {
		return e
	}
	if e = s.authorizeMetrics(ctx, snapshot); e != nil {
		if errors.Is(e, tenancy.ErrForbidden) || errors.Is(e, tenancy.ErrConflict) {
			return tenancy.RevokeAutomation(ctx, s.db, "server_metrics", id)
		}
		return e
	}
	digest, e := metricsDigest(ctx, configstate.Queryer(ctx, s.db), id)
	if e != nil {
		return e
	}
	return tenancy.ApproveAutomation(ctx, s.db, "server_metrics", id, "", digest, []tenancy.AutomationPermission{{Action: "server.inspect", ResourceID: id}, {Action: "credential.use", ResourceID: snapshot.Server.CredentialID}})
}
func (s *service) MetricsAuthorizationState(ctx context.Context, id string) (string, error) {
	if e := tenancy.Check(ctx, s.db, "server.inspect", "server", id); e != nil {
		return "", e
	}
	on, e := tenancy.Enabled(ctx, s.db)
	if e != nil || !on {
		return "", e
	}
	digest, e := metricsDigest(ctx, configstate.Queryer(ctx, s.db), id)
	if e != nil {
		return "", e
	}
	if tenancy.AutomationReady(ctx, s.db, "server_metrics", id, digest, s.metricsCurrent(id)) {
		return "ready", nil
	}
	return "required", nil
}
func (s *service) CollectApproved(ctx context.Context, consume func(context.Context, string, Service) error) error {
	if consume == nil || tenancy.HasAuthorizationContext(ctx) {
		return tenancy.ErrForbidden
	}
	on, e := tenancy.Enabled(ctx, s.db)
	if e != nil {
		return e
	}
	if !on {
		return tenancy.ErrForbidden
	}
	rows, e := s.db.QueryContext(ctx, `SELECT source_id FROM access_automation_authorizations WHERE source_kind='server_metrics' AND revoked_at IS NULL ORDER BY source_id`)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
		if len(ids) > 4096 {
			rows.Close()
			return tenancy.ErrInvalidInput
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var failed error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		digest, e := metricsDigest(ctx, s.db, id)
		if e != nil {
			continue
		}
		principal, e := tenancy.AutomationContext(ctx, s.db, "server_metrics", id, digest, s.metricsCurrent(id))
		if e != nil {
			continue
		}
		snapshot, e := s.Capture(principal, id)
		if e != nil {
			continue
		}
		if e = s.authorizeMetrics(principal, snapshot); e != nil {
			continue
		}
		bounded := &metricsTarget{Service: s, service: s, snapshot: snapshot}
		if e = consume(principal, id, bounded); e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Keep sampling other approved servers, but do not report a fully
			// successful batch after storage or sampling failures. No payloads.
			failed = errors.New("target: approved monitoring batch incomplete")
		}
	}
	return failed
}

type metricsTarget struct {
	Service
	service  *service
	snapshot ConnectionSnapshot
}

// Only these literal argv are allowed. No shell, user arguments, upload, terminal
// or generic execution capability is granted by the saved monitoring approval.
var metricsCommands = [][]string{{"cat", "/proc/loadavg"}, {"uptime"}, {"nproc"}, {"getconf", "_NPROCESSORS_ONLN"}, {"free", "-b"}, {"df", "-B1", "/"}, {"df", "-k", "/"}, {"dmidecode", "-t", "17"}}

func (m *metricsTarget) Exec(ctx context.Context, id string, argv []string) (*ExecResult, error) {
	allowed := false
	for _, command := range metricsCommands {
		if reflect.DeepEqual(argv, command) {
			allowed = true
			break
		}
	}
	if !allowed || id != m.snapshot.Server.ID {
		return nil, tenancy.ErrForbidden
	}
	if e := m.service.authorizeMetrics(ctx, m.snapshot); e != nil {
		return nil, e
	}
	runner, ok := m.service.dialer.(limitedSSHRunner)
	if !ok {
		return nil, ErrLimitedUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out *LimitedResult
	e := m.service.withPinnedCredential(bounded, m.snapshot, func(addr string, cfg SSHConfig) error {
		if e := m.service.authorizeMetrics(bounded, m.snapshot); e != nil {
			return e
		}
		var err error
		out, err = runner.RunLimited(bounded, addr, cfg, argv, ExecutionLimits{Bytes: 64 << 10, Lines: 1000})
		return err
	})
	if e != nil {
		return nil, e
	}
	if e = m.service.authorizeMetrics(ctx, m.snapshot); e != nil {
		return nil, e
	}
	if out == nil {
		return nil, ErrLimitedUnavailable
	}
	return &ExecResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode}, nil
}
