package tenancy

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// AutomationPermission fixes an action to an existing resource. ProjectID is
// used only for a future run admission, whose eventual run ID does not yet exist.
type AutomationPermission struct {
	Action        string `json:"action"`
	ResourceID    string `json:"resourceId,omitempty"`
	EnvironmentID string `json:"environmentId,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
}
type automationSourceProjectKey struct{}
type automationEnvelope struct {
	ResourceVersions map[string]string      `json:"resourceVersions,omitempty"`
	ProjectDigest    string                 `json:"projectDigest,omitempty"`
	SourceDigest     string                 `json:"sourceDigest,omitempty"`
	SourceProject    string                 `json:"sourceProject,omitempty"`
	Permissions      []AutomationPermission `json:"permissions"`
	MCPTokenID       string                 `json:"mcpTokenId,omitempty"`
}
type automationKey struct{}
type automationPrincipal struct {
	db                         *sql.DB
	kind, id, approval, digest string
	user, team                 string
	current                    func(context.Context, configstate.DBTX) (string, error)
}
type automationRecord struct {
	user, team, project string
	epoch, generation   uint64
	envelope            automationEnvelope
}

func validAutomationSource(kind, id, digest string) bool {
	switch kind {
	case "anomaly_rule", "trigger", "cron", "chain", "preview", "notification", "server_metrics":
	default:
		return false
	}
	if id == "" || len(id) > 64 || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// ApproveAutomation is called in the same authorization-fenced transaction as
// saving a trusted configuration. Background capabilities cannot mint new ones.
// A replacement rotates approval_id, invalidating every previously loaded job.
func ApproveAutomation(ctx context.Context, db *sql.DB, kind, id, project, digest string, permissions []AutomationPermission) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	if q == db || !validAutomationSource(kind, id, digest) || len(permissions) == 0 || len(permissions) > 256 {
		return ErrInvalidInput
	}
	if _, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		return ErrForbidden
	}
	if _, ok := ctx.Value(executionContextKey{}).(executionContext); ok {
		return ErrForbidden
	}
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return ErrForbidden
	}
	if err = LockAuthorization(ctx, db); err != nil {
		return err
	}
	a, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	envelope := automationEnvelope{}
	if source, ok := ctx.Value(automationSourceProjectKey{}).(string); ok {
		digest, e := ProjectExecutionDigest(ctx, q, source)
		if e != nil {
			return e
		}
		envelope.SourceProject = source
		envelope.SourceDigest = digest
	}
	if kind == "trigger" || kind == "cron" || kind == "chain" || kind == "preview" {
		digest, e := ProjectExecutionDigest(ctx, q, project)
		if e != nil {
			return e
		}
		envelope.ProjectDigest = digest
	}

	if token, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		envelope.MCPTokenID = token.tokenID
	}
	seen := map[AutomationPermission]bool{}
	// Include every read prerequisite, not only the first link of the catalog.
	for _, permission := range permissions {
		for {
			if seen[permission] {
				break
			}
			seen[permission] = true
			definition, known := authz.Lookup(permission.Action)
			if !known {
				return ErrForbidden
			}
			resource, e := automationResource(ctx, q, request.teamID, project, request.actor.UserID, permission)
			if e != nil {
				return e
			}
			allowed, e := a.Service.Allowed(a.Principal, permission.Action, resource)
			if e != nil {
				return e
			}
			if !allowed {
				return ErrForbidden
			}
			if envelope.MCPTokenID != "" {
				// Token ceilings apply independently from the owner's roles.
				if definition.InstanceOnly {
					return ErrForbidden
				}
				if permission.Action == "run.start" {
					if e = CheckRunAdmission(ctx, db, permission.ProjectID, "automation-admission"); e != nil {
						return e
					}
				} else if e = Check(ctx, db, permission.Action, definition.ResourceKind, permission.ResourceID); e != nil {
					return e
				}
			}
			envelope.Permissions = append(envelope.Permissions, permission)
			if len(envelope.Permissions) > 512 {
				return ErrInvalidInput
			}
			if definition.ReadAction == "" || permission.Action == "run.start" {
				break
			}
			permission.Action = definition.ReadAction
		}
	}
	if kind == "trigger" || kind == "cron" || kind == "chain" || kind == "preview" || kind == "server_metrics" {
		envelope.ResourceVersions, err = captureAutomationResourceVersions(ctx, q, envelope.Permissions)
		if err != nil {
			return err
		}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `DELETE FROM access_automation_authorizations WHERE source_kind=? AND source_id=?`, kind, id); err != nil {
		return err
	}
	var projectValue any
	if project != "" {
		projectValue = project
	}
	_, err = q.ExecContext(ctx, `INSERT INTO access_automation_authorizations(source_kind,source_id,approval_id,tenant_id,user_id,project_id,mode_epoch,user_generation,catalog_version,config_digest,permissions_json,created_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,NULL)`, kind, id, uuid.NewString(), request.teamID, request.actor.UserID, projectValue, request.actor.ModeEpoch, request.actor.SecurityGeneration, authz.CatalogVersion, digest, string(body), time.Now().Unix())
	return err
}

// RevokeAutomation must be part of the source's authorized mutation transaction.
func RevokeAutomation(ctx context.Context, db *sql.DB, kind, id string) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	if q == db {
		return ErrConflict
	}
	if err = LockAuthorization(ctx, db); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE access_automation_authorizations SET revoked_at=? WHERE source_kind=? AND source_id=?`, time.Now().Unix(), kind, id)
	return err
}

