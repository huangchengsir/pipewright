package run

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

const resolvedTestSHA = "1234567890abcdef1234567890abcdef12345678"

func TestResolvedCommitEvidence(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		svc := New(st.DB)
		projectID := seedProject(t, st.DB)
		for _, triggerCommit := range []string{"", "abc1234", resolvedTestSHA} {
			r, err := svc.Create(ctx, projectID, Trigger{Type: TriggerManual, Commit: triggerCommit})
			if err != nil {
				t.Fatal(err)
			}
			before, err := svc.Get(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if before.ResolvedCommitSHA != "" || before.ResolvedCommitConflict || before.Trigger.Commit != triggerCommit {
				t.Fatalf("trigger is not checkout evidence: %+v", before)
			}
			if err := svc.SetCommit(ctx, r.ID, "  "+strings.ToUpper(resolvedTestSHA)+"\n"); err != nil {
				t.Fatal(err)
			}
			if err := svc.SetCommit(ctx, r.ID, resolvedTestSHA); err != nil {
				t.Fatalf("same checkout must be idempotent: %v", err)
			}
			after, err := svc.Get(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.ResolvedCommitSHA != resolvedTestSHA || after.ResolvedCommitConflict || after.Trigger.Commit != resolvedTestSHA[:7] {
				t.Fatalf("resolved evidence/display mismatch: %+v", after)
			}
		}
		listed, err := svc.List(ctx, ListFilter{ProjectID: projectID})
		if err != nil || len(listed.Items) != 3 {
			t.Fatalf("List: result=%+v err=%v", listed, err)
		}
		for _, r := range listed.Items {
			if r.ResolvedCommitSHA != resolvedTestSHA || r.ResolvedCommitConflict {
				t.Fatalf("list lost checkout evidence: %+v", r)
			}
		}
	})
}

func TestResolvedCommitRejectsGuesses(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		svc := New(st.DB)
		r, err := svc.Create(ctx, seedProject(t, st.DB), Trigger{Type: TriggerManual, Commit: "legacy7"})
		if err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []string{"abc1234", "main", strings.Repeat("z", 40), strings.Repeat("a", 39), strings.Repeat("a", 41)} {
			if err := svc.SetCommit(ctx, r.ID, invalid); !errors.Is(err, ErrInvalidCommit) {
				t.Fatalf("SetCommit(%q) error=%v, want invalid resolved commit", invalid, err)
			}
		}
		if err := svc.SetCommit(ctx, r.ID, " \n"); err != nil {
			t.Fatal(err)
		}
		got, err := svc.Get(ctx, r.ID)
		if err != nil || got.ResolvedCommitSHA != "" || got.ResolvedCommitConflict || got.Trigger.Commit != "legacy7" {
			t.Fatalf("legacy value was expanded/changed: run=%+v err=%v", got, err)
		}
		if err := svc.SetCommit(ctx, "missing-run", resolvedTestSHA); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing run error=%v", err)
		}
	})
}

func TestResolvedCommitConcurrentConflict(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		svc := New(st.DB)
		otherService := New(st.DB) // SQL, rather than a service-local lock, arbitrates.
		r, err := svc.Create(ctx, seedProject(t, st.DB), Trigger{Type: TriggerManual})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.SetCommit(ctx, r.ID, resolvedTestSHA); err != nil {
			t.Fatal(err)
		}
		otherSHA := strings.Repeat("b", 40)
		start := make(chan struct{})
		errs := make(chan error, 24)
		var wg sync.WaitGroup
		for i := range 24 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				sha := resolvedTestSHA
				if i%2 != 0 {
					sha = otherSHA
				}
				errs <- otherService.SetCommit(ctx, r.ID, sha)
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := svc.SetCommit(ctx, r.ID, resolvedTestSHA); err != nil {
			t.Fatal(err)
		}
		got, err := svc.Get(ctx, r.ID)
		if err != nil || got.ResolvedCommitSHA != resolvedTestSHA || !got.ResolvedCommitConflict || got.Trigger.Commit != resolvedTestSHA[:7] {
			t.Fatalf("conflict must retain the first checkout and stay set: run=%+v err=%v", got, err)
		}
		list, err := svc.List(ctx, ListFilter{ProjectID: r.ProjectID})
		if err != nil || len(list.Items) != 1 || !list.Items[0].ResolvedCommitConflict {
			t.Fatalf("list lost conflict: list=%+v err=%v", list, err)
		}
	})
}
