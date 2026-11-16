package authz

const PresetVersion = 1

type Grant struct {
	Action string `json:"action"`
	Scope  Scope  `json:"scope"`
}
type Role struct {
	ID                      string   `json:"id"`
	TenantID                string   `json:"tenantId"`
	NameKey                 string   `json:"nameKey"`
	PresetKey               string   `json:"presetKey,omitempty"`
	SeedVersion             int      `json:"seedVersion"`
	RequiresSelectedBinding bool     `json:"requiresSelectedBinding"`
	Deleted                 bool     `json:"deleted"`
	Pages                   []string `json:"pages"`
	Grants                  []Grant  `json:"grants"`
}

// Presets returns fresh templates, not member assignments. Storage must seed
// these once per team and retain deletion markers across restart and migration.
// Member assignment requires selected resources for ordinary presets. Execution
// additionally requires an explicitly marked test environment and a binding that
// names that exact environment; no production access is inherited.
func Presets(tenantID string) []Role {
	read := []string{"project.read", "environment.read", "run.read", "guard.read", "dashboard.read", "template.read", "variable_group.read", "custom_node.read", "dora.read"}
	release := append(append([]string{}, read...), "run.start", "run.retry", "run.cancel", "deployment.execute", "release.promote", "guard.observe", "guard.stop", "guard.investigate")
	maintain := append(append([]string{}, release...), "project.edit", "pipeline.read", "pipeline.edit", "template.create", "variable_group.create", "variable_group.edit", "custom_node.create", "custom_node.edit", "artifact.download", "guard.analyze")
	admin := append(append([]string{}, maintain...), "project.create", "project.delete", "guard.delete", "template.delete", "variable_group.delete", "custom_node.delete", "server.inspect", "container.inspect", "credential.metadata", "member.read", "member.manage", "role.read", "role.manage", "audit.read", "preview.read", "certificate.read", "anomaly.read", "notification.read")
	specs := []struct {
		key  string
		ids  []string
		kind ScopeKind
	}{{"team-admin", admin, ScopeTeam}, {"maintainer", maintain, ScopeSelected}, {"publisher", release, ScopeSelected}, {"viewer", read, ScopeSelected}}
	roles := make([]Role, 0, len(specs))
	for _, s := range specs {
		r := Role{TenantID: tenantID, PresetKey: s.key, NameKey: "access.roles." + s.key, SeedVersion: PresetVersion, RequiresSelectedBinding: s.key != "team-admin"}
		seen := map[string]bool{}
		for _, id := range s.ids {
			a, _ := Lookup(id)
			if !seen[a.Page] {
				r.Pages = append(r.Pages, a.Page)
				seen[a.Page] = true
			}
			r.Grants = append(r.Grants, Grant{Action: id, Scope: Scope{Kind: ScopeTeam, EnvironmentPolicy: "test"}})
		}
		roles = append(roles, r)
	}
	return roles
}
