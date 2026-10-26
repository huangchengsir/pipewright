package configstate

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/completedrefs"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewNestedTransactionNeverFlushesRawDBCleanup(t *testing.T) {
	st := storetest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls atomic.Int32
	e := WithWriteTxContext(ctx, st.DB, func(bound context.Context, q DBTX) error {
		if _, e := q.ExecContext(bound, "INSERT INTO selfupdate_leases(id,kind,created_at) VALUES('nested-proof','local',7)"); e != nil {
			return e
		}
		canceled, done := context.WithCancel(bound)
		done()
		e := completedrefs.Complete(canceled, st.DB, "nested-proof", "nested-proof", "owner", func(retry context.Context) error {
			calls.Add(1)
			_, e := st.DB.ExecContext(retry, "DELETE FROM selfupdate_leases WHERE id='nested-proof' AND kind='local' AND created_at=7")
			return e
		})
		if !errors.Is(e, context.Canceled) {
			return errors.New("canceled attempt lost its completion proof")
		}
		return WithWriteTx(bound, st.DB, func(q DBTX) error {
			var n int
			if e := q.QueryRowContext(bound, "SELECT COUNT(*) FROM selfupdate_leases WHERE id='nested-proof'").Scan(&n); e != nil {
				return e
			}
			if n != 1 || calls.Load() != 0 {
				return errors.New("nested transaction ran raw DB cleanup")
			}
			return nil
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = WithWriteTx(ctx, st.DB, func(q DBTX) error {
		var n int
		e := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM selfupdate_leases WHERE id='nested-proof'").Scan(&n)
		if e != nil {
			return e
		}
		if n != 0 || calls.Load() != 1 {
			return errors.New("next outer write failed to flush confirmed bookkeeping")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
