package authz

import (
	"slices"
	"testing"
)

func TestPagesDoNotGrantResourcesOrCrossIdentity(t *testing.T) {
	s, p, r, snap := fixture(t)
	if !slices.Equal(s.Pages(p), []string{"page.runs"}) {
		t.Fatal(s.Pages(p))
	}
	r.ProjectID = "outside"
	assertAllowed(t, s, p, "run.start", r, false)
	for _, change := range []func(*Principal){func(p *Principal) { p.ID = "other" }, func(p *Principal) { p.TenantID = "other" }, func(p *Principal) { p.Active = false }, func(p *Principal) { p.Revision++ }} {
		x := p
		change(&x)
		if len(s.Pages(x)) != 0 {
			t.Fatal("stale or foreign menus", x)
		}
	}
	for i := range snap.Roles {
		snap.Roles[i].Deleted = true
	}
	s, e := New(snap)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Pages(p)) != 0 {
		t.Fatal("deleted role menus")
	}
	p.InstanceAdmin = true
	if !slices.Contains(s.Pages(p), "page.system") {
		t.Fatal("admin menus")
	}
}
