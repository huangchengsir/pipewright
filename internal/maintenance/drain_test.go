package maintenance

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"sync"
	"testing"
)

func TestDrainClosesConcurrentAdmissionAndAllowsExistingWorkToFinish(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	release, e := Enter(ctx, st.DB, "existing")
	if e != nil {
		t.Fatal(e)
	}
	if e = Drain(ctx, st.DB, "migration"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, e := Enter(ctx, st.DB, "new")
			if e == nil {
				done()
				t.Error("admitted behind migration drain")
			}
		}()
	}
	wg.Wait()
	if e = Inspect(ctx, st.DB); !errors.Is(e, ErrBlocked) {
		t.Fatal("existing work disappeared", e)
	}
	release()
	if e = Inspect(ctx, st.DB); e != nil {
		t.Fatal("completion blocked", e)
	}
	if e = Fence(ctx, st.DB, "updater"); e == nil {
		t.Fatal("update entered during migration")
	}
	if e = Unfence(ctx, st.DB, "other"); e != nil {
		t.Fatal(e)
	}
	if done, e := Enter(ctx, st.DB, "new"); e == nil {
		done()
		t.Fatal("foreign task unfenced migration")
	}
}
