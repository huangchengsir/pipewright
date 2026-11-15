package anomaly

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

func (s *service) authorizeRuleServer(ctx context.Context, server *string, action string) error {
	if server == nil || strings.TrimSpace(*server) == "" {
		return tenancy.RequireInstanceAdministrator(ctx, s.db)
	}
	return tenancy.Check(ctx, s.db, action, "server", strings.TrimSpace(*server))
}
func ruleMutation[T any](ctx context.Context, s *service, fn func(context.Context) (T, error)) (T, error) {
	var result T
	err := configstate.WithWriteTxContext(ctx, s.db, func(c context.Context, _ configstate.DBTX) error {
		if e := tenancy.LockAuthorization(c, s.db); e != nil {
			return e
		}
		var e error
		result, e = fn(c)
		return e
	})
	return result, err
}

// normalizeRuleScope keeps the authorized identity identical to the stored identity.
func normalizeRuleScope(in CreateRuleInput) CreateRuleInput {
	if in.ServerID != nil {
		id := strings.TrimSpace(*in.ServerID)
		if id == "" {
			in.ServerID = nil
		} else {
			in.ServerID = &id
		}
	}
	return in
}
func (s *service) CreateRule(ctx context.Context, in CreateRuleInput) (*Rule, error) {
	in = normalizeRuleScope(in)
	return ruleMutation(ctx, s, func(c context.Context) (*Rule, error) {
		if e := s.authorizeRuleServer(c, in.ServerID, "anomaly.manage"); e != nil {
			return nil, e
		}
		if in.AIAnalysisEnabled != nil && *in.AIAnalysisEnabled {
			if e := s.authorizeRuleServer(c, in.ServerID, "anomaly.analyze"); e != nil {
				return nil, e
			}
		}
		rule, e := s.createRule(c, in)
		if e != nil {
			return nil, e
		}
		if e = s.approveSampling(c, rule); e != nil {
			return nil, e
		}
		return rule, nil
	})
}
func (s *service) UpdateRule(ctx context.Context, id string, in CreateRuleInput) (*Rule, error) {
	in = normalizeRuleScope(in)
	cancelAfterReturn := false
	result, err := ruleMutation(ctx, s, func(c context.Context) (*Rule, error) {
		old, e := s.getRule(c, id)
		if e != nil {
			return nil, e
		}
		if e = s.authorizeRuleServer(c, old.ServerID, "anomaly.read"); e != nil {
			if errors.Is(e, tenancy.ErrForbidden) {
				return nil, ErrNotFound
			}
			return nil, e
		}
		if e = s.authorizeRuleServer(c, old.ServerID, "anomaly.manage"); e != nil {
			return nil, e
		}
		if e = s.authorizeRuleServer(c, in.ServerID, "anomaly.manage"); e != nil {
			return nil, e
		}
		aiEnabled := old.AIAnalysisEnabled
		if in.AIAnalysisEnabled != nil {
			aiEnabled = *in.AIAnalysisEnabled
		}
		enabled := old.Enabled
		if !in.PreserveEnabled {
			enabled = in.Enabled
		}
		if aiEnabled && enabled {
			if e = s.authorizeRuleServer(c, in.ServerID, "anomaly.analyze"); e != nil {
				return nil, e
			}
		}
		if e = s.updateRule(c, id, in); e != nil {
			return nil, e
		}
		if !enabled {
			if e = tenancy.RevokeAutomation(c, s.db, "anomaly_rule", id); e != nil {
				return nil, e
			}
		} else if on, modeErr := tenancy.Enabled(c, s.db); modeErr != nil {
			return nil, modeErr
		} else if on {
			updated, _, _, e := s.samplingSource(c, configstate.Queryer(c, s.db), id)
			if e != nil {
				return nil, e
			}
			if e = s.approveSampling(c, updated); e != nil {
				return nil, e
			}
		}
		if !enabled || !aiEnabled {
			cancelAfterReturn = !configstate.AfterCommit(c, func() { s.cancelRuleAnalysis(id) })
		}
		old.Enabled = enabled
		old.AIAnalysisEnabled = aiEnabled
		old.ServerID = in.ServerID
		return old, nil
	})
	if err != nil {
		return nil, err
	}
	if cancelAfterReturn {
		s.cancelRuleAnalysis(id)
	}
	if err = s.authorizeRuleServer(ctx, result.ServerID, "anomaly.read"); err != nil {
		return nil, err
	}
	rule, e := s.getRule(ctx, id)
	if e != nil {
		return nil, e
	}
	s.samplingStatus(ctx, rule)
	return rule, nil
}
func (s *service) DeleteRule(ctx context.Context, id string) error {
	cancelAfterReturn := false
	_, err := ruleMutation(ctx, s, func(c context.Context) (struct{}, error) {
		old, e := s.getRule(c, id)
		if e != nil {
			return struct{}{}, e
		}
		if e = s.authorizeRuleServer(c, old.ServerID, "anomaly.read"); e != nil {
			if errors.Is(e, tenancy.ErrForbidden) {
				return struct{}{}, ErrNotFound
			}
			return struct{}{}, e
		}
		if e = s.authorizeRuleServer(c, old.ServerID, "anomaly.manage"); e != nil {
			return struct{}{}, e
		}
		if e = tenancy.RevokeAutomation(c, s.db, "anomaly_rule", id); e != nil {
			return struct{}{}, e
		}
		if e = s.deleteRule(c, id); e != nil {
			return struct{}{}, e
		}
		cancelAfterReturn = !configstate.AfterCommit(c, func() { s.cancelRuleAnalysis(id) })
		return struct{}{}, nil
	})
	if err == nil && cancelAfterReturn {
		s.cancelRuleAnalysis(id)
	}
	return err
}
