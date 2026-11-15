package tenancy

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type RoleInput struct {
	Name                    string        `json:"name"`
	Pages                   []string      `json:"pages"`
	Grants                  []authz.Grant `json:"grants"`
	RequiresSelectedBinding bool          `json:"requiresSelectedBinding"`
	ExpectedRevision        uint64        `json:"expectedRevision"`
}
type RoleView struct {
	authz.Role
	Revision  uint64 `json:"revision"`
	CanManage bool   `json:"canManage"`
}

func readRole(ctx context.Context, tx *sql.Tx, team, id string) (RoleView, error) {
	var out RoleView
	var selected, deleted int
	var pages, grants string
	out.TenantID = team
	out.ID = id
	err := tx.QueryRowContext(ctx, `SELECT name,preset_key,seed_version,requires_selected_binding,deleted,revision,pages_json,grants_json FROM access_roles WHERE tenant_id=? AND id=?`, team, id).Scan(&out.NameKey, &out.PresetKey, &out.SeedVersion, &selected, &deleted, &out.Revision, &pages, &grants)
	if err != nil {
		return out, ErrForbidden
	}
	out.RequiresSelectedBinding = selected == 1
	out.Deleted = deleted == 1
	if json.Unmarshal([]byte(pages), &out.Pages) != nil || json.Unmarshal([]byte(grants), &out.Grants) != nil {
		return out, authz.ErrInvalidPolicy
	}
	return out, nil
}
func checkRoleManagement(ctx context.Context, tx *sql.Tx, actor Actor, team, id string, creating bool) (Authority, error) {
	authority, err := loadAuthority(ctx, tx, actor, team)
	if err != nil {
		return authority, err
	}
	resource := authz.Resource{TenantID: team, Kind: "role", ID: id}
	if !creating {
		resource, err = resourceFrom(ctx, tx, team, "role", id)
		if err != nil {
			return authority, err
		}
	}
	yes, err := authority.Service.Allowed(authority.Principal, "role.manage", resource)
	if err != nil {
		return authority, err
	}
	if !yes {
		return authority, ErrForbidden
	}
	return authority, nil
}
func validateRoleGrants(ctx context.Context, tx configstate.DBTX, team string, role authz.Role) (map[string]string, error) {
	if _, err := authz.New(authz.Snapshot{CatalogVersion: authz.CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: []authz.Role{role}}); err != nil {
		return nil, err
	}
	purposes := map[string]string{}
	for _, g := range role.Grants {
		action, _ := authz.Lookup(g.Action)
		for _, id := range g.Scope.ProjectIDs {
			var owner string
			if err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, id).Scan(&owner); err != nil || owner != team {
				return nil, ErrInvalidInput
			}
		}
		for _, ref := range g.Scope.ResourceRefs {
			if ref.Kind != action.ResourceKind {
				return nil, ErrInvalidInput
			}
		}
		for _, id := range g.Scope.IDsForKind(action.ResourceKind) {
			var owner string
			switch action.ResourceKind {
			case "role":
				if err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM access_roles WHERE id=? AND deleted=0`, id).Scan(&owner); err != nil {
					return nil, ErrInvalidInput
				}
			case "member":
				if err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM access_members WHERE id=? AND enabled=1`, id).Scan(&owner); err != nil {
					return nil, ErrInvalidInput
				}
			default:
				if err := tx.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind=? AND resource_id=?`, action.ResourceKind, id).Scan(&owner); err != nil {
					return nil, ErrInvalidInput
				}
			}
			if owner != team {
				return nil, ErrInvalidInput
			}
		}
		for _, id := range g.Scope.EnvironmentIDs {
			var owner, purpose string
			if err := tx.QueryRowContext(ctx, `SELECT tenant_id,purpose FROM access_environments WHERE id=?`, id).Scan(&owner, &purpose); err != nil || owner != team {
				return nil, ErrInvalidInput
			}
			purposes[id] = purpose
		}
	}
	return purposes, nil
}

// SaveRole creates or updates an explicit role. Existing preset receipts stay
// intact; editing/deleting a seed never asks startup to recreate it.
func (r *Repository) SaveRole(ctx context.Context, actor Actor, team, id string, in RoleInput) (RoleView, error) {
	if !validName(in.Name) || len(in.Grants) > 128 || len(in.Pages) > 64 {
		return RoleView{}, ErrInvalidInput
	}
	creating := id == ""
	if creating {
		id = uuid.NewString()
		if in.ExpectedRevision != 0 {
			return RoleView{}, ErrConflict
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return RoleView{}, err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return RoleView{}, err
	}
	authority, err := checkRoleManagement(ctx, tx, actor, team, id, creating)
	if err != nil {
		return RoleView{}, err
	}
	var old RoleView
	if !creating {
		old, err = readRole(ctx, tx, team, id)
		if err != nil {
			return RoleView{}, err
		}
		if old.Deleted || old.Revision != in.ExpectedRevision {
			return RoleView{}, ErrConflict
		}
	}
	next := RoleView{Role: authz.Role{ID: id, TenantID: team, NameKey: in.Name, Pages: in.Pages, Grants: in.Grants, RequiresSelectedBinding: in.RequiresSelectedBinding}, Revision: old.Revision + 1}
	next.PresetKey, next.SeedVersion = old.PresetKey, old.SeedVersion
	purposes, err := validateRoleGrants(ctx, tx, team, next.Role)
	if err != nil {
		return RoleView{}, err
	}
	if !authority.Principal.InstanceAdmin && old.RequiresSelectedBinding && !next.RequiresSelectedBinding {
		return RoleView{}, ErrForbidden
	}
	for _, grant := range next.Grants {
		unchanged := false
		if !creating && (!old.RequiresSelectedBinding || next.RequiresSelectedBinding) {
			for _, previous := range old.Grants {
				if reflect.DeepEqual(previous, grant) && readPrerequisiteUnchanged(old.Role, next.Role, grant.Action) {
					unchanged = true
					break
				}
			}
		}
		if !unchanged && !authority.Service.CanDelegate(authority.Principal, grant, purposes) {
			return RoleView{}, ErrForbidden
		}
	}
	if !creating && next.RequiresSelectedBinding && !old.RequiresSelectedBinding {
		rows, e := tx.QueryContext(ctx, `SELECT scope_json FROM access_bindings WHERE tenant_id=? AND role_id=?`, team, id)
		if e != nil {
			return RoleView{}, e
		}
		for rows.Next() {
			var body string
			var scope authz.Scope
			if e = rows.Scan(&body); e != nil {
				rows.Close()
				return RoleView{}, e
			}
			if json.Unmarshal([]byte(body), &scope) != nil || (scope.Kind != authz.ScopeSelected && scope.Kind != authz.ScopeNone) {
				rows.Close()
				return RoleView{}, ErrConflict
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return RoleView{}, e
		}
	}
	beforeAdmins, err := teamAdministratorCount(ctx, tx, team)
	if err != nil {
		return RoleView{}, err
	}
	pages, _ := json.Marshal(next.Pages)
	grants, _ := json.Marshal(next.Grants)
	selected := 0
	if next.RequiresSelectedBinding {
		selected = 1
	}
	if creating {
		_, err = tx.ExecContext(ctx, `INSERT INTO access_roles(id,tenant_id,name,preset_key,seed_version,requires_selected_binding,deleted,revision,pages_json,grants_json) VALUES(?,?,?,'',0,?,0,1,?,?)`, id, team, in.Name, selected, string(pages), string(grants))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE access_roles SET name=?,requires_selected_binding=?,revision=revision+1,pages_json=?,grants_json=? WHERE tenant_id=? AND id=? AND revision=?`, in.Name, selected, string(pages), string(grants), team, id, in.ExpectedRevision)
	}
	if err != nil {
		return RoleView{}, err
	}
	if creating {
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('role',?,?,?)`, id, team, actor.UserID); err != nil {
			return RoleView{}, err
		}
	}
	afterAdmins, err := teamAdministratorCount(ctx, tx, team)
	if err != nil {
		return RoleView{}, err
	}
	if beforeAdmins > 0 && afterAdmins == 0 {
		return RoleView{}, ErrProtectedAccount
	}
	if current, e := loadAuthority(ctx, tx, actor, team); e == nil {
		if resource, e := resourceFrom(ctx, tx, team, "role", id); e == nil {
			next.CanManage, _ = current.Service.Allowed(current.Principal, "role.manage", resource)
		}
	}
	if err = tx.Commit(); err != nil {
		return RoleView{}, err
	}
	return next, nil
}
func (r *Repository) DeleteRole(ctx context.Context, actor Actor, team, id string, expected uint64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return err
	}
	if _, err = checkRoleManagement(ctx, tx, actor, team, id, false); err != nil {
		return err
	}
	role, err := readRole(ctx, tx, team, id)
	if err != nil {
		return err
	}
	if role.Deleted || role.Revision != expected {
		return ErrConflict
	}
	beforeAdmins, err := teamAdministratorCount(ctx, tx, team)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE access_roles SET deleted=1,revision=revision+1 WHERE tenant_id=? AND id=? AND revision=?`, team, id, expected); err != nil {
		return err
	}
	afterAdmins, err := teamAdministratorCount(ctx, tx, team)
	if err != nil {
		return err
	}
	if beforeAdmins > 0 && afterAdmins == 0 {
		return ErrProtectedAccount
	}
	return tx.Commit()
}

