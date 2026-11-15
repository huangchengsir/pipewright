package authz

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	casbin "github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
)

var ErrInvalidPolicy = errors.New("authz: invalid policy")

// Principal and Resource must be loaded by trusted adapters. Epoch and revision
// freshness is checked against the authoritative snapshot on every request.
type Principal struct {
	ID, TenantID          string
	Active, InstanceAdmin bool
	ModeEpoch, Revision   uint64
}
type Binding struct {
	SubjectID, RoleID, TenantID string
	Limit                       Scope
}
type Snapshot struct {
	CatalogVersion      int
	ModeEpoch, Revision uint64
	Roles               []Role
	Bindings            []Binding
}

type Service struct {
	enforcer *casbin.SyncedEnforcer
	snapshot Snapshot
}

const fixedModel = `[request_definition]
r = sub, dom, role, act, grant
[policy_definition]
p = role, dom, act, grant
[role_definition]
g = _, _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = r.dom == p.dom && r.role == p.role && r.act == p.act && r.grant == p.grant && g(r.sub, p.role, r.dom)`

// New builds a private immutable snapshot; there is no caller-controlled matcher,
// wildcard, transitive role hierarchy, or mutation of the live enforcer.
func New(snapshot Snapshot) (*Service, error) {
	if snapshot.CatalogVersion != CatalogVersion || snapshot.ModeEpoch == 0 || snapshot.Revision == 0 {
		return nil, ErrInvalidPolicy
	}
	m, err := model.NewModelFromString(fixedModel)
	if err != nil {
		return nil, err
	}
	e, err := casbin.NewSyncedEnforcer(m)
	if err != nil {
		return nil, err
	}
	e.SetLogger(nil)
	s := &Service{enforcer: e, snapshot: Snapshot{CatalogVersion: snapshot.CatalogVersion, ModeEpoch: snapshot.ModeEpoch, Revision: snapshot.Revision}}
	type roleKey struct{ tenant, id string }
	keys := map[roleKey]bool{}
	for _, r := range snapshot.Roles {
		if r.ID == "" || r.TenantID == "" {
			return nil, ErrInvalidPolicy
		}
		key := roleKey{r.TenantID, r.ID}
		if keys[key] {
			return nil, ErrInvalidPolicy
		}
		keys[key] = true
		knownPages := map[string]bool{}
		for _, a := range Catalog() {
			if !a.InstanceOnly {
				knownPages[a.Page] = true
			}
		}
		seenPages := map[string]bool{}
		for _, page := range r.Pages {
			if !knownPages[page] || seenPages[page] {
				return nil, ErrInvalidPolicy
			}
			seenPages[page] = true
		}
		r.Pages = slices.Clone(r.Pages)
		grants := make([]Grant, 0, len(r.Grants))
		for i, g := range r.Grants {
			a, ok := Lookup(g.Action)
			if !ok || a.InstanceOnly || !g.Scope.valid() || !slices.Contains(r.Pages, a.Page) {
				return nil, ErrInvalidPolicy
			}
			if a.ReadAction != "" {
				found := false
				for _, prerequisite := range r.Grants {
					if prerequisite.Action == a.ReadAction {
						found = true
						break
					}
				}
				if !found {
					return nil, ErrInvalidPolicy
				}
			}
			g.Scope = copyScope(g.Scope)
			grants = append(grants, g)
			if !r.Deleted {
				if _, err = e.AddPolicy(r.ID, r.TenantID, g.Action, strconv.Itoa(i)); err != nil {
					return nil, err
				}
			}
		}
		r.Grants = grants
		s.snapshot.Roles = append(s.snapshot.Roles, r)
	}
	for _, b := range snapshot.Bindings {
		if b.SubjectID == "" || b.TenantID == "" || !keys[roleKey{b.TenantID, b.RoleID}] || !b.Limit.valid() || b.Limit.EnvironmentPolicy == "test" {
			return nil, ErrInvalidPolicy
		}
		for _, role := range s.snapshot.Roles {
			if role.ID == b.RoleID && role.TenantID == b.TenantID && role.RequiresSelectedBinding && b.Limit.Kind != ScopeSelected && b.Limit.Kind != ScopeNone {
				return nil, ErrInvalidPolicy
			}
		}
		b.Limit = copyScope(b.Limit)
		s.snapshot.Bindings = append(s.snapshot.Bindings, b)
		if _, err = e.AddGroupingPolicy("user:"+b.SubjectID, b.RoleID, b.TenantID); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Allowed requires one whole grant AND that member binding's restriction. It
// never combines an action from one role with a resource scope from another.
func (s *Service) Allowed(p Principal, actionID string, resource Resource) (bool, error) {
	a, known := Lookup(actionID)
	if s == nil || s.enforcer == nil || !known || !p.Active || p.ID == "" || p.ModeEpoch != s.snapshot.ModeEpoch || p.Revision != s.snapshot.Revision {
		return false, nil
	}
	if resource.ID == "" || resource.Kind != a.ResourceKind {
		return false, nil
	}
	if a.OwnerOnly && (resource.OwnerID != p.ID || resource.TenantID != p.TenantID) {
		return false, nil
	}
	if p.InstanceAdmin {
		return true, nil
	}
	if a.InstanceOnly || p.TenantID == "" || resource.TenantID != p.TenantID {
		return false, nil
	}
	for _, b := range s.snapshot.Bindings {
		if b.SubjectID != p.ID || b.TenantID != p.TenantID || !b.Limit.matches(p.ID, a, resource) {
			continue
		}
		for _, r := range s.snapshot.Roles {
			if r.ID != b.RoleID || r.TenantID != p.TenantID || r.Deleted || !slices.Contains(r.Pages, a.Page) {
				continue
			}
			if a.ReadAction != "" && !roleAllows(r, p.ID, a.ReadAction, resource) {
				continue
			}
			for i, g := range r.Grants {
				if g.Action != actionID || !g.Scope.matches(p.ID, a, resource) {
					continue
				}
				allowed, err := s.enforcer.Enforce("user:"+p.ID, p.TenantID, r.ID, actionID, strconv.Itoa(i))
				if err != nil {
					return false, fmt.Errorf("authz: enforce: %w", err)
				}
				if allowed {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
func roleAllows(r Role, user, action string, res Resource) bool {
	seen := map[string]bool{}
	for action != "" {
		if seen[action] {
			return false
		}
		seen[action] = true
		a, ok := Lookup(action)
		if !ok || a.ResourceKind != res.Kind || !slices.Contains(r.Pages, a.Page) {
			return false
		}
		matched := false
		for _, g := range r.Grants {
			if g.Action == action && g.Scope.matches(user, a, res) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
		action = a.ReadAction
	}
	return true
}

// HasAction is a navigation hint only. It never authorizes a concrete resource;
// all reads and writes still use full per-resource predicates/Allowed checks.
func (s *Service) HasAction(p Principal, id string) bool {
	action, known := Lookup(id)
	if s == nil || !known || action.InstanceOnly || !p.Active || p.ID == "" || p.TenantID == "" || p.ModeEpoch != s.snapshot.ModeEpoch || p.Revision != s.snapshot.Revision {
		return false
	}
	if p.InstanceAdmin {
		return true
	}
	possible := func(scope Scope) bool {
		if scope.Kind == ScopeNone {
			return false
		}
		if scope.Kind == ScopeSelected && len(scope.IDsForKind(action.ResourceKind)) == 0 && (!action.ProjectScoped || len(scope.ProjectIDs) == 0) {
			return false
		}
		return !action.EnvironmentRequired || scope.EnvironmentPolicy == "test" || len(scope.EnvironmentIDs) > 0
	}
	for _, binding := range s.snapshot.Bindings {
		if binding.SubjectID != p.ID || binding.TenantID != p.TenantID || !possible(binding.Limit) {
			continue
		}
		for _, role := range s.snapshot.Roles {
			if role.ID != binding.RoleID || role.TenantID != p.TenantID || role.Deleted || !slices.Contains(role.Pages, action.Page) {
				continue
			}
			for _, grant := range role.Grants {
				if grant.Action == id && possible(grant.Scope) {
					return true
				}
			}
		}
	}
	return false
}
