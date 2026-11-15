package authz

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Predicate is a parameterized SQL condition for a business query, including
// count and pagination. It never authorizes loading unfiltered rows into memory.
// Callers must use the trusted resource ID column and the same database/tx as the
// policy snapshot. The adapter additionally checks the live session and mode.
type Predicate struct {
	SQL  string
	Args []any
}

var rootColumn = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*$`)

// ValidRootColumn admits only qualified identifiers supplied by server code.
func ValidRootColumn(column string) bool { return rootColumn.MatchString(column) }

func deniedPredicate() Predicate { return Predicate{SQL: "1=0"} }
func (s *Service) RootPredicate(p Principal, actionID, column string) (Predicate, error) {
	a, known := Lookup(actionID)
	if s == nil || s.enforcer == nil || !known || !rootColumn.MatchString(column) || !p.Active || p.ID == "" || p.TenantID == "" || p.ModeEpoch != s.snapshot.ModeEpoch || p.Revision != s.snapshot.Revision {
		return deniedPredicate(), nil
	}
	if a.InstanceOnly && (!p.InstanceAdmin || a.ResourceKind == "instance") {
		return deniedPredicate(), nil
	} // Global instance operations have a separate gate; admin-owned resource actions still use ownership SQL.
	var branches []Predicate
	if p.InstanceAdmin {
		branches = append(branches, Predicate{SQL: "1=1"})
	} else {
		for _, b := range s.snapshot.Bindings {
			if b.SubjectID != p.ID || b.TenantID != p.TenantID {
				continue
			}
			for _, role := range s.snapshot.Roles {
				if role.ID != b.RoleID || role.TenantID != p.TenantID || role.Deleted || !slices.Contains(role.Pages, a.Page) {
					continue
				}
				prerequisite := Predicate{SQL: "1=1"}
				seen := map[string]bool{a.ID: true}
				for next := a.ReadAction; next != ""; {
					read, ok := Lookup(next)
					if !ok || seen[next] || read.ResourceKind != a.ResourceKind || !slices.Contains(role.Pages, read.Page) {
						prerequisite = deniedPredicate()
						break
					}
					seen[next] = true
					var candidates []Predicate
					for _, g := range role.Grants {
						if g.Action == read.ID {
							candidates = append(candidates, scopePredicate(g.Scope, p.ID, read))
						}
					}
					prerequisite = joinPredicates("AND", []Predicate{prerequisite, joinPredicates("OR", candidates)})
					next = read.ReadAction
				}
				for i, g := range role.Grants {
					if g.Action != actionID {
						continue
					}
					yes, err := s.enforcer.Enforce("user:"+p.ID, p.TenantID, role.ID, actionID, strconv.Itoa(i))
					if err != nil {
						return deniedPredicate(), err
					}
					if !yes {
						continue
					}
					branches = append(branches, joinPredicates("AND", []Predicate{scopePredicate(b.Limit, p.ID, a), scopePredicate(g.Scope, p.ID, a), prerequisite}))
				}
			}
		}
	}
	grant := joinPredicates("OR", branches)
	if a.OwnerOnly {
		grant = joinPredicates("AND", []Predicate{grant, {SQL: "az.owner_id=?", Args: []any{p.ID}}})
	}
	// Each business query checks the persisted revision. A revoked snapshot cannot
	// continue returning rows even if a caller accidentally retains it.
	args := []any{a.ResourceKind, p.TenantID, p.ModeEpoch, p.Revision, CatalogVersion}
	args = append(args, grant.Args...)
	if len(args) > 1800 {
		return deniedPredicate(), ErrInvalidPolicy
	}
	return Predicate{SQL: `EXISTS (SELECT 1 FROM access_ownership az LEFT JOIN access_environments aze ON aze.id=az.environment_id AND aze.tenant_id=az.tenant_id AND aze.project_id=az.project_id WHERE az.resource_kind=? AND az.resource_id=` + column + ` AND az.tenant_id=? AND EXISTS (SELECT 1 FROM access_control azc WHERE azc.id=1 AND azc.enabled=1 AND azc.mode_epoch=? AND azc.authz_revision=? AND azc.catalog_version=?) AND (` + grant.SQL + `))`, Args: args}, nil
}
func joinPredicates(operator string, ps []Predicate) Predicate {
	if len(ps) == 0 {
		return deniedPredicate()
	}
	out := Predicate{}
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		parts = append(parts, "("+p.SQL+")")
		out.Args = append(out.Args, p.Args...)
	}
	out.SQL = strings.Join(parts, " "+operator+" ")
	return out
}
func inPredicate(column string, ids []string) Predicate {
	if len(ids) == 0 {
		return deniedPredicate()
	}
	p := Predicate{SQL: column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"}
	for _, id := range ids {
		p.Args = append(p.Args, id)
	}
	return p
}
func scopePredicate(scope Scope, user string, action Action) Predicate {
	if !scope.valid() {
		return deniedPredicate()
	}
	var data Predicate
	switch scope.Kind {
	case ScopeTeam:
		data = Predicate{SQL: "1=1"}
	case ScopeOwn:
		data = Predicate{SQL: "az.owner_id=?", Args: []any{user}}
	case ScopeSelected:
		options := []Predicate{inPredicate("az.resource_id", scope.IDsForKind(action.ResourceKind))}
		if action.ProjectScoped {
			options = append(options, inPredicate("az.project_id", scope.ProjectIDs))
		}
		data = joinPredicates("OR", options)
	default:
		return deniedPredicate()
	}
	if !action.EnvironmentRequired {
		return data
	}
	var environment Predicate
	if scope.EnvironmentPolicy == "test" {
		environment = Predicate{SQL: "aze.purpose='test'"}
		if len(scope.EnvironmentIDs) > 0 {
			environment = joinPredicates("AND", []Predicate{environment, inPredicate("az.environment_id", scope.EnvironmentIDs)})
		}
	} else {
		environment = inPredicate("az.environment_id", scope.EnvironmentIDs)
	}
	return joinPredicates("AND", []Predicate{data, environment})
}
