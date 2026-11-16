package authz

import (
	"database/sql"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

// The same data boundary applies independently to each published action. Test
// through the real Casbin-backed evaluator, including cross-kind reused IDs.
func TestCatalogActionsRespectDataScopes(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, action := range Catalog() {
		t.Run(action.ID, func(t *testing.T) {
			principal := Principal{ID: "alice", TenantID: "team-a", Active: true, ModeEpoch: 1, Revision: 2}
			resource := Resource{ID: "selected", Kind: action.ResourceKind, TenantID: "team-a", OwnerID: "alice", ProjectID: "project-a", EnvironmentID: "staging", EnvironmentPurpose: "test"}
			snapshot := Snapshot{CatalogVersion: CatalogVersion, ModeEpoch: 1, Revision: 2}
			if action.InstanceOnly {
				service, err := New(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				assertAllowed(t, service, principal, action.ID, resource, false)
				principal.InstanceAdmin = true
				assertAllowed(t, service, principal, action.ID, resource, true)
				principal.Revision--
				assertAllowed(t, service, principal, action.ID, resource, false)
				return
			}

			ownerMismatch := resource
			ownerMismatch.OwnerID, ownerMismatch.ID = "bob", "different"
			crossTeam := resource
			crossTeam.TenantID = "team-b"
			wrongKind := resource
			wrongKind.Kind = "different-kind"
			wrongEnvironment := resource
			wrongEnvironment.EnvironmentID, wrongEnvironment.EnvironmentPurpose = "production", "production"

			for _, tc := range []struct {
				name  string
				scope Scope
				same  bool
				other bool
			}{
				{"none", Scope{Kind: ScopeNone}, false, false},
				{"team", Scope{Kind: ScopeTeam}, true, !action.OwnerOnly},
				{"selected", Scope{Kind: ScopeSelected, ResourceRefs: []ResourceRef{{Kind: resource.Kind, ID: resource.ID}}}, true, false},
				{"empty selection", Scope{Kind: ScopeSelected}, false, false},
				{"own", Scope{Kind: ScopeOwn}, true, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					tc.scope.EnvironmentIDs = []string{"staging"}
					role := Role{ID: "role", TenantID: "team-a", Pages: []string{action.Page}, Grants: []Grant{{Action: action.ID, Scope: tc.scope}}}
					visited := map[string]bool{action.ID: true}
					for id := action.ReadAction; id != ""; {
						read, ok := Lookup(id)
						if !ok || visited[id] || read.ResourceKind != resource.Kind {
							t.Fatalf("invalid prerequisite %q for %s", id, action.ID)
						}
						visited[id] = true
						if !slices.Contains(role.Pages, read.Page) {
							role.Pages = append(role.Pages, read.Page)
						}
						role.Grants = append(role.Grants, Grant{Action: read.ID, Scope: Scope{Kind: ScopeTeam, EnvironmentIDs: []string{"staging"}}})
						id = read.ReadAction
					}

					snapshot.Roles = []Role{role}
					snapshot.Bindings = []Binding{{SubjectID: principal.ID, RoleID: role.ID, TenantID: "team-a", Limit: Scope{Kind: ScopeTeam, EnvironmentIDs: []string{"staging"}}}}
					service, err := New(snapshot)
					if err != nil {
						t.Fatal(err)
					}
					for _, probe := range []struct {
						name     string
						resource Resource
						allowed  bool
					}{
						{"in scope", resource, tc.same},
						{"different resource and owner", ownerMismatch, tc.other},
						{"same id in another team", crossTeam, false},
						{"same id with a different kind", wrongKind, false},
					} {
						assertAllowed(t, service, principal, action.ID, probe.resource, probe.allowed)
						assertCatalogSQL(t, db, service, principal, action.ID, probe.resource, principal.Revision, probe.name, probe.allowed)
					}
					if action.EnvironmentRequired {
						assertAllowed(t, service, principal, action.ID, wrongEnvironment, false)
						assertCatalogSQL(t, db, service, principal, action.ID, wrongEnvironment, principal.Revision, "production", false)
					}
					assertCatalogSQL(t, db, service, principal, action.ID, resource, principal.Revision+1, "stale revision", false)
				})
			}
		})
	}
}

func assertCatalogSQL(t *testing.T, db *sql.DB, service *Service, p Principal, action string, r Resource, storedRevision uint64, caseName string, allowed bool) {
	t.Helper()
	predicate, err := service.RootPredicate(p, action, "item.id")
	if err != nil {
		t.Fatal(err)
	}
	// Each probe represents one row in a domain query. Its ownership comes
	// from storage, including reused IDs on different teams/resource kinds.
	query := `WITH
		item(id) AS (VALUES (?)),
		access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) AS (VALUES (?,?,?,?,?,?)),
		access_environments(id,tenant_id,project_id,purpose) AS (VALUES (?,?,?,?)),
		access_control(id,enabled,mode_epoch,authz_revision,catalog_version) AS (VALUES (1,1,?,?,?))
		SELECT COUNT(*) FROM item WHERE ` + predicate.SQL
	args := []any{r.ID, r.Kind, r.ID, r.TenantID, r.OwnerID, r.ProjectID, r.EnvironmentID,
		r.EnvironmentID, r.TenantID, r.ProjectID, r.EnvironmentPurpose, p.ModeEpoch, storedRevision, CatalogVersion}
	args = append(args, predicate.Args...)
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("%s SQL query: %v", caseName, err)
	}
	if (count == 1) != allowed {
		t.Fatalf("%s SQL count=%d, allowed=%v", caseName, count, allowed)
	}
}
