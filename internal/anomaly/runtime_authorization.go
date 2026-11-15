package anomaly

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

type samplingTarget struct {
	ID, Host, User, Credential, Binding string
	Port                                int
	Ciphertext                          []byte
}

func (s *service) samplingSource(ctx context.Context, q configstate.DBTX, id string) (*Rule, []samplingTarget, string, error) {
	rule := &Rule{ID: id}
	var server sql.NullString
	var enabled, ai int
	err := q.QueryRowContext(ctx, `SELECT metric,operator,threshold,server_id,enabled,ai_analysis_enabled FROM anomaly_rules WHERE id=?`, id).Scan(&rule.Metric, &rule.Operator, &rule.Threshold, &server, &enabled, &ai)
	if err != nil {
		return nil, nil, "", err
	}
	if enabled != 0 && enabled != 1 || ai != 0 && ai != 1 {
		return nil, nil, "", tenancy.ErrConflict
	}
	if server.Valid && server.String != "" {
		rule.ServerID = &server.String
	}
	rule.Enabled = enabled == 1
	rule.AIAnalysisEnabled = ai == 1
	query := `SELECT s.id,s.host,s.port,s.` + store.Identifier(store.DialectOf(s.db), "user") + `,s.credential_id,COALESCE(s.execution_binding_json,''),c.ciphertext FROM servers s JOIN credentials c ON c.id=s.credential_id`
	args := []any{}
	if rule.ServerID != nil {
		query += ` WHERE s.id=?`
		args = append(args, *rule.ServerID)
	}
	query += ` ORDER BY s.id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, "", err
	}
	targets := []samplingTarget{}
	for rows.Next() {
		var target samplingTarget
		if err = rows.Scan(&target.ID, &target.Host, &target.Port, &target.User, &target.Credential, &target.Binding, &target.Ciphertext); err != nil {
			rows.Close()
			return nil, nil, "", err
		}
		targets = append(targets, target)
		if len(targets) > 64 {
			rows.Close()
			return nil, nil, "", tenancy.ErrInvalidInput
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, "", err
	}
	// Timestamp presentation is excluded. The encrypted credential and dedicated
	// execution assignment are included without decrypting or exposing either.
	body, err := json.Marshal(struct {
		ID, Metric, Operator string
		Threshold            float64
		Server               *string
		Enabled, AI          bool
		Targets              []samplingTarget
	}{rule.ID, rule.Metric, rule.Operator, rule.Threshold, rule.ServerID, rule.Enabled, rule.AIAnalysisEnabled, targets})
	if err != nil {
		return nil, nil, "", err
	}
	sum := sha256.Sum256(body)
	return rule, targets, hex.EncodeToString(sum[:]), nil
}
func (s *service) approveSampling(ctx context.Context, rule *Rule) error {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return err
	}
	rule.SamplingAuthorization = "required"
	if !rule.Enabled {
		rule.SamplingAuthorization = ""
		return tenancy.RevokeAutomation(ctx, s.db, "anomaly_rule", rule.ID)
	}
	_, targets, digest, err := s.samplingSource(ctx, configstate.Queryer(ctx, s.db), rule.ID)
	if err != nil {
		return err
	}
	permissions := []tenancy.AutomationPermission{}
	if rule.ServerID == nil {
		permissions = append(permissions, tenancy.AutomationPermission{Action: "system.manage", ResourceID: "instance"})
	}
	for _, target := range targets {
		actions := []struct{ action, kind, id string }{{"anomaly.manage", "server", target.ID}, {"server.use", "server", target.ID}, {"credential.use", "credential", target.Credential}}
		if rule.AIAnalysisEnabled {
			actions = append(actions, struct{ action, kind, id string }{"anomaly.analyze", "server", target.ID})
		}
		for _, action := range actions {
			if err = tenancy.Check(ctx, s.db, action.action, action.kind, action.id); err != nil {
				if errors.Is(err, tenancy.ErrForbidden) {
					return tenancy.RevokeAutomation(ctx, s.db, "anomaly_rule", rule.ID)
				}
				return err
			}
			permissions = append(permissions, tenancy.AutomationPermission{Action: action.action, ResourceID: action.id})
		}
	}
	if len(targets) == 0 {
		return tenancy.RevokeAutomation(ctx, s.db, "anomaly_rule", rule.ID)
	}
	err = tenancy.ApproveAutomation(ctx, s.db, "anomaly_rule", rule.ID, "", digest, permissions)
	if err == nil {
		rule.SamplingAuthorization = "ready"
	}
	return err
}
func (s *service) samplingContext(ctx context.Context, ruleID string) (context.Context, []string, error) {
	rule, targets, digest, err := s.samplingSource(ctx, configstate.Queryer(ctx, s.db), ruleID)
	if err != nil || !rule.Enabled {
		return nil, nil, tenancy.ErrForbidden
	}
	job, err := tenancy.AutomationContext(ctx, s.db, "anomaly_rule", ruleID, digest, func(c context.Context, q configstate.DBTX) (string, error) {
		r, _, d, e := s.samplingSource(c, q, ruleID)
		if e != nil || !r.Enabled {
			return "", tenancy.ErrForbidden
		}
		return d, nil
	})
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	return job, ids, nil
}

// CheckScheduled never borrows a bootstrap identity. Each saved rule carries
// its own approval and exact source snapshot. Missing approval is a reported
// authorization failure, not a successful empty/healthy sample.
func (s *service) CheckScheduled(ctx context.Context) ([]*Alert, error) {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if !on {
		return s.Check(ctx)
	}
	if tenancy.HasAuthorizationContext(ctx) {
		return nil, tenancy.ErrForbidden
	}
	select {
	case s.checkGate <- struct{}{}:
		defer func() { <-s.checkGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.analysisContext.Done():
		return nil, s.analysisContext.Err()
	}
	s.analysisMu.Lock()
	if s.closed {
		s.analysisMu.Unlock()
		return nil, context.Canceled
	}
	s.checkWG.Add(1)
	s.analysisMu.Unlock()
	defer s.checkWG.Done()
	local, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.analysisContext, cancel)
	defer func() { stop(); cancel() }()
	ctx = local

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM anomaly_rules WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []*Alert{}
	denied := false
	for _, id := range ids {
		job, targets, e := s.samplingContext(ctx, id)
		if e != nil {
			denied = true
			continue
		}
		produced, e := s.checkAuthorizedRule(job, id, targets)
		if e != nil {
			denied = true
			continue
		}
		out = append(out, produced...)
	}
	if denied {
		return out, fmt.Errorf("anomaly: one or more rules require current sampling authorization")
	}
	return out, nil
}
func (s *service) checkAuthorizedRule(ctx context.Context, id string, targets []string) ([]*Alert, error) {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return nil, err
	}
	rule, err := s.getRule(ctx, id)
	if err != nil {
		return nil, err
	}
	if !rule.Enabled || len(targets) == 0 {
		return nil, tenancy.ErrForbidden
	}
	if s.collector == nil {
		return []*Alert{}, nil
	}
	snapshots, err := s.collector.Collect(ctx, targets)
	if err != nil {
		return nil, err
	}
	permitted := map[string]bool{}
	for _, target := range targets {
		permitted[target] = true
	}
	out := []*Alert{}
	for _, snap := range snapshots {
		if !permitted[snap.ServerID] || !snap.Available {
			continue
		}
		if err = tenancy.VerifyExecution(ctx); err != nil {
			return nil, err
		}
		if err = tenancy.Check(ctx, s.db, "anomaly.manage", "server", snap.ServerID); err != nil {
			return nil, err
		}
		value := snap.percentFor(rule.Metric)
		if value == nil || !evaluate(rule.Operator, *value, rule.Threshold) {
			continue
		}
		recent, e := s.hasRecentAlert(ctx, snap.ServerID, rule)
		if e != nil {
			return nil, e
		}
		if recent {
			continue
		}
		var alert *Alert
		err = configstate.WithWriteTxContext(ctx, s.db, func(c context.Context, _ configstate.DBTX) error {
			if e := tenancy.VerifyExecution(c); e != nil {
				return e
			}
			var e error
			alert, e = s.recordAlert(c, rule, snap, *value)
			return e
		})
		if err != nil {
			return nil, err
		}
		out = append(out, alert)
		s.dispatchAlert(ctx, *alert, cloneMetrics(snap))
	}
	return out, nil
}

// Sampling status discloses only readiness, never the approving user's identity.
func (s *service) samplingStatus(ctx context.Context, rule *Rule) {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on || !rule.Enabled {
		return
	}
	rule.SamplingAuthorization = "required"
	base, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	if _, _, err = s.samplingContext(base, rule.ID); err == nil {
		rule.SamplingAuthorization = "ready"
	}
}
