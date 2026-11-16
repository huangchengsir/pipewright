package authz

import "testing"

func TestDelegationPreservesSymbolicAndPerActionLimits(t *testing.T) {
	p := Principal{ID: "manager", TenantID: "team", Active: true, ModeEpoch: 1, Revision: 1}
	selected := Scope{Kind: ScopeSelected, ProjectIDs: []string{"p1", "p2"}, EnvironmentIDs: []string{"test"}}
	role := Role{ID: "role", TenantID: "team", Pages: []string{"page.runs", "page.environments"}, Grants: []Grant{{Action: "environment.read", Scope: Scope{Kind: ScopeTeam}}, {Action: "deployment.execute", Scope: Scope{Kind: ScopeTeam, EnvironmentPolicy: "test"}}}}
	service, err := New(Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: []Role{role}, Bindings: []Binding{{SubjectID: p.ID, TenantID: "team", RoleID: "role", Limit: selected}}})
	if err != nil {
		t.Fatal(err)
	}
	if service.CanDelegate(p, Grant{Action: "environment.read", Scope: Scope{Kind: ScopeTeam}}, nil) {
		t.Fatal("all current selected IDs promoted to future-team access")
	}
	if !service.CanDelegate(p, Grant{Action: "deployment.execute", Scope: selected}, map[string]string{"test": "test"}) {
		t.Fatal("valid narrowed test grant denied")
	}
	production := selected
	production.EnvironmentIDs = []string{"prod"}
	if service.CanDelegate(p, Grant{Action: "deployment.execute", Scope: production}, map[string]string{"prod": "production"}) {
		t.Fatal("production grant escaped binding")
	}
	if service.CanDelegate(p, Grant{Action: "deployment.execute", Scope: selected}, nil) {
		t.Fatal("unknown environment purpose trusted")
	}
	if service.CanDelegate(p, Grant{Action: "server.terminal", Scope: Scope{Kind: ScopeTeam}}, nil) {
		t.Fatal("undelegated terminal granted")
	}
	if service.CanDelegate(p, Grant{Action: "environment.read", Scope: Scope{Kind: ScopeOwn}}, nil) {
		t.Fatal("own scope of another subject exceeds selected projects")
	}
}
