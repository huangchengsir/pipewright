package maintenance

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestFenceIsAtomicWithAdmissionAndAllowsCompletion(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	release, e := Enter(ctx, st.DB, "terminal_pty")
	if e != nil {
		t.Fatal(e)
	}
	if e = Fence(ctx, st.DB, "task"); !errors.Is(e, ErrBlocked) {
		t.Fatal(e)
	}
	release()
	if e = Fence(ctx, st.DB, "task"); e != nil {
		t.Fatal(e)
	}
	if done, e := Enter(ctx, st.DB, "new_work"); e == nil {
		done()
		t.Fatal("admitted behind fence")
	}
	if e = Unfence(ctx, st.DB, "other-task"); e != nil {
		t.Fatal(e)
	}
	if done, e := Enter(ctx, st.DB, "still_blocked"); e == nil {
		done()
		t.Fatal("wrong task removed fence")
	}
	if e = Unfence(ctx, st.DB, "task"); e != nil {
		t.Fatal(e)
	}
	done, e := Enter(ctx, st.DB, "new_work")
	if e != nil {
		t.Fatal(e)
	}
	done()
}
