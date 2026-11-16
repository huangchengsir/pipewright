package tenancy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type PreviewInput struct {
	MemberID    string     `json:"memberId"`
	Action      string     `json:"action"`
	ResourceID  string     `json:"resourceId"`
	DraftRoleID string     `json:"draftRoleId,omitempty"`
	Draft       *RoleInput `json:"draft,omitempty"`
}
type PreviewResult struct {
	CurrentAllowed bool   `json:"currentAllowed"`
	DraftAllowed   *bool  `json:"draftAllowed,omitempty"`
	Revision       uint64 `json:"revision"`
}

// Preview reports existing effective authorization and, optionally, a separate
// hypothetical role replacement. It neither grants access nor writes policy.
func (r *Repository) Preview(ctx context.Context, actor Actor, team string, in PreviewInput) (PreviewResult, error) {
	var out PreviewResult
	action, known := authz.Lookup(in.Action)
	if !known || action.InstanceOnly || in.MemberID == "" || in.ResourceID == "" || (in.Draft == nil) != (in.DraftRoleID == "") {
		return out, ErrInvalidInput
	}
	err := configstate.WithReadTx(ctx, r.db, func(q configstate.DBTX) error {
		viewer, err := loadAuthority(ctx, q, actor, team)
		if err != nil {
			return err
		}
		check := func(id string, res authz.Resource) error {
			yes, e := viewer.Service.Allowed(viewer.Principal, id, res)
			if e != nil {
				return e
			}
			if !yes {
				return ErrForbidden
			}
			return nil
		}
		member, err := resourceFrom(ctx, q, team, "member", in.MemberID)
		if err != nil {
			return err
		}
		if err = check("member.read", member); err != nil {
			return err
		}
		resource, err := resourceFrom(ctx, q, team, action.ResourceKind, in.ResourceID)
		if err != nil {
			return err
		}
		read := action.ReadAction
		if read == "" {
			read = action.ID
		}
		if err = check(read, resource); err != nil {
			return err
		}
		var subject User
		var enabled, admin, mustChange int
		if err = q.QueryRowContext(ctx, `SELECT u.id,u.enabled,u.instance_admin,u.must_change_password FROM access_users u JOIN access_members m ON m.user_id=u.id WHERE m.id=? AND m.tenant_id=? AND m.enabled=1`, in.MemberID, team).Scan(&subject.ID, &enabled, &admin, &mustChange); err != nil {
			return ErrForbidden
		}
		subject.Enabled = enabled == 1 && mustChange == 0
		subject.InstanceAdmin = admin == 1
		current, err := loadSubjectAuthority(ctx, q, subject, team, nil)
		if err != nil {
			return err
		}
		current.Principal.Active = subject.Enabled
		out.Revision = current.Principal.Revision
		out.CurrentAllowed, err = current.Service.Allowed(current.Principal, in.Action, resource)
		if err != nil {
			return err
		}
		if in.Draft == nil {
			return nil
		}
		roleResource, err := resourceFrom(ctx, q, team, "role", in.DraftRoleID)
		if err != nil {
			return err
		}
		if err = check("role.read", roleResource); err != nil {
			return err
		}
		var revision uint64
		if err = q.QueryRowContext(ctx, `SELECT revision FROM access_roles WHERE id=? AND tenant_id=? AND deleted=0`, in.DraftRoleID, team).Scan(&revision); err != nil {
			return ErrForbidden
		}
		if revision != in.Draft.ExpectedRevision {
			return ErrConflict
		}
		role := authz.Role{ID: in.DraftRoleID, TenantID: team, Pages: in.Draft.Pages, Grants: in.Draft.Grants, RequiresSelectedBinding: in.Draft.RequiresSelectedBinding}
		if _, err = validateRoleGrants(ctx, q, team, role); err != nil {
			return err
		}
		hypothetical, err := loadSubjectAuthority(ctx, q, subject, team, &role)
		if err != nil {
			return err
		}
		hypothetical.Principal.Active = subject.Enabled
		yes, err := hypothetical.Service.Allowed(hypothetical.Principal, in.Action, resource)
		if err != nil {
			return err
		}
		out.DraftAllowed = &yes
		return nil
	})
	return out, err
}
