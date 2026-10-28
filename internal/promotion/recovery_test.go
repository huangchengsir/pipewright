package promotion

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"sync"
	"sync/atomic"
	"testing"
)

func retryPromotionFixture(t *testing.T, db *sql.DB) (string, string, string) {
	t.Helper()
	p := seedProject(t, db)
	root := seedRun(t, db, p, SuccessStatus)
	child := seedRun(t, db, p, SuccessStatus)
	for i, id := range []string{root, child} {
		parent, kind := "", "original"
		if i > 0 {
			parent, kind = root, "recovery"
		}
		if _, e := db.Exec("INSERT INTO run_retry_attempts(run_id,root_id,parent_id,revision,execution_kind,status,created_at) VALUES(?,?,?,?,?,'success',1)", id, root, parent, i+1, kind); e != nil {
			t.Fatal(e)
		}
	}
	return p, root, child
}

type inspectRetryGate struct {
	called atomic.Int32
	source string
}

func (g *inspectRetryGate) Await(_ context.Context, _ string, info GateInfo) (bool, string, error) {
	g.called.Add(1)
	g.source = info.SourceRunID
	return true, "current-reviewer", nil
}
func TestPromotionSpansRecoveryLineageAndUsesCurrentGateSource(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := NewStore(st.DB)
		p, root, child := retryPromotionFixture(t, st.DB)
		if _, e := s.SaveChain(ctx, p, Chain{Environments: []EnvStage{{Name: "dev"}, {Name: "prod", Gated: true}}}); e != nil {
			t.Fatal(e)
		}
		gate := &inspectRetryGate{}
		c := NewCoordinator(s, fakeRuns{root: {ID: root, ProjectID: p, Status: SuccessStatus}, child: {ID: child, ProjectID: p, Status: SuccessStatus}}, gate, nil)
		if _, e := c.Promote(ctx, root, "dev", "first"); e != nil {
			t.Fatal(e)
		}
		if _, e := c.Promote(ctx, child, "dev", "second"); !errors.Is(e, ErrAlreadyPromoted) {
			t.Fatal(e)
		}
		result, e := c.Promote(ctx, child, "prod", "second")
		if e != nil || !result.Approved || gate.called.Load() != 1 || gate.source != child || result.Record.SourceRunID != child {
			t.Fatal(result, e, gate.called.Load(), gate.source)
		}
		if _, e = c.Promote(ctx, root, "prod", "third"); !errors.Is(e, ErrAlreadyPromoted) {
			t.Fatal(e)
		}
		records, e := s.ListRecordsForRun(ctx, child)
		if e != nil || len(records) != 2 {
			t.Fatal(records, e)
		}
		sources := map[string]string{}
		for _, record := range records {
			sources[record.TargetEnvironment] = record.SourceRunID
		}
		if sources["dev"] != root || sources["prod"] != child {
			t.Fatal(sources)
		}
	})
}
func TestConcurrentPromotionOriginalAndRecoveryReserveOnlyOneTarget(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := NewStore(st.DB)
		p, root, child := retryPromotionFixture(t, st.DB)
		if _, e := s.SaveChain(ctx, p, Chain{Environments: []EnvStage{{Name: "dev"}}}); e != nil {
			t.Fatal(e)
		}
		c := NewCoordinator(s, fakeRuns{root: {ID: root, ProjectID: p, Status: SuccessStatus}, child: {ID: child, ProjectID: p, Status: SuccessStatus}}, nil, nil)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, id := range []string{root, child} {
			wg.Add(1)
			go func(id string) { defer wg.Done(); <-start; _, e := c.Promote(ctx, id, "dev", "actor"); results <- e }(id)
		}
		close(start)
		wg.Wait()
		close(results)
		ok, duplicate := 0, 0
		for e := range results {
			if e == nil {
				ok++
			} else if errors.Is(e, ErrAlreadyPromoted) {
				duplicate++
			} else {
				t.Fatal(e)
			}
		}
		if ok != 1 || duplicate != 1 {
			t.Fatal(ok, duplicate)
		}
		records, e := s.ListRecordsForProject(ctx, p)
		if e != nil || len(records) != 1 {
			t.Fatal(records, e)
		}
	})
}
