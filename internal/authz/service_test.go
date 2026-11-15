package authz

import (
	"errors"
	"slices"
	"testing"
)

func fixture(t *testing.T) (*Service, Principal, Resource, Snapshot) {
	t.Helper()
	selected := Scope{Kind: ScopeSelected, ProjectIDs: []string{"project-a"}, EnvironmentIDs: []string{"test"}}
	snapshot := Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 7,
		Roles: []Role{
			{ID: "reader", TenantID: "team-a", Pages: []string{"page.runs"}, Grants: []Grant{{Action: "run.read", Scope: Scope{Kind: ScopeTeam}}}},
			{ID: "publisher", TenantID: "team-a", Pages: []string{"page.runs"}, Grants: []Grant{{Action: "run.read", Scope: selected}, {Action: "run.start", Scope: selected}}},
		}, Bindings: []Binding{{SubjectID: "alice", RoleID: "reader", TenantID: "team-a", Limit: Scope{Kind: ScopeTeam}}, {SubjectID: "alice", RoleID: "publisher", TenantID: "team-a", Limit: Scope{Kind: ScopeTeam, EnvironmentIDs: []string{"test", "prod"}}}}}
	service, err := New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return service, Principal{ID: "alice", TenantID: "team-a", Active: true, ModeEpoch: 1, Revision: 7}, Resource{TenantID: "team-a", Kind: "run", ID: "run-1", ProjectID: "project-a", EnvironmentID: "test"}, snapshot
}
func assertAllowed(t *testing.T, s *Service, p Principal, a string, r Resource, want bool) {
	t.Helper()
	got, err := s.Allowed(p, a, r)
	if err != nil || got != want {
		t.Fatalf("%s %+v: got %v %v, want %v", a, r, got, err, want)
	}
}
func TestCompleteGrantNotCartesianRoleUnion(t *testing.T) {
	s, p, r, _ := fixture(t)
	assertAllowed(t, s, p, "run.start", r, true)
	r.ProjectID = "project-b"
	assertAllowed(t, s, p, "run.read", r, true)
	assertAllowed(t, s, p, "run.start", r, false)
	r.ProjectID = "project-a"
	r.EnvironmentID = "prod"
	// Scheduling is project scoped; every actual deploy needs deployment.execute.
	assertAllowed(t, s, p, "run.start", r, true)
}
func TestFailClosedPrincipalAndResource(t *testing.T) {
	s, p, r, _ := fixture(t)
	for _, change := range []func(*Principal, *Resource){func(p *Principal, r *Resource) { p.ID = "unknown" }, func(p *Principal, r *Resource) { p.Active = false }, func(p *Principal, r *Resource) { p.Revision = 6 }, func(p *Principal, r *Resource) { p.ModeEpoch = 2 }, func(p *Principal, r *Resource) { p.TenantID = "" }, func(p *Principal, r *Resource) { r.TenantID = "team-b" }, func(p *Principal, r *Resource) { r.Kind = "credential" }, func(p *Principal, r *Resource) { r.ID = "" }} {
		pp, rr := p, r
		change(&pp, &rr)
		assertAllowed(t, s, pp, "run.start", rr, false)
	}
	assertAllowed(t, s, p, "unknown.future.action", r, false)
	assertAllowed(t, nil, p, "run.read", r, false)
}
func TestBindingOnlyNarrows(t *testing.T) {
	_, p, r, snap := fixture(t)
	snap.Bindings[1].Limit = Scope{Kind: ScopeSelected, ProjectIDs: []string{"project-b"}, EnvironmentIDs: []string{"test"}}
	s, err := New(snap)
	if err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, s, p, "run.start", r, false)
}
func TestReadPrerequisiteMustCoverResource(t *testing.T) {
	_, p, r, snap := fixture(t)
	snap.Roles[1].Grants[0].Scope = Scope{Kind: ScopeNone}
	s, err := New(snap)
	if err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, s, p, "run.start", r, false)
}
func TestDeletedRoleAndImmutableSnapshot(t *testing.T) {
	s, p, r, snap := fixture(t)
	snap.Roles[1].Grants[1].Scope.ProjectIDs[0] = "project-b"
	snap.Roles[1].Pages[0] = "page.other"
	assertAllowed(t, s, p, "run.start", r, true)
	_, _, _, snap = fixture(t)
	snap.Roles[1].Deleted = true
	s, err := New(snap)
	if err != nil {
		t.Fatal(err)
	}
	assertAllowed(t, s, p, "run.start", r, false)
}
func TestMalformedPolicyRejected(t *testing.T) {
	for _, change := range []func(*Snapshot){func(s *Snapshot) { s.CatalogVersion++ }, func(s *Snapshot) { s.ModeEpoch = 0 }, func(s *Snapshot) { s.Revision = 0 }, func(s *Snapshot) { s.Roles[1].Grants[1].Action = "future" }, func(s *Snapshot) { s.Roles[1].Grants[1].Scope.Kind = "all" }, func(s *Snapshot) { s.Roles[1].Grants[1].Scope.ProjectIDs = []string{"*"} }, func(s *Snapshot) { s.Roles[1].Pages = nil }, func(s *Snapshot) { s.Roles[1].Grants = s.Roles[1].Grants[1:] }, func(s *Snapshot) { s.Bindings[1].RoleID = "missing" }} {
		_, _, _, snap := fixture(t)
		change(&snap)
		if _, err := New(snap); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("want invalid policy, got %v", err)
		}
	}
}
func TestOwnScopeDoesNotClaimLegacyRows(t *testing.T) {
	a, _ := Lookup("run.read")
	s := Scope{Kind: ScopeOwn}
	r := Resource{Kind: "run", ID: "r"}
	if s.matches("alice", a, r) {
		t.Fatal("legacy owner accepted")
	}
	r.OwnerID = "alice"
	if !s.matches("alice", a, r) {
		t.Fatal("own resource denied")
	}
}
func TestProjectScopeDoesNotGrantCredential(t *testing.T) {
	a, _ := Lookup("credential.use")
	s := Scope{Kind: ScopeSelected, ProjectIDs: []string{"p"}}
	r := Resource{Kind: "credential", ID: "c", ProjectID: "p"}
	if s.matches("alice", a, r) {
		t.Fatal("project scope granted credential use")
	}
}
func TestInstanceAdminStillNeedsFreshActivePrincipalAndKnownAction(t *testing.T) {
	s, p, _, _ := fixture(t)
	p.InstanceAdmin = true
	r := Resource{ID: "controller", Kind: "instance"}
	assertAllowed(t, s, p, "database.migrate", r, true)
	assertAllowed(t, s, p, "unknown", r, false)
	p.Active = false
	assertAllowed(t, s, p, "database.migrate", r, false)
}
func TestPresetsExplicitAndClosedUntilBound(t *testing.T) {
	roles := Presets("a")
	if len(roles) != 4 {
		t.Fatal(len(roles))
	}
	for i := range roles {
		roles[i].ID = roles[i].PresetKey
		for _, g := range roles[i].Grants {
			a, ok := Lookup(g.Action)
			if !ok || !slices.Contains(roles[i].Pages, a.Page) {
				t.Fatal("invalid preset")
			}
			if slices.Contains([]string{"server.terminal", "credential.reveal", "release.approve", "release.rollback", "database.migrate"}, g.Action) {
				t.Fatal("sensitive preset")
			}
			if len(g.Scope.EnvironmentIDs) > 0 || len(g.Scope.ProjectIDs) > 0 || len(g.Scope.ResourceIDs) > 0 {
				t.Fatal("preset chose user resources")
			}
			if i == 3 && g.Action != "environment.read" && g.Action != "project.read" && g.Action != "run.read" && g.Action != "guard.read" && g.Action != "dashboard.read" && g.Action != "template.read" && g.Action != "variable_group.read" && g.Action != "custom_node.read" && g.Action != "dora.read" {
				t.Fatal("viewer action")
			}
		}
	}
	_, err := New(Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	roles[0].Grants[0].Scope.Kind = ScopeNone
	if Presets("a")[0].Grants[0].Scope.Kind != ScopeTeam {
		t.Fatal("presets share state")
	}
}
func TestPresetAssignmentOnlyNeedsRoleAndResources(t *testing.T) {
	roles := Presets("a")
	for i := range roles {
		roles[i].ID = roles[i].PresetKey
	}
	binding := Binding{SubjectID: "alice", RoleID: "publisher", TenantID: "a", Limit: Scope{Kind: ScopeSelected, ProjectIDs: []string{"p"}, EnvironmentIDs: []string{"test-id", "production-id"}}}
	snapshot := Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: roles, Bindings: []Binding{binding}}
	s, err := New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{ID: "alice", TenantID: "a", Active: true, ModeEpoch: 1, Revision: 1}
	r := Resource{ID: "run-1", Kind: "run", TenantID: "a", ProjectID: "p"}
	assertAllowed(t, s, p, "run.start", r, true) // Pure CI needs no fictional environment.
	r = Resource{ID: "test-id", Kind: "environment", TenantID: "a", ProjectID: "p", EnvironmentID: "test-id", EnvironmentPurpose: "test"}
	assertAllowed(t, s, p, "deployment.execute", r, true)
	r.EnvironmentID = "production-id"
	r.EnvironmentPurpose = "production"
	assertAllowed(t, s, p, "deployment.execute", r, false)
	r.EnvironmentID = "test-id"
	r.EnvironmentPurpose = ""
	assertAllowed(t, s, p, "deployment.execute", r, false)
	snapshot.Bindings[0].Limit.ProjectIDs = nil
	s, err = New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	r.EnvironmentPurpose = "test"
	assertAllowed(t, s, p, "deployment.execute", r, false)
	snapshot.Bindings[0].Limit.Kind = ScopeTeam
	if _, err = New(snapshot); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal("preset binding silently broadened", err)
	}
}
func TestCatalogCopiesAndPrerequisites(t *testing.T) {
	cat := Catalog()
	seen := map[string]bool{}
	for _, a := range cat {
		if seen[a.ID] {
			t.Fatal("duplicate action")
		}
		seen[a.ID] = true
		chain := map[string]bool{a.ID: true}
		for next := a.ReadAction; next != ""; {
			read, ok := Lookup(next)
			if !ok || chain[next] || read.ResourceKind != a.ResourceKind {
				t.Fatalf("bad read prerequisite: %s -> %s", a.ID, next)
			}
			chain[next] = true
			next = read.ReadAction
		}
	}
	cat[0].ID = "mutated"
	if _, ok := Lookup("mutated"); ok {
		t.Fatal("shared catalog")
	}
}

