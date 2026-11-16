package promotion

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

type PlanStage struct {
	Name          string `json:"name"`
	Gated         bool   `json:"gated"`
	EnvironmentID string `json:"environmentId,omitempty"`
}
type Plan struct {
	Configured   bool        `json:"configured"`
	AtTop        bool        `json:"atTop"`
	Environments []PlanStage `json:"environments"`
	Next         *PlanStage  `json:"next,omitempty"`
}

// ReadPlan exposes target names and gates, never configuration variables or
// credentials. The next stage is calculated from persisted history before
// filtering so hidden stages cannot be skipped by a filtered client history.
func (s *Store) ReadPlan(ctx context.Context, runID string) (Plan, error) {
	out := Plan{Environments: []PlanStage{}}
	err := configstate.WithReadTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if e := tenancy.Check(tx, s.db, "run.read", "run", runID); e != nil {
			return e
		}
		var project string
		if e := q.QueryRowContext(tx, `SELECT project_id FROM pipeline_runs WHERE id=?`, runID).Scan(&project); e != nil {
			return ErrRunNotFound
		}
		chain, e := s.getChain(tx, project)
		if errors.Is(e, ErrChainNotConfigured) {
			return nil
		}
		if e != nil {
			return e
		}
		out.Configured = true
		current, e := s.currentEnvForRun(tx, runID, chain)
		if e != nil {
			return e
		}
		next, e := chain.nextTarget(current)
		if errors.Is(e, ErrAlreadyAtTop) {
			out.AtTop = true
		} else if e != nil {
			return e
		}
		on, e := tenancy.Enabled(tx, s.db)
		if e != nil {
			return e
		}
		for _, stage := range chain.Environments {
			if e = tenancy.CheckEnvironmentName(tx, s.db, "environment.read", project, stage.Name); e != nil {
				if errors.Is(e, tenancy.ErrForbidden) {
					continue
				}
				return e
			}
			v := PlanStage{Name: stage.Name, Gated: stage.Gated}
			if on {
				if e = q.QueryRowContext(tx, `SELECT id FROM access_environments WHERE project_id=? AND name=?`, project, stage.Name).Scan(&v.EnvironmentID); e != nil {
					return e
				}
			}
			out.Environments = append(out.Environments, v)
			if stage.Name == next {
				copy := v
				out.Next = &copy
			}
		}
		return nil
	})
	return out, err
}
