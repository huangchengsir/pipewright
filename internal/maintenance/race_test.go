package maintenance

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"sync"
	"testing"
)

func TestConcurrentFenceAdmissionNeverOverlap(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		start := make(chan struct{})
		entered := make(chan func(), 1)
		fenced := make(chan error, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			release, err := Enter(ctx, st.DB, "race")
			if err != nil {
				entered <- nil
			} else {
				entered <- release
			}
		}()
		go func() { defer wg.Done(); <-start; fenced <- Fence(ctx, st.DB, "race-task") }()
		close(start)
		release := <-entered
		err := <-fenced
		wg.Wait()
		if release != nil && err == nil {
			release()
			t.Fatal("installation and work both admitted")
		}
		if release == nil && err != nil {
			t.Fatalf("neither side admitted: %v", err)
		}
		if release != nil {
			if !errors.Is(err, ErrBlocked) {
				t.Fatal(err)
			}
			release()
		} else {
			if err = Unfence(ctx, st.DB, "race-task"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestUnresolvedLeaseNeverExpiresByTimestamp(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	_, e := st.DB.Exec("INSERT INTO selfupdate_leases(id,kind,created_at) VALUES('unknown-owner','terminal_pty',0)")
	if e != nil {
		t.Fatal(e)
	}
	e = Fence(ctx, st.DB, "task")
	var blocked *BlockedError
	if !errors.As(e, &blocked) {
		t.Fatal(e)
	}
	if len(blocked.Blockers) != 1 || blocked.Blockers[0].Kind != "terminal_sessions" || blocked.Blockers[0].Count != 1 {
		t.Fatal(blocked)
	}
}
