package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func (s *service) targetDigest(ctx context.Context, id string) (string, string, error) {
	q := configstate.Queryer(ctx, s.db)
	var upstream, downstream, branch string
	var enabled int
	if err := q.QueryRowContext(ctx, `SELECT project_id,downstream_project_id,branch,enabled FROM project_chain_targets WHERE id=?`, id).Scan(&upstream, &downstream, &branch, &enabled); err != nil {
		return "", "", err
	}
	if enabled != 1 {
		return "", "", tenancy.ErrForbidden
	}
	sourceRevision, err := tenancy.ProjectExecutionDigest(ctx, q, upstream)
	if err != nil {
		return "", "", err
	}
	targetRevision, err := tenancy.ProjectExecutionDigest(ctx, q, downstream)
	if err != nil {
		return "", "", err
	}
	body, err := json.Marshal([]any{id, upstream, downstream, branch, sourceRevision, targetRevision})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), downstream, nil
}
func (s *service) approveTargets(ctx context.Context, upstream string) error {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return err
	}
	q := configstate.Queryer(ctx, s.db)
	rows, err := q.QueryContext(ctx, `SELECT id,downstream_project_id FROM project_chain_targets WHERE project_id=? AND enabled=1`, upstream)
	if err != nil {
		return err
	}
	type item struct{ id, project string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.project); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range items {
		if _, err = configstate.LockProject(ctx, q, s.db, v.project); err != nil {
			return err
		}
		digest, _, e := s.targetDigest(ctx, v.id)
		if e != nil {
			return e
		}
		if e = tenancy.ApproveProjectAutomation(ctx, s.db, "chain", v.id, v.project, digest, upstream); e != nil {
			return e
		}
	}
	return nil
}

// noAuthorityContext carries cancellation/deadline, never an upstream run's
// identity. The source was verified separately before selecting this approval.
type noAuthorityContext struct{ context.Context }

func (noAuthorityContext) Value(any) any { return nil }
func (s *service) ChainContext(ctx context.Context, runID, upstream string, target Target) (context.Context, error) {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return ctx, err
	}
	if err = tenancy.Check(ctx, s.db, "run.read", "run", runID); err != nil {
		return nil, err
	}
	var project string
	if err = s.db.QueryRowContext(ctx, `SELECT project_id FROM pipeline_runs WHERE id=?`, runID).Scan(&project); err != nil || project != upstream {
		return nil, tenancy.ErrForbidden
	}
	var id string
	if err = s.db.QueryRowContext(ctx, `SELECT id FROM project_chain_targets WHERE project_id=? AND downstream_project_id=? AND branch=? AND enabled=1`, upstream, target.DownstreamProjectID, target.Branch).Scan(&id); err != nil {
		return nil, tenancy.ErrForbidden
	}
	digest, _, err := s.targetDigest(ctx, id)
	if err != nil {
		return nil, err
	}
	return tenancy.AutomationContext(noAuthorityContext{ctx}, s.db, "chain", id, digest, func(c context.Context, q configstate.DBTX) (string, error) {
		d, _, e := s.targetDigest(configstate.WithTxContext(c, s.db, q), id)
		return d, e
	})
}
