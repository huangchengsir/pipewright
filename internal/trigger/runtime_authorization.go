package trigger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func triggerDigest(ctx context.Context, q configstate.DBTX, project string) (string, error) {
	var token, events, mappings, policy, paths string
	var secret []byte
	if err := q.QueryRowContext(ctx, `SELECT webhook_token,webhook_secret_ciphertext,events_json,branch_mappings_json,unmatched_policy,path_filters_json FROM pipeline_triggers WHERE project_id=?`, project).Scan(&token, &secret, &events, &mappings, &policy, &paths); err != nil {
		return "", err
	}
	body, err := json.Marshal([]any{project, token, secret, events, mappings, policy, paths})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}
func (s *service) approveTrigger(ctx context.Context, project string, cfg *Config) error {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return err
	}
	if !cfg.Events.Push && !cfg.Events.Tag && !cfg.Events.PullRequest && !cfg.Events.Release {
		return tenancy.RevokeAutomation(ctx, s.db, "trigger", project)
	}
	digest, err := triggerDigest(ctx, configstate.Queryer(ctx, s.db), project)
	if err != nil {
		return err
	}
	return tenancy.ApproveProjectAutomation(ctx, s.db, "trigger", project, project, digest)
}
func triggerContext(ctx context.Context, db *sql.DB, project string) (context.Context, error) {
	on, err := tenancy.Enabled(ctx, db)
	if err != nil || !on {
		return ctx, err
	}
	digest, err := triggerDigest(ctx, configstate.Queryer(ctx, db), project)
	if err != nil {
		return nil, err
	}
	return tenancy.AutomationContext(ctx, db, "trigger", project, digest, func(c context.Context, q configstate.DBTX) (string, error) { return triggerDigest(c, q, project) })
}
