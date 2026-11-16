package tenancy

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

type ExecutionPermission struct {
	Action        string `json:"action"`
	ResourceID    string `json:"resourceId"`
	EnvironmentID string `json:"environmentId,omitempty"`
}
type executionContextKey struct{}
type executionContext struct {
	db         *sql.DB
	runID      string
	planDigest string
}
type runAuthorization struct {
	runID, team, user, project, digest string
	epoch, generation                  uint64
	revision                           int64
	permissions                        []ExecutionPermission
}

// RunContext is exclusively for controller-owned queue dispatch. No browser
// cookie or caller-provided user/tenant claims are copied to background work.
func RunContext(ctx context.Context, db *sql.DB, runID, planDigest string) context.Context {
	return configstate.WithWriteAdmission(context.WithValue(ctx, executionContextKey{}, executionContext{db: db, runID: runID, planDigest: planDigest}), LockAuthorization)
}

// ApproveRun persists the fixed plan's action/resource ceiling under the same
// transaction as run admission. Call only after trusted plan resolution.
func ApproveRun(ctx context.Context, db *sql.DB, runID, project, digest string, permissions []ExecutionPermission) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	if q == db || len(permissions) == 0 || len(permissions) > 256 || len(digest) != 64 {
		return ErrInvalidInput
	}
	if _, err = hex.DecodeString(digest); err != nil {
		return ErrInvalidInput
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	if err = LockAuthorization(ctx, db); err != nil {
		return err
	}
	authority, _, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	var actual string
	if err = q.QueryRowContext(ctx, `SELECT project_id FROM pipeline_runs WHERE id=?`, runID).Scan(&actual); err != nil || actual != project {
		return ErrForbidden
	}
	revision, err := configstate.ReadRevision(ctx, q, project)
	if err != nil {
		return err
	}
	checked := []ExecutionPermission{}
	seen := map[string]bool{}
	for _, permission := range permissions {
		if _, automatic := ctx.Value(automationKey{}).(*automationPrincipal); automatic {
			if permission.Action == "run.start" || permission.Action == "run.read" {
				if err = automationAllows(ctx, db, "run.start", "", project, ""); err != nil {
					return err
				}
			} else if err = automationAllows(ctx, db, permission.Action, permission.ResourceID, "", permission.EnvironmentID); err != nil {
				return err
			}
		}
		action, known := authz.Lookup(permission.Action)
		if !known || action.InstanceOnly {
			return ErrForbidden
		}
		resource, err := executionResource(ctx, q, request.teamID, project, action, permission)
		if err != nil {
			return err
		}
		yes, err := authority.Service.Allowed(authority.Principal, action.ID, resource)
		if err != nil {
			return err
		}
		if !yes {
			return ErrForbidden
		}
		key := permission.Action + "/" + permission.ResourceID + "/" + permission.EnvironmentID
		if !seen[key] {
			checked = append(checked, permission)
			seen[key] = true
		}
		read := permission
		for next := action.ReadAction; next != ""; {
			read.Action = next
			key = read.Action + "/" + read.ResourceID + "/" + read.EnvironmentID
			if seen[key] {
				break
			}
			definition, known := authz.Lookup(next)
			if !known || definition.ResourceKind != action.ResourceKind {
				return ErrForbidden
			}
			checked = append(checked, read)
			seen[key] = true
			if len(checked) > 512 {
				return ErrInvalidInput
			}
			next = definition.ReadAction
		}
	}
	// Auto-diagnosis is optional and cannot raise the triggering actor's ceiling.
	// Capture it only for a human-approved run; MCP has a separate token ceiling.
	_, automatic := ctx.Value(automationKey{}).(*automationPrincipal)
	if _, isMCP := ctx.Value(mcpContextKey{}).(mcpContext); !automatic && !isMCP && request.serviceRunID == "" {
		permission := ExecutionPermission{Action: "run.diagnose", ResourceID: runID}
		action, _ := authz.Lookup(permission.Action)
		if resource, e := executionResource(ctx, q, request.teamID, project, action, permission); e == nil {
			if allowed, e := authority.Service.Allowed(authority.Principal, action.ID, resource); e == nil && allowed && !seen[permission.Action+"/"+runID+"/"] {
				checked = append(checked, permission)
			}
		}
	}
	envelope := runPermissionEnvelope{Version: 1, Permissions: checked}
	if parent, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		envelope.Automation = &runAutomationReference{Kind: parent.kind, ID: parent.id, Approval: parent.approval, Digest: parent.digest}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	var mcpID any
	if cap, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		_, record, e := automationAuthority(ctx, db, cap)
		if e != nil {
			return e
		}
		if record.envelope.MCPTokenID != "" {
			mcpID = record.envelope.MCPTokenID
		}
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		mcpID = cap.tokenID
	}
	_, err = q.ExecContext(ctx, `INSERT INTO access_run_authorizations(run_id,tenant_id,user_id,project_id,mode_epoch,user_generation,config_revision,catalog_version,plan_digest,permissions_json,created_at,revoked_at,mcp_token_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,NULL,?)`, runID, request.teamID, request.actor.UserID, project, request.actor.ModeEpoch, request.actor.SecurityGeneration, revision, authz.CatalogVersion, digest, string(body), time.Now().Unix(), mcpID)
	return err
}
func executionResource(ctx context.Context, q configstate.DBTX, team, project string, action authz.Action, permission ExecutionPermission) (authz.Resource, error) {
	resource, err := resourceFrom(ctx, q, team, action.ResourceKind, permission.ResourceID)
	if err != nil {
		return resource, err
	}
	if action.ProjectScoped && resource.ProjectID != project {
		return resource, ErrForbidden
	}
	if permission.EnvironmentID != "" {
		var purpose string
		if err = q.QueryRowContext(ctx, `SELECT purpose FROM access_environments WHERE id=? AND tenant_id=? AND project_id=?`, permission.EnvironmentID, team, project).Scan(&purpose); err != nil {
			return resource, ErrForbidden
		}
		if resource.EnvironmentID != "" && resource.EnvironmentID != permission.EnvironmentID {
			return resource, ErrForbidden
		}
		resource.EnvironmentID = permission.EnvironmentID
		resource.EnvironmentPurpose = purpose
	}
	return resource, nil
}
func loadRunAuthority(ctx context.Context, q configstate.DBTX, runID string) (Authority, runAuthorization, error) {
	var cap runAuthorization
	var catalog int
	var body string
	cap.runID = runID
	err := q.QueryRowContext(ctx, `SELECT tenant_id,user_id,project_id,mode_epoch,user_generation,config_revision,catalog_version,plan_digest,permissions_json FROM access_run_authorizations WHERE run_id=? AND revoked_at IS NULL`, runID).Scan(&cap.team, &cap.user, &cap.project, &cap.epoch, &cap.generation, &cap.revision, &catalog, &cap.digest, &body)
	if err != nil {
		return Authority{}, cap, ErrForbidden
	}
	envelope, decodeErr := decodeRunPermissions(body)
	if catalog != authz.CatalogVersion || decodeErr != nil {
		return Authority{}, cap, ErrForbidden
	}
	cap.permissions = envelope.Permissions
	if envelope.Automation != nil {
		if err = validateRunAutomation(ctx, q, cap, envelope.Automation); err != nil {
			return Authority{}, cap, err
		}
	}
	var epoch uint64
	var enabled int
	if err = q.QueryRowContext(ctx, `SELECT enabled,mode_epoch FROM access_control WHERE id=1`).Scan(&enabled, &epoch); err != nil || enabled != 1 || epoch != cap.epoch {
		return Authority{}, cap, ErrForbidden
	}
	revision, err := configstate.ReadRevision(ctx, q, cap.project)
	if err != nil || revision != cap.revision {
		return Authority{}, cap, ErrForbidden
	}
	var user User
	var active, admin, restricted int
	if err = q.QueryRowContext(ctx, `SELECT id,enabled,instance_admin,must_change_password,security_generation FROM access_users WHERE id=?`, cap.user).Scan(&user.ID, &active, &admin, &restricted, &user.SecurityGeneration); err != nil || active != 1 || restricted != 0 || user.SecurityGeneration != cap.generation {
		return Authority{}, cap, ErrForbidden
	}
	var mcpID sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT mcp_token_id FROM access_run_authorizations WHERE run_id=?`, runID).Scan(&mcpID); err != nil {
		return Authority{}, cap, ErrForbidden
	}
	if mcpID.Valid {
		token, e := auth.LoadMCPToken(ctx, q, nil, mcpID.String, false)
		if e != nil || token.OwnerUserID != cap.user || token.OwnerTeamID != cap.team || token.OwnerModeEpoch != cap.epoch || token.OwnerGeneration != cap.generation || !token.Allows(cap.project, true) {
			return Authority{}, cap, ErrForbidden
		}
		owner, _, e := mcpTokenAuthority(ctx, q, token)
		if e != nil {
			return Authority{}, cap, e
		}
		permitted, e := owner.Service.Allowed(owner.Principal, "mcp.issue", authz.Resource{Kind: "project", ID: cap.project, ProjectID: cap.project, TenantID: cap.team})
		if e != nil || !permitted {
			return Authority{}, cap, ErrForbidden
		}
	}
	user.Enabled = true
	user.InstanceAdmin = admin == 1
	authority, err := loadSubjectAuthority(ctx, q, user, cap.team, nil)
	return authority, cap, err
}

// CheckRunPermission is checked immediately before each planned side effect.
// Role changes are evaluated fresh; unrelated global revision changes do not
// terminate work. The immutable plan ceiling prevents later grants expanding it.
func CheckRunPermission(ctx context.Context, db *sql.DB, action, id, environment string) error {
	job, ok := ctx.Value(executionContextKey{}).(executionContext)
	if !ok || job.db != db {
		return ErrForbidden
	}
	return configstate.WithReadTx(ctx, db, func(q configstate.DBTX) error {
		authority, cap, err := loadRunAuthority(ctx, q, job.runID)
		if err == nil && (job.planDigest == "" || job.planDigest != cap.digest) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		for _, permission := range cap.permissions {
			if permission.Action != action || permission.ResourceID != id || permission.EnvironmentID != environment {
				continue
			}
			definition, known := authz.Lookup(action)
			if !known {
				return ErrForbidden
			}
			resource, err := executionResource(ctx, q, cap.team, cap.project, definition, permission)
			if err != nil {
				return err
			}
			yes, err := authority.Service.Allowed(authority.Principal, action, resource)
			if err != nil {
				return err
			}
			if yes {
				return nil
			}
			return ErrForbidden
		}
		return ErrForbidden
	})
}

// CheckRunAdmission authorizes a new run against its project's authoritative
// tenant before inserting it. This grants scheduling only, never deployment.
func CheckRunAdmission(ctx context.Context, db *sql.DB, projectID, runID string) error {
	if err := automationAllows(ctx, db, "run.start", "", projectID, ""); err != nil {
		return err
	}
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return ErrForbidden
	}
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return err
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		_, _, token, e := mcpAuthority(ctx, db, cap)
		if e != nil || !token.Allows(projectID, true) {
			return ErrForbidden
		}
		if e = Check(ctx, db, "mcp.issue", "project", projectID); e != nil {
			return e
		}
	}
	if request.serviceRunID != "" {
		return ErrForbidden
	} // Automatic triggers use a separately approved binding.
	var count int
	if err = configstate.Queryer(ctx, db).QueryRowContext(ctx, `SELECT COUNT(*) FROM access_ownership WHERE resource_kind='project' AND resource_id=? AND tenant_id=?`, projectID, request.teamID).Scan(&count); err != nil || count != 1 {
		return ErrForbidden
	}
	yes, err := authority.Service.Allowed(authority.Principal, "run.start", authz.Resource{Kind: "run", ID: runID, ProjectID: projectID, TenantID: request.teamID, OwnerID: request.actor.UserID})
	if err != nil {
		return err
	}
	if !yes {
		return ErrForbidden
	}
	return nil
}

func executionInputPredicate(ctx context.Context, db *sql.DB, authority Authority, runID, column string) (authz.Predicate, error) {
	if !authz.ValidRootColumn(column) {
		return authz.Predicate{SQL: "1=0"}, nil
	}
	_, cap, err := loadRunAuthority(ctx, configstate.Queryer(ctx, db), runID)
	if err != nil {
		return authz.Predicate{}, err
	}
	admission, err := RunAdmissionAction(ctx, db, runID)
	if err != nil {
		return authz.Predicate{}, err
	}
	start, err := authority.Service.RootPredicate(authority.Principal, admission, "azj.run_id")
	if err != nil {
		return authz.Predicate{}, err
	}
	sql := `EXISTS (SELECT 1 FROM access_run_authorizations azj JOIN access_users azu ON azu.id=azj.user_id JOIN project_config_versions azv ON azv.project_id=azj.project_id WHERE azj.run_id=? AND azj.project_id=` + column + ` AND azj.revoked_at IS NULL AND azj.mode_epoch=? AND azj.user_generation=azu.security_generation AND azu.enabled=1 AND azu.must_change_password=0 AND azj.config_revision=azv.revision AND azv.deleted_at IS NULL AND (` + start.SQL + `))`
	args := []any{runID, cap.epoch}
	args = append(args, start.Args...)
	return authz.Predicate{SQL: sql, Args: args}, nil
}

type preparationContextKey struct{}
type preparationContext struct {
	db      *sql.DB
	project string
}

// PrepareRunContext permits controller-owned preparation to consume only this
// run's project inputs. It grants no UI inspection, configuration writes, secret
// use or execution, and is revalidated on every query and at final admission.
func PrepareRunContext(ctx context.Context, db *sql.DB, project string) (context.Context, error) {
	if err := CheckRunAdmission(ctx, db, project, "prepared-run"); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, preparationContextKey{}, preparationContext{db: db, project: project}), nil
}
