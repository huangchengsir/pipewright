package tenancy

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// Authority contains a single request's policy snapshot. Do not retain it across
// requests or use it after a policy mutation; mutations must recheck under their
// transaction/fence. The HTTP/MCP adapters are responsible for explicit team IDs.
type Authority struct {
	Principal authz.Principal
	Service   *authz.Service
}

func (r *Repository) Authority(ctx context.Context, actor Actor, teamID string) (Authority, error) {
	var out Authority
	err := configstate.WithReadTx(ctx, r.db, func(q configstate.DBTX) error {
		var err error
		out, err = loadAuthority(ctx, q, actor, teamID)
		return err
	})
	return out, err
}
func loadAuthority(ctx context.Context, tx configstate.DBTX, actor Actor, teamID string) (Authority, error) {
	user, err := verifyActor(ctx, tx, actor, false)
	if err != nil {
		return Authority{}, err
	}
	return loadSubjectAuthority(ctx, tx, user, teamID, nil)
}

// loadSubjectAuthority is internal: callers must authenticate the requesting
// actor and authorize access to this subject before using it for inspection.
func loadSubjectAuthority(ctx context.Context, tx configstate.DBTX, user User, teamID string, draft *authz.Role) (Authority, error) {
	var err error
	var teamEnabled int
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM access_teams WHERE id=?`, teamID).Scan(&teamEnabled); err != nil || teamEnabled != 1 {
		return Authority{}, ErrForbidden
	}
	if !user.InstanceAdmin {
		var memberEnabled int
		if err = tx.QueryRowContext(ctx, `SELECT enabled FROM access_members WHERE tenant_id=? AND user_id=?`, teamID, user.ID).Scan(&memberEnabled); err != nil || memberEnabled != 1 {
			return Authority{}, ErrForbidden
		}
	}
	snapshot := authz.Snapshot{}
	if err = tx.QueryRowContext(ctx, `SELECT mode_epoch,authz_revision,catalog_version FROM access_control WHERE id=1`).Scan(&snapshot.ModeEpoch, &snapshot.Revision, &snapshot.CatalogVersion); err != nil {
		return Authority{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name,preset_key,seed_version,requires_selected_binding,deleted,pages_json,grants_json FROM access_roles WHERE tenant_id=?`, teamID)
	if err != nil {
		return Authority{}, err
	}
	for rows.Next() {
		var role authz.Role
		var selected, deleted int
		var pages, grants string
		role.TenantID = teamID
		if err = rows.Scan(&role.ID, &role.NameKey, &role.PresetKey, &role.SeedVersion, &selected, &deleted, &pages, &grants); err != nil {
			rows.Close()
			return Authority{}, err
		}
		role.RequiresSelectedBinding = selected == 1
		role.Deleted = deleted == 1
		if json.Unmarshal([]byte(pages), &role.Pages) != nil || json.Unmarshal([]byte(grants), &role.Grants) != nil {
			rows.Close()
			return Authority{}, authz.ErrInvalidPolicy
		}
		snapshot.Roles = append(snapshot.Roles, role)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Authority{}, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT b.role_id,b.scope_json FROM access_bindings b JOIN access_members m ON m.id=b.member_id AND m.tenant_id=b.tenant_id JOIN access_roles r ON r.id=b.role_id AND r.tenant_id=b.tenant_id WHERE b.tenant_id=? AND m.user_id=? AND m.enabled=1 AND r.deleted=0`, teamID, user.ID)
	if err != nil {
		return Authority{}, err
	}
	for rows.Next() {
		b := authz.Binding{TenantID: teamID, SubjectID: user.ID}
		var body string
		if err = rows.Scan(&b.RoleID, &body); err != nil {
			rows.Close()
			return Authority{}, err
		}
		if json.Unmarshal([]byte(body), &b.Limit) != nil {
			rows.Close()
			return Authority{}, authz.ErrInvalidPolicy
		}
		snapshot.Bindings = append(snapshot.Bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Authority{}, err
	}
	if draft != nil {
		found := false
		for i := range snapshot.Roles {
			if snapshot.Roles[i].ID == draft.ID && snapshot.Roles[i].TenantID == draft.TenantID {
				snapshot.Roles[i] = *draft
				found = true
			}
		}
		if !found {
			return Authority{}, ErrForbidden
		}
	}
	service, err := authz.New(snapshot)
	if err != nil {
		return Authority{}, err
	}
	return Authority{Principal: authz.Principal{ID: user.ID, TenantID: teamID, Active: true, InstanceAdmin: user.InstanceAdmin, ModeEpoch: snapshot.ModeEpoch, Revision: snapshot.Revision}, Service: service}, nil
}

// Resource reads trusted ownership and explicit environment purpose. Missing or
// wrong-team rows are indistinguishable. Callers cannot supply owner/purpose.
func (r *Repository) Resource(ctx context.Context, teamID, kind, id string) (authz.Resource, error) {
	return resourceFrom(ctx, configstate.Queryer(ctx, r.db), teamID, kind, id)
}
func resourceFrom(ctx context.Context, q configstate.DBTX, teamID, kind, id string) (authz.Resource, error) {
	out := authz.Resource{TenantID: teamID, Kind: kind, ID: id}
	var owner, project, environment, purpose sql.NullString
	err := q.QueryRowContext(ctx, `SELECT o.owner_id,o.project_id,o.environment_id,e.purpose FROM access_ownership o LEFT JOIN access_environments e ON e.id=o.environment_id AND e.tenant_id=o.tenant_id AND e.project_id=o.project_id WHERE o.tenant_id=? AND o.resource_kind=? AND o.resource_id=?`, teamID, kind, id).Scan(&owner, &project, &environment, &purpose)
	if err != nil {
		return authz.Resource{}, ErrForbidden
	}
	out.OwnerID = owner.String
	out.ProjectID = project.String
	out.EnvironmentID = environment.String
	out.EnvironmentPurpose = purpose.String
	return out, nil
}
