package tenancy

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"slices"
	"strings"
)

// ApprovalScope is called only by the gate controller. The stored scope cannot
// be supplied or overwritten by an approval HTTP body or notification token.
func ApprovalScope(ctx context.Context, db *sql.DB, runID, environmentName string) (string, string, error) {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return "", "", err
	}
	if err = VerifyExecution(ctx); err != nil {
		return "", "", err
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return "", "", err
	}
	resource, err := resourceFrom(ctx, q, request.teamID, "run", runID)
	if err != nil {
		return "", "", err
	}
	if environmentName != "" {
		var id, purpose string
		if err = q.QueryRowContext(ctx, `SELECT id,purpose FROM access_environments WHERE tenant_id=? AND project_id=? AND name=?`, request.teamID, resource.ProjectID, environmentName).Scan(&id, &purpose); err != nil {
			return "", "", ErrForbidden
		}
		allowed, e := authority.Service.Allowed(authority.Principal, "deployment.execute", authz.Resource{Kind: "environment", ID: id, TenantID: request.teamID, ProjectID: resource.ProjectID, EnvironmentID: id, EnvironmentPurpose: purpose})
		if e != nil || !allowed {
			return "", "", ErrForbidden
		}
		encoded, _ := json.Marshal([]string{id})
		return "release.approve", string(encoded), nil
	}
	if request.serviceRunID != runID {
		return "", "", ErrForbidden
	}
	_, cap, err := loadRunAuthority(ctx, q, runID)
	if err != nil {
		return "", "", err
	}
	environments := []string{}
	for _, permission := range cap.permissions {
		if permission.Action == "deployment.execute" {
			if permission.EnvironmentID == "" {
				return "", "", ErrForbidden
			}
			if !slices.Contains(environments, permission.EnvironmentID) {
				environments = append(environments, permission.EnvironmentID)
			}
		}
	}
	if len(environments) > 0 {
		slices.Sort(environments)
		encoded, _ := json.Marshal(environments)
		return "release.approve", string(encoded), nil
	}

	return "run.approve", "", nil
}
func CheckApproval(ctx context.Context, db *sql.DB, runID, action, environment string) (string, error) {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return "", err
	}
	if action != "run.approve" && action != "release.approve" {
		return "", ErrForbidden
	}
	if _, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		return "", ErrForbidden
	}
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return "", ErrForbidden
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil || request.serviceRunID != "" {
		return "", ErrForbidden
	}
	resource, err := resourceFrom(ctx, q, request.teamID, "run", runID)
	if err != nil {
		return "", err
	}
	environments, err := ApprovalEnvironmentIDs(action, environment)
	if err != nil {
		return "", err
	}
	if action == "run.approve" {
		environments = []string{""}
	}
	for _, id := range environments {
		current := resource
		if id != "" {
			var purpose string
			if err = q.QueryRowContext(ctx, `SELECT purpose FROM access_environments WHERE id=? AND tenant_id=? AND project_id=?`, id, request.teamID, resource.ProjectID).Scan(&purpose); err != nil {
				return "", ErrForbidden
			}
			current.EnvironmentID = id
			current.EnvironmentPurpose = purpose
		}
		allowed, e := authority.Service.Allowed(authority.Principal, action, current)
		if e != nil || !allowed {
			return "", ErrForbidden
		}
	}

	return request.actor.UserID, nil
}

// ApprovalEnvironmentIDs decodes the bounded, controller-persisted approval ceiling.
func ApprovalEnvironmentIDs(action, encoded string) ([]string, error) {
	if action == "" && encoded == "" {
		return nil, nil
	} // legacy, off mode presentation
	if action == "run.approve" && encoded == "" {
		return nil, nil
	}
	if action != "release.approve" || len(encoded) > 32768 {
		return nil, ErrForbidden
	}
	var ids []string
	if json.Unmarshal([]byte(encoded), &ids) != nil || len(ids) == 0 || len(ids) > 256 {
		return nil, ErrForbidden
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 128 || seen[id] {
			return nil, ErrForbidden
		}
		seen[id] = true
	}
	return ids, nil
}

// PendingApprovalEnvironment checks the concrete gate before exposing a UI action.
func PendingApprovalEnvironment(ctx context.Context, q configstate.DBTX, runID, action, environment string) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT access_environment_ids FROM run_approvals WHERE run_id=? AND status='pending' AND access_action=?`, runID, action)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var encoded sql.NullString
		if err = rows.Scan(&encoded); err != nil {
			return false, err
		}
		ids, e := ApprovalEnvironmentIDs(action, encoded.String)
		if e != nil {
			continue
		}
		if action == "run.approve" && environment == "" || action == "release.approve" && slices.Contains(ids, environment) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// PromotionApprovalScope freezes the target chosen by the coordinator. It does
// not grant deployment or allow a caller to self-approve the resulting gate.
func PromotionApprovalScope(ctx context.Context, db *sql.DB, runID, name string) (string, string, error) {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return "", "", err
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return "", "", err
	}
	resource, err := resourceFrom(ctx, q, request.teamID, "run", runID)
	if err != nil {
		return "", "", err
	}
	if err = CheckEnvironmentAction(ctx, db, "release.promote", "run", runID, resource.ProjectID, name); err != nil {
		return "", "", err
	}
	var id string
	if err = q.QueryRowContext(ctx, `SELECT id FROM access_environments WHERE tenant_id=? AND project_id=? AND name=?`, request.teamID, resource.ProjectID, name).Scan(&id); err != nil {
		return "", "", ErrForbidden
	}
	body, _ := json.Marshal([]string{id})
	return "release.approve", string(body), nil
}

// SubjectID returns the authenticated immutable user, never a supplied label.
func SubjectID(ctx context.Context, db *sql.DB, legacy string) (string, error) {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return legacy, err
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return "", err
	}
	return request.actor.UserID, nil
}
