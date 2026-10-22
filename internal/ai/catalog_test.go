package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestSaveSecretCatalogEpoch(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := New(st.DB, vault.New(st.DB, testMasterKey()), nil)
		read := func() int64 {
			t.Helper()
			n, err := configstate.ReadEpoch(ctx, st.DB, configstate.EpochSecrets)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := read()
		key := "synthetic-key"
		in := SaveInput{Provider: ProviderOpenAI, APIKey: &key}
		if _, err := s.Save(ctx, in); err != nil {
			t.Fatal(err)
		}
		if read() != before+1 {
			t.Fatal("save did not advance catalog")
		}
		key = "synthetic-key-rotated"
		if _, err := s.Save(ctx, in); err != nil {
			t.Fatal(err)
		}
		if read() != before+2 {
			t.Fatal("rotation did not advance catalog")
		}
		if _, err := s.Save(ctx, SaveInput{Provider: "invalid"}); !errors.Is(err, ErrInvalidProvider) {
			t.Fatal(err)
		}
		if read() != before+2 {
			t.Fatal("failed save advanced catalog")
		}
	})
}
