package storetest

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
)

func TestBinaryCompareAndSwapContract(t *testing.T) {
	ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		// The existing MySQL credentials column is BLOB (65,535 bytes), not
		// LONGBLOB. Exercise its exact limit without changing that schema;
		// the other engines additionally cross the 64 KiB comparison boundary.
		largest := 65537
		if st.Dialect == store.MySQL {
			largest = 65535
		}
		for _, size := range []int{0, 1, largest} {
			t.Run(fmt.Sprint(size), func(t *testing.T) {
				original := bytes.Repeat([]byte{0x81}, size)
				stale := append([]byte(nil), original...)
				if size == 0 {
					stale = []byte{1}
				} else {
					stale[size-1] ^= 1
				}
				id := fmt.Sprintf("binary-cas-%d", size)
				_, e := st.DB.ExecContext(ctx, "INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at,username) VALUES(?,?,?, ?,"+store.BinaryPlaceholder(st.Dialect)+",?,?,?,?)", id, "CAS fixture", "git_token", "", original, "masked", "2026-11-11T00:00:00Z", "2026-11-11T00:00:00Z", "")
				if e != nil {
					t.Fatal(e)
				}
				for _, expected := range [][]byte{stale, nil} {
					result, e := st.DB.ExecContext(ctx, "UPDATE credentials SET name=? WHERE id=? AND "+store.BlobEqual(st.Dialect, "ciphertext"), "incorrect", id, expected)
					if e != nil {
						t.Fatal(e)
					}
					n, e := result.RowsAffected()
					if e != nil || n != 0 {
						t.Fatal("mismatching or NULL cipher updated row", n, e)
					}
				}
				var name string
				var current []byte
				if e = st.DB.QueryRowContext(ctx, "SELECT name,ciphertext FROM credentials WHERE id=?", id).Scan(&name, &current); e != nil {
					t.Fatal(e)
				}
				if name != "CAS fixture" || !bytes.Equal(current, original) {
					t.Fatal("stale comparison changed data")
				}
				result, e := st.DB.ExecContext(ctx, "UPDATE credentials SET name=? WHERE id=? AND "+store.BlobEqual(st.Dialect, "ciphertext"), "matched", id, original)
				if e != nil {
					t.Fatal(e)
				}
				n, e := result.RowsAffected()
				if e != nil || n != 1 {
					t.Fatal("complete matching cipher did not update", n, e)
				}
			})
		}
	})
}
