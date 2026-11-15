package authz

import "slices"

// CanDelegate checks symbolic upper bounds, not an enumeration of currently
// existing resources. Selected IDs never imply permission over future projects.
// environmentPurposes must be read from trusted access_environments storage.
func (s *Service) CanDelegate(p Principal, wanted Grant, environmentPurposes map[string]string) bool {
	action, ok := Lookup(wanted.Action)
	if s == nil || !ok || action.InstanceOnly || !wanted.Scope.valid() || !p.Active || p.ID == "" || p.ModeEpoch != s.snapshot.ModeEpoch || p.Revision != s.snapshot.Revision {
		return false
	}
	if p.InstanceAdmin {
		return true
	}
	if wanted.Scope.Kind == ScopeNone {
		return true
	}
	for _, binding := range s.snapshot.Bindings {
		if binding.SubjectID != p.ID || binding.TenantID != p.TenantID || !scopeContained(wanted.Scope, binding.Limit, action, environmentPurposes) {
			continue
		}
		for _, role := range s.snapshot.Roles {
			if role.ID != binding.RoleID || role.TenantID != p.TenantID || role.Deleted || !slices.Contains(role.Pages, action.Page) {
				continue
			}
			readOK := true
			seen := map[string]bool{action.ID: true}
			for next := action.ReadAction; next != ""; {
				read, known := Lookup(next)
				if !known || seen[next] || read.ResourceKind != action.ResourceKind || !slices.Contains(role.Pages, read.Page) {
					readOK = false
					break
				}
				seen[next] = true
				matched := false
				for _, grant := range role.Grants {
					if grant.Action == read.ID && scopeContained(wanted.Scope, grant.Scope, read, environmentPurposes) {
						matched = true
						break
					}
				}
				if !matched {
					readOK = false
					break
				}
				next = read.ReadAction
			}
			if !readOK {
				continue
			}
			for _, grant := range role.Grants {
				if grant.Action == wanted.Action && scopeContained(wanted.Scope, grant.Scope, action, environmentPurposes) {
					return true
				}
			}
		}
	}
	return false
}
func scopeContained(wanted, ceiling Scope, action Action, purposes map[string]string) bool {
	if !wanted.valid() || !ceiling.valid() {
		return false
	}
	if wanted.Kind == ScopeNone {
		return true
	}
	if ceiling.Kind == ScopeNone {
		return false
	}
	if action.EnvironmentRequired && len(wanted.EnvironmentIDs) == 0 && wanted.EnvironmentPolicy != "test" {
		return true
	} // Grants no environment.
	switch ceiling.Kind {
	case ScopeTeam:
	case ScopeSelected:
		if wanted.Kind != ScopeSelected || !subset(wanted.IDsForKind(action.ResourceKind), ceiling.IDsForKind(action.ResourceKind)) || !subset(wanted.ProjectIDs, ceiling.ProjectIDs) {
			return false
		}
	case ScopeOwn:
		// Own refers to the grantee, so copying it to another subject is not proven
		// to be inside this administrator's own resources.
		return false
	default:
		return false
	}
	if !action.EnvironmentRequired {
		return true
	}
	if ceiling.EnvironmentPolicy == "test" {
		if wanted.EnvironmentPolicy != "test" {
			for _, id := range wanted.EnvironmentIDs {
				if purposes[id] != "test" {
					return false
				}
			}
		}
		if len(ceiling.EnvironmentIDs) > 0 {
			return len(wanted.EnvironmentIDs) > 0 && subset(wanted.EnvironmentIDs, ceiling.EnvironmentIDs)
		}
		return true
	}
	return len(wanted.EnvironmentIDs) > 0 && subset(wanted.EnvironmentIDs, ceiling.EnvironmentIDs)
}
func subset(wanted, ceiling []string) bool {
	for _, v := range wanted {
		if !slices.Contains(ceiling, v) {
			return false
		}
	}
	return true
}
