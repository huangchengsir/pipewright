package tenancy

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type TeamView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision uint64 `json:"revision"`
}
type MemberDetail struct {
	MemberView
	Username       string `json:"username"`
	DisplayName    string `json:"displayName"`
	AccountEnabled bool   `json:"accountEnabled"`
	CanManage      bool   `json:"canManage"`
}
type ResourceChoice struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
	Purpose   string `json:"purpose,omitempty"`
}
type ResourceChoices struct {
	Resources    []ResourceChoice `json:"resources"`
	Projects     []ResourceChoice `json:"projects"`
	Environments []ResourceChoice `json:"environments"`
}

func (r *Repository) Teams(ctx context.Context, actor Actor) ([]TeamView, error) {
	out := []TeamView{}
	err := configstate.WithReadTx(ctx, r.db, func(q configstate.DBTX) error {
		user, err := verifyActor(ctx, q, actor, false)
		if err != nil {
			return err
		}
		filter := `EXISTS (SELECT 1 FROM access_members m WHERE m.tenant_id=t.id AND m.user_id=? AND m.enabled=1)`
		args := []any{user.ID}
		if user.InstanceAdmin {
			filter = "1=1"
			args = nil
		}
		rows, err := q.QueryContext(ctx, `SELECT t.id,t.name,t.revision FROM access_teams t WHERE t.enabled=1 AND `+filter+` ORDER BY t.name,t.id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var team TeamView
			if err = rows.Scan(&team.ID, &team.Name, &team.Revision); err != nil {
				return err
			}
			out = append(out, team)
		}
		return rows.Err()
	})
	return out, err
}
func (r *Repository) CreateTeamAs(ctx context.Context, actor Actor, name string) (TeamView, error) {
	if !validName(name) {
		return TeamView{}, ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamView{}, err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return TeamView{}, err
	}
	if _, err = verifyActor(ctx, tx, actor, true); err != nil {
		return TeamView{}, err
	}
	out := TeamView{ID: uuid.NewString(), Name: name, Revision: 1}
	if _, err = tx.ExecContext(ctx, `INSERT INTO access_teams(id,name,enabled,revision,created_at) VALUES(?,?,1,1,?)`, out.ID, name, time.Now().Unix()); err != nil {
		return TeamView{}, err
	}
	if err = seedRoles(ctx, tx, out.ID); err != nil {
		return TeamView{}, err
	}
	if err = tx.Commit(); err != nil {
		return TeamView{}, err
	}
	return out, nil
}
func (r *Repository) ReadRoles(ctx context.Context, actor Actor, team string) ([]RoleView, error) {
	out := []RoleView{}
	ctx = WithActor(ctx, actor, team)
	err := configstate.WithReadTxContext(ctx, r.db, func(txctx context.Context, q configstate.DBTX) error {
		predicate, err := QueryPredicate(txctx, r.db, "role.read", "r.id")
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(txctx, `SELECT r.id,r.name,r.preset_key,r.seed_version,r.requires_selected_binding,r.revision,r.pages_json,r.grants_json FROM access_roles r WHERE r.tenant_id=? AND r.deleted=0 AND `+predicate.SQL+` ORDER BY r.name,r.id`, append([]any{team}, predicate.Args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v RoleView
			var selected int
			var pages, grants string
			v.TenantID = team
			if err = rows.Scan(&v.ID, &v.NameKey, &v.PresetKey, &v.SeedVersion, &selected, &v.Revision, &pages, &grants); err != nil {
				rows.Close()
				return err
			}
			v.RequiresSelectedBinding = selected == 1
			if json.Unmarshal([]byte(pages), &v.Pages) != nil || json.Unmarshal([]byte(grants), &v.Grants) != nil {
				rows.Close()
				return authz.ErrInvalidPolicy
			}
			out = append(out, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		authority, err := loadAuthority(txctx, q, actor, team)
		if err != nil {
			return err
		}
		for i := range out {
			resource, e := resourceFrom(txctx, q, team, "role", out[i].ID)
			if e != nil {
				return e
			}
			out[i].CanManage, e = authority.Service.Allowed(authority.Principal, "role.manage", resource)
			if e != nil {
				return e
			}
		}
		return nil
	})
	return out, err
}
func (r *Repository) ReadMembers(ctx context.Context, actor Actor, team string) ([]MemberDetail, error) {
	out := []MemberDetail{}
	ctx = WithActor(ctx, actor, team)
	err := configstate.WithReadTxContext(ctx, r.db, func(txctx context.Context, q configstate.DBTX) error {
		predicate, err := QueryPredicate(txctx, r.db, "member.read", "m.id")
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(txctx, `SELECT m.id,m.user_id,m.enabled,m.revision,u.username,u.display_name,u.enabled FROM access_members m JOIN access_users u ON u.id=m.user_id WHERE m.tenant_id=? AND `+predicate.SQL+` ORDER BY u.username,m.id`, append([]any{team}, predicate.Args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v MemberDetail
			var enabled, userEnabled int
			v.TeamID = team
			v.Bindings = []MemberRoleInput{}
			if err = rows.Scan(&v.ID, &v.UserID, &enabled, &v.Revision, &v.Username, &v.DisplayName, &userEnabled); err != nil {
				rows.Close()
				return err
			}
			v.Enabled = enabled == 1
			v.AccountEnabled = userEnabled == 1
			out = append(out, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		authority, err := loadAuthority(txctx, q, actor, team)
		if err != nil {
			return err
		}
		for i := range out {
			resource, e := resourceFrom(txctx, q, team, "member", out[i].ID)
			if e != nil {
				return e
			}
			out[i].CanManage, e = authority.Service.Allowed(authority.Principal, "member.manage", resource)
			if e != nil {
				return e
			}
			bindings, e := q.QueryContext(txctx, `SELECT role_id,scope_json FROM access_bindings WHERE tenant_id=? AND member_id=? ORDER BY role_id`, team, out[i].ID)
			if e != nil {
				return e
			}
			for bindings.Next() {
				var b MemberRoleInput
				var body string
				if e = bindings.Scan(&b.RoleID, &body); e != nil {
					bindings.Close()
					return e
				}
				if json.Unmarshal([]byte(body), &b.Scope) != nil {
					bindings.Close()
					return authz.ErrInvalidPolicy
				}
				out[i].Bindings = append(out[i].Bindings, b)
			}
			e = bindings.Err()
			bindings.Close()
			if e != nil {
				return e
			}
		}
		return nil
	})
	return out, err
}
func (r *Repository) Choices(ctx context.Context, actor Actor, team string) (ResourceChoices, error) {
	out := ResourceChoices{Projects: []ResourceChoice{}, Environments: []ResourceChoice{}, Resources: []ResourceChoice{}}
	ctx = WithActor(ctx, actor, team)
	err := configstate.WithReadTxContext(ctx, r.db, func(txctx context.Context, q configstate.DBTX) error {
		predicate, err := QueryPredicate(txctx, r.db, "project.read", "p.id")
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(txctx, `SELECT p.id,p.name FROM projects p WHERE `+predicate.SQL+` ORDER BY p.name,p.id`, predicate.Args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var choice ResourceChoice
			if err = rows.Scan(&choice.ID, &choice.Name); err != nil {
				rows.Close()
				return err
			}
			out.Projects = append(out.Projects, choice)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		predicate, err = QueryPredicate(txctx, r.db, "environment.read", "e.id")
		if err != nil {
			return err
		}
		rows, err = q.QueryContext(txctx, `SELECT e.id,e.name,e.project_id,e.purpose FROM access_environments e WHERE e.tenant_id=? AND `+predicate.SQL+` ORDER BY e.project_id,e.name,e.id`, append([]any{team}, predicate.Args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var choice ResourceChoice
			if err = rows.Scan(&choice.ID, &choice.Name, &choice.ProjectID, &choice.Purpose); err != nil {
				return err
			}
			out.Environments = append(out.Environments, choice)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var resourceErr error
		out.Resources, resourceErr = r.resourceChoices(txctx, q)
		return resourceErr
	})
	return out, err
}

// LookupExisting is exact-match only. A team manager can add a known account,
// but cannot enumerate unrelated global identities through a search endpoint.
func (r *Repository) LookupExisting(ctx context.Context, actor Actor, team, username string) (User, error) {
	if !validUsername(username) {
		return User{}, ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	authority, err := loadAuthority(ctx, tx, actor, team)
	if err != nil {
		return User{}, err
	}
	yes, err := authority.Service.Allowed(authority.Principal, "member.manage", authz.Resource{TenantID: team, Kind: "member", ID: "new-member"})
	if err != nil {
		return User{}, err
	}
	if !yes {
		return User{}, ErrForbidden
	}
	limit := 12
	if authority.Principal.InstanceAdmin {
		limit = 60
	}
	if retry := r.lookupBudget.Allow(actor.UserID, limit); retry > 0 {
		return User{}, &auth.ErrLockedOut{RetryAfter: retry}
	}
	var user User
	if err = tx.QueryRowContext(ctx, `SELECT id,username,display_name FROM access_users WHERE username_key=? AND enabled=1`, strings.ToLower(username)).Scan(&user.ID, &user.Username, &user.DisplayName); err != nil {
		return User{}, ErrInvalidInput
	}
	user.Enabled = true
	return user, nil
}
func (r *Repository) SetEnvironmentPurpose(ctx context.Context, actor Actor, team, id, purpose string) error {
	if purpose != "test" && purpose != "production" && purpose != "other" {
		return ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return err
	}
	if _, err = verifyActor(ctx, tx, actor, true); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE access_environments SET purpose=?,revision=revision+1 WHERE id=? AND tenant_id=?`, purpose, id, team)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	return tx.Commit()
}

// ManagementCapabilities describes broad management operations. Per-resource
// authorization remains authoritative in each write and query service.
func (r *Repository) ManagementCapabilities(ctx context.Context, actor Actor, team string) (map[string]bool, error) {
	out := map[string]bool{}
	err := configstate.WithReadTx(ctx, r.db, func(q configstate.DBTX) error {
		a, err := loadAuthority(ctx, q, actor, team)
		if err != nil {
			return err
		}
		for _, action := range []string{"role.manage", "member.manage"} {
			definition, _ := authz.Lookup(action)
			allowed, err := a.Service.Allowed(a.Principal, action, authz.Resource{TenantID: team, Kind: definition.ResourceKind, ID: "new-resource"})
			if err != nil {
				return err
			}
			out[action] = allowed
		}
		for _, id := range []string{"role.read", "member.read", "project.read", "environment.read"} {
			out[id] = a.Service.HasAction(a.Principal, id)
		}
		for _, action := range authz.Catalog() {
			if !action.Sensitive && action.ReadAction == "" && a.Service.HasAction(a.Principal, action.ID) {
				out["choices.read"] = true
			}
		}
		return nil
	})
	return out, err
}

// resourceChoices projects only safe identifiers and labels through SQL-level
// read predicates. Secret payloads and unrelated team rows are never loaded.
func (r *Repository) resourceChoices(ctx context.Context, q configstate.DBTX) ([]ResourceChoice, error) {
	out := []ResourceChoice{}
	byKind := map[string][]authz.Action{}
	for _, action := range authz.Catalog() {
		if !action.InstanceOnly && !action.Sensitive && action.ReadAction == "" && !action.EnvironmentRequired && action.ResourceKind != "project" && action.ResourceKind != "environment" {
			byKind[action.ResourceKind] = append(byKind[action.ResourceKind], action)
		}
	}
	for kind, actions := range byKind {
		clauses := []string{}
		args := []any{kind}
		for _, action := range actions {
			predicate, err := QueryPredicate(ctx, r.db, action.ID, "o.resource_id")
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, "("+predicate.SQL+")")
			args = append(args, predicate.Args...)
		}
		label := "o.resource_id"
		ancestor := ""
		switch kind {
		case "business_project":
			label = "COALESCE((SELECT b.name FROM workspace_business_projects b WHERE b.id=o.resource_id),o.resource_id)"
		case "product":
			label = "COALESCE((SELECT p.name FROM workspace_products p WHERE p.id=o.resource_id),o.resource_id)"
			predicate, err := QueryPredicate(ctx, r.db, "business_project.read", "b.id")
			if err != nil {
				return nil, err
			}
			ancestor = ` AND EXISTS (SELECT 1 FROM workspace_products p JOIN workspace_business_projects b
				ON b.id=p.business_project_id WHERE p.id=o.resource_id AND (` + predicate.SQL + `))`
			args = append(args, predicate.Args...)

		case "template":
			label = "COALESCE((SELECT l.name FROM pipeline_templates l WHERE l.id=o.resource_id),o.resource_id)"
		case "variable_group":
			label = "COALESCE((SELECT l.name FROM variable_groups l WHERE l.id=o.resource_id),o.resource_id)"
		case "custom_node":
			label = "COALESCE((SELECT l.name FROM custom_nodes l WHERE l.id=o.resource_id),o.resource_id)"
		case "server":
			label = "COALESCE((SELECT s.name FROM servers s WHERE s.id=o.resource_id),o.resource_id)"
		case "notification_channel":
			label = "COALESCE((SELECT c.name FROM notification_channels c WHERE c.id=o.resource_id),o.resource_id)"
		case "dns_provider":
			label = "COALESCE((SELECT d.name FROM dns_providers d WHERE d.id=o.resource_id),o.resource_id)"
		case "credential":
			label = "COALESCE((SELECT c.name FROM credentials c WHERE c.id=o.resource_id),o.resource_id)"
		case "kubernetes_credential":
			label = "COALESCE((SELECT c.name FROM k8s_credentials c WHERE c.id=o.resource_id),o.resource_id)"
		case "kubernetes":
			label = "COALESCE((SELECT k.connection_json FROM k8s_connections k WHERE k.id=o.resource_id),'{}')"
		case "role":
			label = "COALESCE((SELECT ar.name FROM access_roles ar WHERE ar.id=o.resource_id AND ar.deleted=0),o.resource_id)"
		case "member":
			label = "COALESCE((SELECT u.display_name FROM access_members m JOIN access_users u ON u.id=m.user_id WHERE m.id=o.resource_id),o.resource_id)"
		}
		rows, err := q.QueryContext(ctx, `SELECT o.resource_id,`+label+` FROM access_ownership o WHERE o.resource_kind=? AND (`+strings.Join(clauses, " OR ")+`)`+ancestor+` ORDER BY o.resource_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v := ResourceChoice{Kind: kind}
			if err = rows.Scan(&v.ID, &v.Name); err != nil {
				rows.Close()
				return nil, err
			}
			if kind == "kubernetes" {
				var named struct {
					Name string `json:"name"`
				}
				if json.Unmarshal([]byte(v.Name), &named) != nil {
					rows.Close()
					return nil, ErrConflict
				}
				if named.Name == "" {
					continue
				}
				v.Name = named.Name
			}
			out = append(out, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
