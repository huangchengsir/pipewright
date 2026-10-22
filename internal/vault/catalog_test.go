package vault

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestSecretCatalogMutationEpoch(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		v := New(st.DB, testKey())
		read := func() int64 {
			t.Helper()
			n, err := configstate.ReadEpoch(ctx, st.DB, configstate.EpochSecrets)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := read()
		c, err := v.Create(CreateInput{Name: "catalog", Type: TypeGitToken, Secret: "synthetic-only"})
		if err != nil {
			t.Fatal(err)
		}
		if read() != before+1 {
			t.Fatal("create did not advance catalog")
		}
		secret := "synthetic-rotated"
		if _, err := v.Update(c.ID, UpdateInput{Secret: &secret}); err != nil {
			t.Fatal(err)
		}
		if read() != before+2 {
			t.Fatal("rotation did not advance catalog")
		}
		if err := configstate.WithReadTx(ctx, st.DB, func(q configstate.DBTX) error {
			found, err := ExistsTx(ctx, q, c.ID)
			if err == nil && !found {
				t.Fatal("same-transaction metadata lookup missed credential")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := v.Delete(c.ID); err != nil {
			t.Fatal(err)
		}
		if read() != before+3 {
			t.Fatal("delete did not advance catalog")
		}
		if err := v.Delete(c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if read() != before+3 {
			t.Fatal("failed delete advanced catalog")
		}
	})
}
