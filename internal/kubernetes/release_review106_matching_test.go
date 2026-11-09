package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReview106ProtectedChecksFollowStableIdentity(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		config := privateConfigFixture()
		config["checks"] = []any{map[string]any{"kind": "http", "name": "first", "target": "http://private.invalid/one", "expected": "one"}, map[string]any{"kind": "http", "name": "second", "target": "http://private.invalid/two", "expected": "two"}}
		safe, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", config)
		if e != nil {
			t.Fatal(e)
		}
		for _, change := range []string{"remove-first", "reorder"} {
			t.Run(change, func(t *testing.T) {
				next, _ := cloneReleaseConfig(safe)
				rows := releaseItems(next["checks"])
				if change == "remove-first" {
					next["checks"] = rows[1:]
				} else {
					next["checks"] = []any{rows[1], rows[0]}
				}
				updated, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", next)
				if e != nil {
					t.Fatal("saving edited protected check config failed", e)
				}
				actual, e := f.s.ResolveReleaseConfig(ctx, "project-1", "node", updated)
				if e != nil || releaseMap(releaseItems(actual["checks"])[0])["target"] != "http://private.invalid/two" {
					t.Fatal("unchanged protected check could not move/remove unrelated rows", e)
				}
			})
		}
		for _, change := range []string{"name", "kind", "namespace", "field", "node", "project"} {
			t.Run(change, func(t *testing.T) {
				next, _ := cloneReleaseConfig(safe)
				check := releaseMap(releaseItems(next["checks"])[1])
				project, node := "project-1", "node"
				switch change {
				case "name":
					check["name"] = "other"
				case "kind":
					check["kind"] = "tcp"
				case "namespace":
					check["namespace"] = "other"
				case "field":
					check["expected"] = check["target"]
				case "node":
					node = "another"
				case "project":
					project = "other-project"
				}
				if _, e := f.s.ProtectReleaseConfig(ctx, project, node, next); e == nil {
					t.Fatal("protected check crossed its stable identity/field/owner")
				}
			})
		}
	})
}
func TestReview106PreparationRejectsUnsupportedProbeWait(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, plan := releaseStoreFixture(t, st)
		c := NewReleaseController(f.s, nil)
		api := &releaseControllerAPI{objects: map[string]map[string]any{}}
		for _, kind := range []string{"storage", "network-deny"} {
			t.Run(kind, func(t *testing.T) {
				checks := []any{map[string]any{"kind": "storage", "name": "check", "target": "claim", "waitSeconds": 1}}
				if kind == "network-deny" {
					checks = []any{map[string]any{"kind": "network-allow", "name": "allow", "target": "10.0.0.1:80"}, map[string]any{"kind": "network-deny", "name": "deny", "target": "10.0.0.1:80", "positiveControl": "allow", "waitSeconds": 1}}
				}
				data, _ := json.Marshal(map[string]any{"namespace": "app", "checks": checks})
				copy := plan
				if _, e := c.appendReleaseChecks(context.Background(), api, &copy, plan.Authorization, data); e != ErrInvalid {
					t.Fatal("impossible probe wait accepted at preparation", e)
				}
			})
		}
	})
}
func TestReview106AtomicAndNamedListNeighbors(t *testing.T) {
	for _, actual := range []any{[]any{"one", "two", "extra"}, []any{"two", "one"}, []any{"one"}} {
		if ReleasePodTemplateMatches(map[string]any{"args": []any{"one", "two"}}, map[string]any{"args": actual}) {
			t.Fatal("atomic argv length/order changed")
		}
	}
	want := releaseTestObject(`{"containers":[{"name":"app","args":["one","two"],"env":[{"name":"A","value":"a"}]}],"volumes":[{"name":"data","emptyDir":{}}]}`)
	actual := releaseTestObject(`{"containers":[{"name":"sidecar"},{"name":"app","args":["one","two"],"env":[{"name":"INJECTED","value":"i"},{"name":"A","value":"a"}]}],"volumes":[{"name":"token","emptyDir":{}},{"name":"data","emptyDir":{}}]}`)
	if !releasePodSpecMatches(want, actual) {
		t.Fatal("admission-added named containers/env/volumes rejected")
	}
	releaseMap(releaseItems(actual["containers"])[1])["args"] = []any{"two", "one"}
	if releasePodSpecMatches(want, actual) {
		t.Fatal("named parent hid reordered atomic child")
	}
}
func TestReview106FieldRestrictionNeverBroadensMutations(t *testing.T) {
	id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
	grant := ReleaseAccess{ID: id, Actions: []string{"get", "patch", "rollback"}, Fields: []string{"/data/allowed"}}
	if !releaseAccessContains(grant, ReleaseAccess{ID: id, Actions: []string{"get"}}) {
		t.Fatal("read catalog denied")
	}
	for _, verb := range []string{"patch", "rollback"} {
		if releaseAccessContains(grant, ReleaseAccess{ID: id, Actions: []string{verb}}) || releaseAccessContains(grant, ReleaseAccess{ID: id, Actions: []string{verb}, Fields: []string{"/data/foreign"}}) {
			t.Fatal("field-restricted write broadened")
		}
	}
	if !releaseAccessContains(grant, ReleaseAccess{ID: id, Actions: []string{"patch"}, Fields: []string{"/data/allowed"}}) {
		t.Fatal("explicit allowed field denied")
	}
}

func TestReview106OrderedNamedListsPreserveRelativeSequence(t *testing.T) {
	for _, field := range []string{"initContainers", "env"} {
		first := map[string]any{"name": "first", "value": "seed"}
		second := map[string]any{"name": "second", "value": "$(first)"}
		injected := map[string]any{"name": "injected"}
		spec := func(values []any) map[string]any {
			if field == "env" {
				return map[string]any{"containers": []any{map[string]any{"name": "app", "env": values}}}
			}
			return map[string]any{field: values}
		}
		want := spec([]any{first, second})
		reversed := spec([]any{second, first})
		if ReleaseObjectMatches(want, reversed) || releasePodSpecMatches(want, reversed) {
			t.Fatal("sequential named list order changed", field)
		}
		if !releasePodSpecMatches(want, spec([]any{injected, first, map[string]any{"name": "middle"}, second})) {
			t.Fatal("distinct injection preserving declared relative order rejected", field)
		}
	}
}
