package run

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Run with storetest's selected dialect. Filtering precedes LIMIT and COUNT
// uses the same predicates, including literal wildcard and timestamp boundaries.
func TestListSearchAcrossPages(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	project := seedProject(t, db)
	ctx := context.Background()
	other := seedProject(t, db)
	for i := 0; i < 7; i++ {
		if _, err := svc.Create(ctx, project, Trigger{Type: TriggerManual, Branch: "feature/literal_%!", Commit: "abc123"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(ctx, other, Trigger{Type: TriggerManual, Branch: "feature/literalAX"}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"literal_%!", "ABC123"} {
		seen := map[string]bool{}
		for page := 1; page <= 4; page++ {
			result, err := svc.List(ctx, ListFilter{Query: query, ProjectID: project, PageSize: 2, Page: page})
			if err != nil {
				t.Fatal(err)
			}
			if result.Total != 7 {
				t.Fatalf("q=%q total=%d", query, result.Total)
			}
			for _, row := range result.Items {
				if seen[row.ID] {
					t.Fatal("duplicate across pages")
				}
				seen[row.ID] = true
			}
		}
		if len(seen) != 7 {
			t.Fatalf("missing results: %d", len(seen))
		}
	}
	r, err := svc.List(ctx, ListFilter{Query: "%"})
	if err != nil || r.Total != 7 {
		t.Fatalf("literal wildcard: %#v %v", r, err)
	}
	r, err = svc.List(ctx, ListFilter{Query: "' OR 1=1 --"})
	if err != nil || r.Total != 0 {
		t.Fatalf("literal SQL text: %#v %v", r, err)
	}
	if _, err := svc.List(ctx, ListFilter{Query: strings.Repeat("界", 201)}); err != ErrInvalidFilter {
		t.Fatalf("long query accepted: %v", err)
	}
}

func TestListSearchIdentityAndUTCBoundary(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	project := seedProject(t, db)
	ctx := context.Background()
	row, err := svc.Create(ctx, project, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE pipeline_runs SET created_at = ? WHERE id = ?`, "2026-10-30T01:00:00Z", row.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetCommit(ctx, row.ID, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{row.ID[:8], strings.Repeat("a", 20)} {
		result, err := svc.List(ctx, ListFilter{Query: q})
		if err != nil || result.Total != 1 {
			t.Fatalf("identity %q %#v %v", q, result, err)
		}
	}
	for _, tc := range []struct {
		after string
		want  int
	}{{"2026-10-30T09:00:00+08:00", 1}, {"2026-10-30T01:00:00.001Z", 0}, {"2026-10-30T01:00:01Z", 0}} {
		after, _ := time.Parse(time.RFC3339, tc.after)
		result, err := svc.List(ctx, ListFilter{After: after})
		if err != nil || result.Total != tc.want {
			t.Fatalf("after=%s %#v %v", tc.after, result, err)
		}
	}
}

func TestListSearchSameUnicodeAndASCII(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	project := seedProject(t, db)
	ctx := context.Background()
	for _, branch := range []string{"Feature/Ä", "Feature/İ", "分支/中文"} {
		if _, err := svc.Create(ctx, project, Trigger{Type: TriggerManual, Branch: branch}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		q    string
		want int
	}{{"Ä", 1}, {"ä", 0}, {"İ", 1}, {"i", 0}, {"中文", 1}, {"FEATURE/", 2}} {
		result, err := svc.List(ctx, ListFilter{Query: tc.q})
		if err != nil || result.Total != tc.want {
			t.Fatalf("query %q want %d got %#v %v", tc.q, tc.want, result, err)
		}
	}
}

func TestListSearchMetacharactersAreLiteral(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	project := seedProject(t, db)
	ctx := context.Background()
	target := `literal.[a]+(b)?*{2}|^$\pattern_%!`
	if _, err := svc.Create(ctx, project, Trigger{Type: TriggerManual, Branch: target}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, project, Trigger{Type: TriggerManual, Branch: "literal-other"}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{target, `.[a]+(b)?*{2}|^$`, `\pattern_%!`, "LITERAL."} {
		result, err := svc.List(ctx, ListFilter{Query: query})
		if err != nil || result.Total != 1 {
			t.Fatalf("literal metacharacter query %q: %#v %v", query, result, err)
		}
	}
}