// Count effective full-team management, including custom roles and multiple
// roles per user. Merely naming a role administrator never qualifies it.
func teamAdministratorCount(ctx context.Context, tx *sql.Tx, team string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.user_id,b.scope_json,r.pages_json,r.grants_json FROM access_bindings b JOIN access_members m ON m.id=b.member_id AND m.tenant_id=b.tenant_id JOIN access_users u ON u.id=m.user_id JOIN access_roles r ON r.id=b.role_id AND r.tenant_id=b.tenant_id WHERE b.tenant_id=? AND m.enabled=1 AND u.enabled=1 AND r.deleted=0`, team)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	rights := map[string]map[string]bool{}
	for rows.Next() {
		var user, scopeBody, pageBody, grantBody string
		if err = rows.Scan(&user, &scopeBody, &pageBody, &grantBody); err != nil {
			return 0, err
		}
		var scope authz.Scope
		var pages []string
		var grants []authz.Grant
		if json.Unmarshal([]byte(scopeBody), &scope) != nil || json.Unmarshal([]byte(pageBody), &pages) != nil || json.Unmarshal([]byte(grantBody), &grants) != nil {
			return 0, authz.ErrInvalidPolicy
		}
		if scope.Kind != authz.ScopeTeam {
			continue
		}
		hasPage := map[string]bool{}
		for _, page := range pages {
			hasPage[page] = true
		}
		have := map[string]bool{}
		for _, grant := range grants {
			if grant.Scope.Kind == authz.ScopeTeam {
				have[grant.Action] = true
			}
		}
		if rights[user] == nil {
			rights[user] = map[string]bool{}
		}
		if hasPage["page.members"] && have["member.read"] && have["member.manage"] {
			rights[user]["members"] = true
		}
		if hasPage["page.roles"] && have["role.read"] && have["role.manage"] {
			rights[user]["roles"] = true
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, right := range rights {
		if right["members"] && right["roles"] {
			count++
		}
	}
	return count, nil
}

// An unchanged write grant can gain authority when its read prerequisite expands.
// Preserve safe label-only edits, but re-prove delegation for every affected
// grant whenever the complete prerequisite changes.
func readPrerequisiteUnchanged(old, next authz.Role, actionID string) bool {
	action, ok := authz.Lookup(actionID)
	if !ok {
		return false
	}
	if action.ReadAction == "" {
		return true
	}
	previous := []authz.Grant{}
	current := []authz.Grant{}
	for _, g := range old.Grants {
		if g.Action == action.ReadAction {
			previous = append(previous, g)
		}
	}
	for _, g := range next.Grants {
		if g.Action == action.ReadAction {
			current = append(current, g)
		}
	}
	return reflect.DeepEqual(previous, current)
}
