package pipeline

import "testing"

func TestWhenMatches(t *testing.T) {
	cases := []struct {
		name   string
		when   When
		branch string
		event  string
		want   bool
	}{
		{"empty always matches", When{}, "anything", "manual", true},
		{"branch exact hit", When{Branches: []string{"main"}}, "main", "webhook", true},
		{"branch exact miss", When{Branches: []string{"main"}}, "dev", "webhook", false},
		{"branch glob crosses slash", When{Branches: []string{"release/*"}}, "release/1.2", "webhook", true},
		{"branch glob miss", When{Branches: []string{"release/*"}}, "main", "webhook", false},
		{"branch star matches all", When{Branches: []string{"*"}}, "whatever", "manual", true},
		{"event hit", When{Events: []string{"schedule"}}, "main", "schedule", true},
		{"event miss", When{Events: []string{"schedule"}}, "main", "manual", false},
		{"both and-hit", When{Branches: []string{"main"}, Events: []string{"webhook"}}, "main", "webhook", true},
		{"both branch-hit event-miss", When{Branches: []string{"main"}, Events: []string{"webhook"}}, "main", "manual", false},
		{"both branch-miss event-hit", When{Branches: []string{"main"}, Events: []string{"webhook"}}, "dev", "webhook", false},
		{"list any hit", When{Branches: []string{"main", "release/*"}}, "release/9", "manual", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.when.Matches(c.branch, c.event); got != c.want {
				t.Errorf("Matches(%q,%q) = %v, want %v", c.branch, c.event, got, c.want)
			}
		})
	}
}

func TestWhenIsEmptyAndNormalize(t *testing.T) {
	if !(When{}).IsEmpty() {
		t.Error("zero When should be empty")
	}
	if (When{Branches: []string{"x"}}).IsEmpty() {
		t.Error("with branch not empty")
	}
	n := normalizeWhen(When{Branches: []string{" main ", "main", "", "dev"}, Events: []string{" manual ", "manual"}})
	if len(n.Branches) != 2 || n.Branches[0] != "main" || n.Branches[1] != "dev" {
		t.Errorf("branches not deduped/trimmed: %v", n.Branches)
	}
	if len(n.Events) != 1 || n.Events[0] != "manual" {
		t.Errorf("events not deduped/trimmed: %v", n.Events)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "x", true},
		{"", "x", true},
		{"main", "main", true},
		{"main", "dev", false},
		{"release/*", "release/1.2", true},
		{"release/*", "release", false},
		{"feature/*", "feature/a/b", true},
		{"*-rc", "v1-rc", true},
		{"*-rc", "v1-final", false},
		{"v*-rc", "v2-rc", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q,%q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}