func TestTestEnvironmentPolicyIntersectsExplicitIDs(t *testing.T) {
	a, _ := Lookup("deployment.execute")
	scope := Scope{Kind: ScopeTeam, EnvironmentPolicy: "test", EnvironmentIDs: []string{"allowed"}}
	res := Resource{Kind: "environment", ID: "r", EnvironmentID: "other-test", EnvironmentPurpose: "test"}
	if scope.matches("u", a, res) {
		t.Fatal("explicit environment selection ignored")
	}
	res.EnvironmentID = "allowed"
	if !scope.matches("u", a, res) {
		t.Fatal("explicit allowed environment denied")
	}
}

func TestTypedResourceReferencesCannotCrossKinds(t *testing.T) {
	scope := Scope{Kind: ScopeSelected, ResourceRefs: []ResourceRef{{Kind: "server", ID: "shared"}}}
	role := Role{ID: "r", TenantID: "t", Pages: []string{"page.servers", "page.vault"}, Grants: []Grant{{Action: "server.inspect", Scope: Scope{Kind: ScopeTeam}}, {Action: "credential.metadata", Scope: Scope{Kind: ScopeTeam}}}}
	service, err := New(Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: []Role{role}, Bindings: []Binding{{SubjectID: "u", RoleID: "r", TenantID: "t", Limit: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{ID: "u", TenantID: "t", Active: true, ModeEpoch: 1, Revision: 1}
	assertAllowed(t, service, principal, "server.inspect", Resource{ID: "shared", Kind: "server", TenantID: "t"}, true)
	assertAllowed(t, service, principal, "credential.metadata", Resource{ID: "shared", Kind: "credential", TenantID: "t"}, false)
	scope.ResourceRefs[0].Kind = "credential"
	assertAllowed(t, service, principal, "credential.metadata", Resource{ID: "shared", Kind: "credential", TenantID: "t"}, false)
}

func TestDeploymentCannotUseRunAdmissionOrAnotherRoleEnvironment(t *testing.T) {
	roles := []Role{{ID: "read", TenantID: "t", Pages: []string{"page.runs", "page.environments"}, Grants: []Grant{{Action: "run.read", Scope: Scope{Kind: ScopeTeam}}, {Action: "run.start", Scope: Scope{Kind: ScopeTeam}}, {Action: "environment.read", Scope: Scope{Kind: ScopeTeam}}}}, {ID: "deploy", TenantID: "t", Pages: []string{"page.runs", "page.environments"}, Grants: []Grant{{Action: "environment.read", Scope: Scope{Kind: ScopeSelected, ProjectIDs: []string{"p"}}}, {Action: "deployment.execute", Scope: Scope{Kind: ScopeSelected, ProjectIDs: []string{"p"}, EnvironmentIDs: []string{"test"}}}}}}
	s, err := New(Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: roles, Bindings: []Binding{{SubjectID: "u", RoleID: "read", TenantID: "t", Limit: Scope{Kind: ScopeTeam}}, {SubjectID: "u", RoleID: "deploy", TenantID: "t", Limit: Scope{Kind: ScopeTeam, EnvironmentIDs: []string{"test", "prod"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{ID: "u", TenantID: "t", Active: true, ModeEpoch: 1, Revision: 1}
	assertAllowed(t, s, p, "run.start", Resource{Kind: "run", ID: "r", TenantID: "t", ProjectID: "p"}, true)
	r := Resource{Kind: "environment", ID: "e", TenantID: "t", ProjectID: "p", EnvironmentID: "test"}
	assertAllowed(t, s, p, "deployment.execute", r, true)
	r.EnvironmentID = "prod"
	assertAllowed(t, s, p, "deployment.execute", r, false)
	r.EnvironmentID = ""
	assertAllowed(t, s, p, "deployment.execute", r, false)
	r.EnvironmentID = "test"
	r.ProjectID = "other"
	assertAllowed(t, s, p, "deployment.execute", r, false)
}

func TestPrivateSessionActionsAlwaysRequireSameOwnerAndTeam(t *testing.T) {
	role := Role{ID: "r", TenantID: "t", Pages: []string{"page.opschat"}, Grants: []Grant{{Action: "opschat.read", Scope: Scope{Kind: ScopeTeam}}, {Action: "opschat.submit", Scope: Scope{Kind: ScopeTeam}}}}
	service, err := New(Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 1, Roles: []Role{role}, Bindings: []Binding{{SubjectID: "u", TenantID: "t", RoleID: "r", Limit: Scope{Kind: ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, admin := range []bool{false, true} {
		p := Principal{ID: "u", TenantID: "t", Active: true, InstanceAdmin: admin, ModeEpoch: 1, Revision: 1}
		for _, action := range []string{"opschat.read", "opschat.submit"} {
			assertAllowed(t, service, p, action, Resource{ID: "chat", Kind: "ops_chat", TenantID: "t", OwnerID: "u"}, true)
			assertAllowed(t, service, p, action, Resource{ID: "chat", Kind: "ops_chat", TenantID: "t", OwnerID: "another"}, false)
			assertAllowed(t, service, p, action, Resource{ID: "chat", Kind: "ops_chat", TenantID: "other", OwnerID: "u"}, false)
			assertAllowed(t, service, p, action, Resource{ID: "chat", Kind: "ops_chat", TenantID: "t"}, false)
		}
	}
}

func TestTransitiveReadPrerequisiteScope(t *testing.T) {
	_, p, _, snap := fixture(t)
	whole := Scope{Kind: ScopeTeam}
	snap.Roles = []Role{{ID: "operator", TenantID: "team-a", Pages: []string{"page.servers", "page.containers"}, Grants: []Grant{{Action: "server.inspect", Scope: Scope{Kind: ScopeSelected, ResourceIDs: []string{"server-a"}}}, {Action: "proxy.read", Scope: whole}, {Action: "proxy.manage", Scope: whole}}}}
	snap.Bindings = []Binding{{SubjectID: p.ID, RoleID: "operator", TenantID: "team-a", Limit: whole}}
	s, e := New(snap)
	if e != nil {
		t.Fatal(e)
	}
	r := Resource{TenantID: "team-a", Kind: "server", ID: "server-a"}
	assertAllowed(t, s, p, "proxy.manage", r, true)
	r.ID = "server-b"
	assertAllowed(t, s, p, "proxy.manage", r, false)
	if s.CanDelegate(p, Grant{Action: "proxy.manage", Scope: whole}, nil) {
		t.Fatal("delegation exceeded transitive read scope")
	}
	if !s.CanDelegate(p, Grant{Action: "proxy.manage", Scope: Scope{Kind: ScopeSelected, ResourceIDs: []string{"server-a"}}}, nil) {
		t.Fatal("bounded delegation rejected")
	}
	pred, e := s.RootPredicate(p, "proxy.manage", "s.id")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, arg := range pred.Args {
		if arg == "server-a" {
			found = true
		}
	}
	if !found {
		t.Fatal("SQL omitted transitive scope", pred)
	}

}
