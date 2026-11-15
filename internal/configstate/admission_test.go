package configstate

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestWriteAdmissionPrecedesBusinessLocksAndCannotBeAddedLate(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		seedProject(t, st)
		calls := 0
		ctx := WithWriteAdmission(context.Background(), func(ctx context.Context, db *sql.DB) error {
			calls++
			var count int
			if err := Queryer(ctx, db).QueryRowContext(ctx, "SELECT COUNT(*) FROM project_config_versions WHERE project_id='p'").Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				t.Fatal("business project lock preceded admission")
			}
			return nil
		})
		if err := MutateProject(ctx, st.DB, "p", func(context.Context, DBTX) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatal(calls)
		}
		blocked := errors.New("rejected admission")
		called := false
		deny := WithWriteAdmission(context.Background(), func(context.Context, *sql.DB) error { return blocked })
		if err := WithWriteTx(deny, st.DB, func(DBTX) error { called = true; return nil }); !errors.Is(err, blocked) || called {
			t.Fatal(err, called)
		}
		if err := WithWriteTxContext(context.Background(), st.DB, func(txctx context.Context, q DBTX) error {
			late := WithWriteAdmission(txctx, func(context.Context, *sql.DB) error { t.Fatal("late admission ran"); return nil })
			return WithWriteTx(late, st.DB, func(DBTX) error { t.Fatal("late actor escaped fence"); return nil })
		}); err == nil {
			t.Fatal("late context accepted")
		}
	})
}
