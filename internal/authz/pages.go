package authz

import "slices"

// Pages projects menu membership only. It never grants an action or resource.
func (s *Service) Pages(p Principal) []string {
	out := []string{}
	if s == nil || !p.Active || p.ID == "" || p.TenantID == "" || p.ModeEpoch != s.snapshot.ModeEpoch || p.Revision != s.snapshot.Revision {
		return out
	}
	add := func(page string) {
		if !slices.Contains(out, page) {
			out = append(out, page)
		}
	}
	if p.InstanceAdmin {
		for _, a := range Catalog() {
			add(a.Page)
		}
		slices.Sort(out)
		return out
	}
	for _, b := range s.snapshot.Bindings {
		if b.SubjectID != p.ID || b.TenantID != p.TenantID {
			continue
		}
		for _, r := range s.snapshot.Roles {
			if r.ID == b.RoleID && r.TenantID == p.TenantID && !r.Deleted {
				for _, page := range r.Pages {
					add(page)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
