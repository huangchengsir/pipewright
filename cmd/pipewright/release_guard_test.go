package main

import (
	"context"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type disabledRunPruner struct{ calls int }

func (p *disabledRunPruner) Prune(context.Context, time.Time) (int, error) { p.calls++; return 0, nil }

func TestGuardRetentionRunsWhenRunRetentionDisabled(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	s, err := releaseguard.New(releaseguard.Options{DB: st.DB, Ledger: deployattempt.New(st.DB)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	if _, err = st.DB.Exec(`INSERT INTO release_guard_plans(id,project_id,expires_at,plan_json) VALUES('expired','p',0,'{}')`); err != nil {
		t.Fatal(err)
	}
	runs := &disabledRunPruner{}
	if _, err = (guardRetentionPruner{runs: runs, guards: s}).Prune(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM release_guard_plans WHERE id='expired'`).Scan(&n); err != nil || n != 0 || runs.calls != 1 {
		t.Fatal("guard retention was gated by run retention", n, runs.calls, err)
	}
}
