// Package authz evaluates explicit team-scoped permissions. Callers must resolve
// resource ownership from trusted storage, never from request-supplied claims.
package authz

import "slices"

type ScopeKind string

const (
	ScopeNone     ScopeKind = "none"
	ScopeTeam     ScopeKind = "team"
	ScopeSelected ScopeKind = "selected"
	ScopeOwn      ScopeKind = "own"
)

type Resource struct {
	TenantID      string
	Kind          string
	ID            string
	ProjectID     string
	EnvironmentID string
	// EnvironmentPurpose is resolved from explicit environment metadata.
	EnvironmentPurpose string
	OwnerID            string
}

// Scope binds its resource and environment restrictions to one action. Empty
// selected sets and empty environment sets never mean all resources.
type ResourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Scope struct {
	ResourceRefs      []ResourceRef `json:"resourceRefs,omitempty"`
	EnvironmentPolicy string        `json:"environmentPolicy,omitempty"`
	Kind              ScopeKind     `json:"kind"`
	ResourceIDs       []string      `json:"resourceIds,omitempty"`
	ProjectIDs        []string      `json:"projectIds,omitempty"`
	EnvironmentIDs    []string      `json:"environmentIds,omitempty"`
}

func (s Scope) valid() bool {
	if s.EnvironmentPolicy != "" && s.EnvironmentPolicy != "selected" && s.EnvironmentPolicy != "test" {
		return false
	}
	if s.Kind != ScopeNone && s.Kind != ScopeTeam && s.Kind != ScopeSelected && s.Kind != ScopeOwn {
		return false
	}
	for _, ref := range s.ResourceRefs {
		known := false
		for _, action := range Catalog() {
			known = known || (action.ResourceKind == ref.Kind && !action.InstanceOnly)
		}
		if !known || ref.ID == "" || ref.ID == "*" {
			return false
		}
	}
	for _, ids := range [][]string{s.ResourceIDs, s.ProjectIDs, s.EnvironmentIDs} {
		for _, id := range ids {
			if id == "" || id == "*" {
				return false
			}
		}
	}
	return true
}
func (s Scope) matches(user string, a Action, r Resource) bool {
	if !s.valid() || r.ID == "" || r.Kind != a.ResourceKind {
		return false
	}
	if a.EnvironmentRequired {
		if r.EnvironmentID == "" {
			return false
		}
		if s.EnvironmentPolicy == "test" {
			if r.EnvironmentPurpose != "test" || (len(s.EnvironmentIDs) > 0 && !slices.Contains(s.EnvironmentIDs, r.EnvironmentID)) {
				return false
			}
		} else if !slices.Contains(s.EnvironmentIDs, r.EnvironmentID) {
			return false
		}
	}
	switch s.Kind {
	case ScopeTeam:
		return true
	case ScopeSelected:
		return slices.Contains(s.IDsForKind(r.Kind), r.ID) || (a.ProjectScoped && r.ProjectID != "" && slices.Contains(s.ProjectIDs, r.ProjectID))
	case ScopeOwn:
		return user != "" && r.OwnerID == user
	default:
		return false
	}
}
func copyScope(s Scope) Scope {
	s.ResourceRefs = slices.Clone(s.ResourceRefs)
	s.ResourceIDs = slices.Clone(s.ResourceIDs)
	s.ProjectIDs = slices.Clone(s.ProjectIDs)
	s.EnvironmentIDs = slices.Clone(s.EnvironmentIDs)
	return s
}

// IDsForKind prevents a member binding from granting another resource kind that
// happens to have the same ID. Legacy untyped IDs remain scoped by each grant's
// fixed action kind; new member bindings reject untyped IDs at the boundary.
func (s Scope) IDsForKind(kind string) []string {
	ids := slices.Clone(s.ResourceIDs)
	for _, ref := range s.ResourceRefs {
		if ref.Kind == kind && !slices.Contains(ids, ref.ID) {
			ids = append(ids, ref.ID)
		}
	}
	return ids
}
