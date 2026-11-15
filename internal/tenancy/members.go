package tenancy

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
)

type MemberRoleInput struct {
	RoleID string      `json:"roleId"`
	Scope  authz.Scope `json:"scope"`
}
type MemberInput struct {
	UserID           string            `json:"userId"`
	Enabled          bool              `json:"enabled"`
	Bindings         []MemberRoleInput `json:"bindings"`
	ExpectedRevision uint64            `json:"expectedRevision"`
}
type MemberView struct {
	ID       string            `json:"id"`
	TeamID   string            `json:"teamId"`
	UserID   string            `json:"userId"`
	Enabled  bool              `json:"enabled"`
	Revision uint64            `json:"revision"`
	Bindings []MemberRoleInput `json:"bindings"`
}

func (r *Repository) SaveMember(ctx context.Context, actor Actor, team, id string, in MemberInput) (MemberView, error) {
	if in.UserID == "" || len(in.Bindings) > 32 {
		return MemberView{}, ErrInvalidInput
	}
	creating := id == ""
	if creating {
		id = uuid.NewString()
		if in.ExpectedRevision != 0 {
			return MemberView{}, ErrConflict
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return MemberView{}, err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return MemberView{}, err
	}
	authority, err := loadAuthority(ctx, tx, actor, team)
	if err != nil {
		return MemberView{}, err
	}
	resource := authz.Resource{TenantID: team, Kind: "member", ID: id}
	if !creating {
		resource, err = resourceFrom(ctx, tx, team, "member", id)
		if err != nil {
			return MemberView{}, err
		}
	}
	yes, err := authority.Service.Allowed(authority.Principal, "member.manage", resource)
	if err != nil {
		return MemberView{}, err
	}
	if !yes {
		return MemberView{}, ErrForbidden
	}
	var userEnabled int
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM access_users WHERE id=?`, in.UserID).Scan(&userEnabled); err != nil || userEnabled != 1 {
		return MemberView{}, ErrInvalidInput
	}
	var oldRevision uint64
	if !creating {
		var userID string
		if err = tx.QueryRowContext(ctx, `SELECT user_id,revision FROM access_members WHERE tenant_id=? AND id=?`, team, id).Scan(&userID, &oldRevision); err != nil {
			return MemberView{}, ErrForbidden
		}
		if userID != in.UserID || oldRevision != in.ExpectedRevision {
			return MemberView{}, ErrConflict
		}
	}
	seen := map[string]bool{}
	for _, binding := range in.Bindings {
		if seen[binding.RoleID] {
			return MemberView{}, ErrInvalidInput
		}
		seen[binding.RoleID] = true
		role, e := assignmentRole(ctx, tx, in.UserID, MemberAssignment{TeamID: team, RoleID: binding.RoleID, Scope: binding.Scope})
		if e != nil {
			return MemberView{}, e
		}
		purposes, e := validateRoleGrants(ctx, tx, team, role)
		if e != nil {
			return MemberView{}, e
		}
		for _, env := range binding.Scope.EnvironmentIDs {
			var purpose string
			if e = tx.QueryRowContext(ctx, `SELECT purpose FROM access_environments WHERE id=? AND tenant_id=?`, env, team).Scan(&purpose); e != nil {
				return MemberView{}, ErrInvalidInput
			}
			purposes[env] = purpose
		}
		for _, grant := range role.Grants {
			// Effective rights are role grant INTERSECT binding limit. Proving either
			// whole bound is below the actor's authority is sufficient, never a union.
			bounded := authz.Grant{Action: grant.Action, Scope: binding.Scope}
			if !authority.Service.CanDelegate(authority.Principal, grant, purposes) && !authority.Service.CanDelegate(authority.Principal, bounded, purposes) {
				return MemberView{}, ErrForbidden
			}
		}
	}
	before, err := teamAdministratorCount(ctx, tx, team)
	if err != nil {
		return MemberView{}, err
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	if creating {
		_, err = tx.ExecContext(ctx, `INSERT INTO access_members(id,tenant_id,user_id,enabled,revision) VALUES(?,?,?,?,1)`, id, team, in.UserID, enabled)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE access_members SET enabled=?,revision=revision+1 WHERE tenant_id=? AND id=? AND revision=?`, enabled, team, id, in.ExpectedRevision)
	}
	if err != nil {
		return MemberView{}, ErrAccountConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_bindings WHERE tenant_id=? AND member_id=?`, team, id); err != nil {
		return MemberView{}, err
	}
	for _, binding := range in.Bindings {
		body, _ := json.Marshal(binding.Scope)
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_bindings(id,tenant_id,member_id,role_id,scope_json) VALUES(?,?,?,?,?)`, uuid.NewString(), team, id, binding.RoleID, string(body)); err != nil {
			return MemberView{}, err
		}
	}
	if creating {
		if _, err = tx.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('member',?,?,?)`, id, team, in.UserID); err != nil {
			return MemberView{}, err
		}
	}
	after, err := teamAdministratorCount(ctx, tx, team)
	if err != nil {
		return MemberView{}, err
	}
	if before > 0 && after == 0 {
		return MemberView{}, ErrProtectedAccount
	}
	if err = tx.Commit(); err != nil {
		return MemberView{}, err
	}
	return MemberView{ID: id, TeamID: team, UserID: in.UserID, Enabled: in.Enabled, Revision: oldRevision + 1, Bindings: in.Bindings}, nil
}