// AutomationContext loads one controller-selected approval. The current callback
// reads only the source configuration and returns its canonical digest, failing
// for disabled/deleted sources. It must not perform network I/O or authorization
// queries (which would recurse). No browser session is retained in this context.
func AutomationContext(ctx context.Context, db *sql.DB, kind, id, digest string, current func(context.Context, configstate.DBTX) (string, error)) (context.Context, error) {
	if HasAuthorizationContext(ctx) || !validAutomationSource(kind, id, digest) || current == nil {
		return nil, ErrForbidden
	}
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil {
		return nil, err
	}
	if !on {
		return ctx, nil
	}
	cap := &automationPrincipal{db: db, kind: kind, id: id, digest: digest, current: current}
	if err = configstate.Queryer(ctx, db).QueryRowContext(ctx, `SELECT approval_id FROM access_automation_authorizations WHERE source_kind=? AND source_id=? AND config_digest=? AND revoked_at IS NULL`, kind, id, digest).Scan(&cap.approval); err != nil {
		return nil, ErrForbidden
	}
	out := configstate.WithWriteAdmission(context.WithValue(ctx, automationKey{}, cap), LockAuthorization)
	var record automationRecord
	if _, record, err = automationAuthority(out, db, cap); err != nil {
		return nil, err
	}
	cap.user, cap.team = record.user, record.team
	return out, nil
}
func automationResource(ctx context.Context, q configstate.DBTX, team, project, user string, p AutomationPermission) (authz.Resource, error) {
	definition, known := authz.Lookup(p.Action)
	if !known {
		return authz.Resource{}, ErrForbidden
	}
	if definition.InstanceOnly {
		if p.ResourceID != "instance" || p.ProjectID != "" || p.EnvironmentID != "" {
			return authz.Resource{}, ErrForbidden
		}
		return authz.Resource{Kind: definition.ResourceKind, ID: "instance", TenantID: team}, nil
	}
	// A preview configuration may approve future previews in its one project.
	// This is not an arbitrary resource wildcard: it is evaluated as a synthetic
	// preview when saved and intersects actual row ownership and live grants later.
	if (p.Action == "preview.read" || p.Action == "preview.reclaim") && p.ProjectID != "" {
		if p.ProjectID != project || p.ResourceID != "" || p.EnvironmentID != "" {
			return authz.Resource{}, ErrForbidden
		}
		r, e := resourceFrom(ctx, q, team, "project", project)
		if e != nil {
			return authz.Resource{}, e
		}
		return authz.Resource{Kind: "preview", ID: "automation-preview", TenantID: r.TenantID, ProjectID: project, OwnerID: user}, nil
	}
	if p.Action == "run.start" {
		if p.ProjectID == "" || p.ProjectID != project || p.ResourceID != "" || p.EnvironmentID != "" {
			return authz.Resource{}, ErrForbidden
		}
		resource, err := resourceFrom(ctx, q, team, "project", project)
		if err != nil {
			return authz.Resource{}, err
		}
		return authz.Resource{Kind: "run", ID: "automation-admission", TenantID: resource.TenantID, ProjectID: project, OwnerID: user}, nil
	}
	if p.ProjectID != "" || p.ResourceID == "" {
		return authz.Resource{}, ErrForbidden
	}
	var actualTeam string
	if err := q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind=? AND resource_id=?`, definition.ResourceKind, p.ResourceID).Scan(&actualTeam); err != nil {
		return authz.Resource{}, ErrForbidden
	}
	if actualTeam != team {
		var admin int
		if err := q.QueryRowContext(ctx, `SELECT instance_admin FROM access_users WHERE id=?`, user).Scan(&admin); err != nil || admin != 1 {
			return authz.Resource{}, ErrForbidden
		}
		team = actualTeam
	}
	return executionResource(ctx, q, team, project, definition, ExecutionPermission{Action: p.Action, ResourceID: p.ResourceID, EnvironmentID: p.EnvironmentID})
}
func automationAuthority(ctx context.Context, db *sql.DB, cap *automationPrincipal) (Authority, automationRecord, error) {
	var record automationRecord
	if cap == nil || cap.db != db || cap.current == nil {
		return Authority{}, record, ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return Authority{}, record, ErrForbidden
	}
	var catalog int
	var body string
	var project sql.NullString
	err = q.QueryRowContext(ctx, `SELECT user_id,tenant_id,project_id,mode_epoch,user_generation,catalog_version,permissions_json FROM access_automation_authorizations WHERE source_kind=? AND source_id=? AND approval_id=? AND config_digest=? AND revoked_at IS NULL`, cap.kind, cap.id, cap.approval, cap.digest).Scan(&record.user, &record.team, &project, &record.epoch, &record.generation, &catalog, &body)
	if err != nil || catalog != authz.CatalogVersion || len(body) > 131072 || json.Unmarshal([]byte(body), &record.envelope) != nil || len(record.envelope.Permissions) == 0 || len(record.envelope.Permissions) > 512 {
		return Authority{}, record, ErrForbidden
	}
	record.project = project.String
	if err = verifyAutomationResourceVersions(ctx, q, record.envelope.ResourceVersions); err != nil {
		return Authority{}, record, err
	}
	if err = verifyAutomationPlans(ctx, q, record.project, record.envelope); err != nil {
		return Authority{}, record, err
	}

	var epoch uint64
	if err = q.QueryRowContext(ctx, `SELECT mode_epoch FROM access_control WHERE id=1`).Scan(&epoch); err != nil || epoch != record.epoch {
		return Authority{}, record, ErrForbidden
	}
	current, err := cap.current(ctx, q)
	if err != nil || current != cap.digest {
		return Authority{}, record, ErrForbidden
	}
	var user User
	var enabled, admin, restricted int
	if err = q.QueryRowContext(ctx, `SELECT id,enabled,instance_admin,must_change_password,security_generation FROM access_users WHERE id=?`, record.user).Scan(&user.ID, &enabled, &admin, &restricted, &user.SecurityGeneration); err != nil || enabled != 1 || restricted != 0 || user.SecurityGeneration != record.generation {
		return Authority{}, record, ErrForbidden
	}
	user.Enabled = true
	user.InstanceAdmin = admin == 1
	a, err := loadSubjectAuthority(ctx, q, user, record.team, nil)
	if err != nil {
		return Authority{}, record, err
	}
	if record.envelope.MCPTokenID != "" {
		token, e := auth.LoadMCPToken(ctx, q, nil, record.envelope.MCPTokenID, false)
		if e != nil || token.OwnerUserID != record.user || token.OwnerTeamID != record.team || token.OwnerModeEpoch != record.epoch || token.OwnerGeneration != record.generation {
			return Authority{}, record, ErrForbidden
		}
		if _, _, e = mcpTokenAuthority(ctx, q, token); e != nil {
			return Authority{}, record, e
		}
	}
	for _, p := range record.envelope.Permissions {
		resource, e := automationResource(ctx, q, record.team, record.project, record.user, p)
		if e != nil {
			return Authority{}, record, e
		}
		yes, e := a.Service.Allowed(a.Principal, p.Action, resource)
		if e != nil || !yes {
			return Authority{}, record, ErrForbidden
		}
	}
	return a, record, nil
}
func automationPredicate(ctx context.Context, db *sql.DB, cap *automationPrincipal, action, column string) (authz.Predicate, error) {
	a, record, err := automationAuthority(ctx, db, cap)
	if err != nil {
		return authz.Predicate{}, err
	}
	predicate, err := a.Service.RootPredicate(a.Principal, action, column)
	if err != nil {
		return authz.Predicate{}, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, p := range record.envelope.Permissions {
		if p.Action == action && p.ResourceID != "" && !seen[p.ResourceID] {
			ids = append(ids, p.ResourceID)
			seen[p.ResourceID] = true
		}
	}
	ceiling := "1=0"
	ceilingArgs := []any{}
	if len(ids) > 0 {
		ceiling = column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, id := range ids {
			ceilingArgs = append(ceilingArgs, id)
		}
	}
	if cap.kind == "preview" && (action == "preview.read" || action == "preview.reclaim") {
		for _, p := range record.envelope.Permissions {
			if p.Action == action && p.ProjectID == record.project && p.ProjectID != "" {
				ceiling = "(" + ceiling + ") OR EXISTS(SELECT 1 FROM access_ownership azpv WHERE azpv.resource_kind='preview' AND azpv.resource_id=" + column + " AND azpv.project_id=? AND azpv.tenant_id=?)"
				ceilingArgs = append(ceilingArgs, record.project, record.team)
				break
			}
		}
	}
	if ceiling == "1=0" {
		return authz.Predicate{SQL: "1=0"}, nil
	}
	predicate.SQL = "(" + predicate.SQL + ") AND (" + ceiling + ") AND EXISTS (SELECT 1 FROM access_automation_authorizations aza JOIN access_users azu ON azu.id=aza.user_id WHERE aza.source_kind=? AND aza.source_id=? AND aza.approval_id=? AND aza.config_digest=? AND aza.revoked_at IS NULL AND aza.mode_epoch=? AND aza.user_generation=azu.security_generation AND azu.enabled=1 AND azu.must_change_password=0)"
	predicate.Args = append(predicate.Args, ceilingArgs...)
	predicate.Args = append(predicate.Args, cap.kind, cap.id, cap.approval, cap.digest, record.epoch)
	if record.envelope.MCPTokenID != "" {
		return mcpPredicate(context.WithValue(ctx, mcpContextKey{}, mcpContext{db: db, tokenID: record.envelope.MCPTokenID}), db, a, action, column, predicate)
	}
	return predicate, nil
}
func automationAllows(ctx context.Context, db *sql.DB, action, id, project, environment string) error {
	cap, ok := ctx.Value(automationKey{}).(*automationPrincipal)
	if !ok {
		return nil
	}
	_, record, err := automationAuthority(ctx, db, cap)
	if err != nil {
		return err
	}
	for _, p := range record.envelope.Permissions {
		if p.Action == action && p.ResourceID == id && p.ProjectID == project && p.EnvironmentID == environment {
			return nil
		}
	}
	return ErrForbidden
}

// AutomationReady is status-only. The caller must authorize reading its source
// first; it exposes neither the approver nor a context usable for execution.
func AutomationReady(ctx context.Context, db *sql.DB, kind, id, digest string, current func(context.Context, configstate.DBTX) (string, error)) bool {
	_, err := AutomationContext(configstate.WithTxContext(automationStatusContext{ctx}, db, configstate.Queryer(ctx, db)), db, kind, id, digest, current)
	return err == nil
}

type automationStatusContext struct{ context.Context }

func (automationStatusContext) Value(any) any { return nil }
