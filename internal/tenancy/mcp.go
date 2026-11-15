package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
	"time"
)

type mcpContextKey struct{}
type mcpContext struct {
	db      *sql.DB
	tokenID string
}
type mcpAuthorization struct{ db *sql.DB }

func MCPAuthorization(db *sql.DB) auth.MCPAuthorization { return &mcpAuthorization{db: db} }
func (m *mcpAuthorization) Admission(ctx context.Context) context.Context {
	return configstate.WithWriteAdmission(ctx, LockAuthorization)
}
func (m *mcpAuthorization) Create(ctx context.Context, q configstate.DBTX, t *auth.MCPToken) error {
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	ctx = configstate.WithTxContext(ctx, m.db, q)
	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok {
		return ErrForbidden
	}
	authority, err := loadAuthority(ctx, q, request.actor, request.teamID)
	if err != nil {
		return err
	}
	for _, id := range t.ProjectIDs {
		if err = Check(ctx, m.db, "mcp.issue", "project", id); err != nil {
			return err
		}
		if t.CanTrigger {
			yes, e := authority.Service.Allowed(authority.Principal, "run.start", authz.Resource{Kind: "run", ID: "new-run", TenantID: request.teamID, ProjectID: id, OwnerID: request.actor.UserID})
			if e != nil || !yes {
				return ErrForbidden
			}
		}
	}
	for _, grant := range t.ConfigGrants {
		for _, section := range grant.Sections {
			action := "pipeline.read"
			if section == "project" {
				action = "project.read"
			}
			if err = Check(ctx, m.db, action, "project", grant.ProjectID); err != nil {
				return err
			}
			if grant.Propose || grant.Apply {
				action = "pipeline.edit"
				if section == "project" {
					action = "project.edit"
				}
				if err = Check(ctx, m.db, action, "project", grant.ProjectID); err != nil {
					return err
				}
			}
		}
	}
	t.OwnerUserID = request.actor.UserID
	t.OwnerTeamID = request.teamID
	t.OwnerModeEpoch = request.actor.ModeEpoch
	t.OwnerGeneration = request.actor.SecurityGeneration
	return nil
}
func (m *mcpAuthorization) Manage(ctx context.Context, q configstate.DBTX, t *auth.MCPToken) error {
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok {
		return ErrForbidden
	}
	_, err = verifyActor(ctx, q, request.actor, false)
	if err != nil {
		return err
	}
	if t.OwnerUserID != request.actor.UserID || t.OwnerTeamID != request.teamID {
		return ErrForbidden
	}
	return nil
}
func (m *mcpAuthorization) ListFilter(ctx context.Context, q configstate.DBTX) (string, []any, error) {
	on, err := modeEnabled(ctx, q)
	if err != nil {
		return "", nil, err
	}
	if !on {
		return "1=1", nil, nil
	}
	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok {
		return "", nil, ErrForbidden
	}
	if _, err = loadAuthority(ctx, q, request.actor, request.teamID); err != nil {
		return "", nil, err
	}
	return "owner_user_id=? AND owner_team_id=?", []any{request.actor.UserID, request.teamID}, nil
}
func (m *mcpAuthorization) Verify(ctx context.Context, q configstate.DBTX, t *auth.MCPToken) error {
	on, err := modeEnabled(ctx, q)
	if err != nil {
		return err
	}
	if !on {
		if t.OwnerModeEpoch != 0 {
			return ErrForbidden
		}
		return nil
	}
	_, _, err = mcpTokenAuthority(ctx, q, t)
	return err
}
func (m *mcpAuthorization) Bind(ctx context.Context, t *auth.MCPToken) (context.Context, error) {
	if t == nil {
		return ctx, ErrForbidden
	}
	q := configstate.Queryer(ctx, m.db)
	fresh, err := auth.LoadMCPToken(ctx, q, m.db, t.ID, false)
	if err != nil {
		return ctx, err
	}
	if err = m.Verify(ctx, q, fresh); err != nil {
		return ctx, err
	}
	if fresh.OwnerModeEpoch == 0 {
		return ctx, nil
	}
	return configstate.WithWriteAdmission(context.WithValue(ctx, mcpContextKey{}, mcpContext{db: m.db, tokenID: fresh.ID}), LockAuthorization), nil
}
func mcpTokenAuthority(ctx context.Context, q configstate.DBTX, t *auth.MCPToken) (Authority, requestActor, error) {
	request := requestActor{actor: Actor{UserID: t.OwnerUserID, ModeEpoch: t.OwnerModeEpoch, SecurityGeneration: t.OwnerGeneration}, teamID: t.OwnerTeamID}
	var epoch uint64
	var on int
	if err := q.QueryRowContext(ctx, `SELECT enabled,mode_epoch FROM access_control WHERE id=1`).Scan(&on, &epoch); err != nil || on != 1 || epoch != t.OwnerModeEpoch || t.OwnerUserID == "" || t.OwnerTeamID == "" {
		return Authority{}, request, ErrForbidden
	}
	var user User
	var active, admin, restricted int
	if err := q.QueryRowContext(ctx, `SELECT id,enabled,instance_admin,must_change_password,security_generation FROM access_users WHERE id=?`, t.OwnerUserID).Scan(&user.ID, &active, &admin, &restricted, &user.SecurityGeneration); err != nil || active != 1 || restricted != 0 || user.SecurityGeneration != t.OwnerGeneration {
		return Authority{}, request, ErrForbidden
	}
	user.Enabled = true
	user.InstanceAdmin = admin == 1
	authority, err := loadSubjectAuthority(ctx, q, user, t.OwnerTeamID, nil)
	return authority, request, err
}
func mcpAuthority(ctx context.Context, db *sql.DB, cap mcpContext) (Authority, requestActor, *auth.MCPToken, error) {
	if cap.db != db {
		return Authority{}, requestActor{}, nil, ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	t, err := auth.LoadMCPToken(ctx, q, db, cap.tokenID, false)
	if err != nil {
		return Authority{}, requestActor{}, nil, err
	}
	authority, request, err := mcpTokenAuthority(ctx, q, t)
	return authority, request, t, err
}
func mcpPredicate(ctx context.Context, db *sql.DB, authority Authority, action, column string, predicate authz.Predicate) (authz.Predicate, error) {
	cap, ok := ctx.Value(mcpContextKey{}).(mcpContext)
	if !ok {
		return predicate, ErrForbidden
	}
	_, _, t, err := mcpAuthority(ctx, db, cap)
	if err != nil {
		return authz.Predicate{}, err
	}
	def, known := authz.Lookup(action)
	if !known || !authz.ValidRootColumn(column) {
		return authz.Predicate{}, ErrForbidden
	}
	ids := []string{}
	for _, id := range t.ProjectIDs {
		if action == "run.start" && !t.CanTrigger {
			continue
		}
		if action == "pipeline.edit" && !t.AllowsConfig(id, "config.propose", "", "") {
			continue
		}
		if action == "project.edit" && !t.AllowsConfig(id, "config.propose", "project", "") {
			continue
		}
		ids = append(ids, id)
	}
	if def.ProjectScoped {
		if len(ids) == 0 {
			return authz.Predicate{SQL: "1=0"}, nil
		}
		issue, err := authority.Service.RootPredicate(authority.Principal, "mcp.issue", "azmp.resource_id")
		if err != nil {
			return authz.Predicate{}, err
		}
		predicate.SQL = "(" + predicate.SQL + ") AND EXISTS (SELECT 1 FROM access_ownership azmo JOIN access_ownership azmp ON azmp.resource_kind='project' AND azmp.resource_id=azmo.project_id WHERE azmo.resource_kind=? AND azmo.resource_id=" + column + " AND azmo.project_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ") AND (" + issue.SQL + "))"
		predicate.Args = append(predicate.Args, def.ResourceKind)
		for _, id := range ids {
			predicate.Args = append(predicate.Args, id)
		}
		predicate.Args = append(predicate.Args, issue.Args...)
	}
	predicate.SQL = "(" + predicate.SQL + ") AND EXISTS (SELECT 1 FROM mcp_tokens azmt JOIN access_users azmu ON azmu.id=azmt.owner_user_id WHERE azmt.id=? AND azmt.revoked_at IS NULL AND azmt.expires_at>? AND azmt.owner_mode_epoch=? AND azmt.owner_generation=azmu.security_generation AND azmu.enabled=1 AND azmu.must_change_password=0)"
	predicate.Args = append(predicate.Args, t.ID, time.Now().UTC().Format(time.RFC3339), t.OwnerModeEpoch)
	return predicate, nil
}

// AuthorizeMCPReference prevents a service caller from substituting another
// token ID. Browser reviews authenticate their independent current team subject.
func AuthorizeMCPReference(ctx context.Context, db *sql.DB, t *auth.MCPToken) error {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return err
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		if cap.db != db || cap.tokenID != t.ID {
			return ErrForbidden
		}
		return nil
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	if !authority.Principal.InstanceAdmin && request.teamID != t.OwnerTeamID {
		return ErrForbidden
	}
	return nil
}
