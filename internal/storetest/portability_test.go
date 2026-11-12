package storetest_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// This contract executes against actual stores. Missing external DSNs are
// reported as skipped, never counted as PostgreSQL/MySQL acceptance.
func TestDatabasePortabilityContract(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		blobType := "BLOB"
		textType := "VARCHAR(255)"
		if st.Dialect == store.SQLServer {
			blobType = "VARBINARY(MAX)"
			textType = "NVARCHAR(255) COLLATE Latin1_General_100_BIN2"
		}
		if st.Dialect == store.PostgreSQL {
			blobType = "BYTEA"
		}
		if _, err := st.DB.Exec("CREATE TABLE portability_contract (id VARCHAR(64) PRIMARY KEY, label " + textType + " NOT NULL, enabled BIGINT NOT NULL, payload " + blobType + " NOT NULL, optional " + textType + ", nullable_payload " + blobType + ", revision BIGINT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		binary := []byte{0, 255, 128, 0, 39, 63}
		stmt, err := st.DB.PrepareContext(ctx, "INSERT INTO portability_contract(id,label,enabled,payload,optional,revision) VALUES(?,?,?,?,?,?)")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.ExecContext(ctx, "a", "中文🙂Éé_%!?'", true, binary, nil, 1); err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.ExecContext(ctx, "empty", "", sql.NullBool{Valid: true}, []byte{}, "", 1); err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		var label string
		var enabled bool
		var got []byte
		var optional sql.NullString
		if err = st.DB.QueryRowContext(ctx, "SELECT label,enabled,payload,optional FROM portability_contract WHERE id=?", "a").Scan(&label, &enabled, &got, &optional); err != nil {
			t.Fatal(err)
		}
		if label != "中文🙂Éé_%!?'" || !enabled || !bytes.Equal(got, binary) || optional.Valid {
			t.Fatal("text, bool, binary or NULL roundtrip changed")
		}
		if err = st.DB.QueryRow("SELECT label,enabled,payload,optional FROM portability_contract WHERE id=?", "empty").Scan(&label, &enabled, &got, &optional); err != nil {
			t.Fatal(err)
		}
		if label != "" || enabled || len(got) != 0 || !optional.Valid || optional.String != "" {
			t.Fatal("empty and NULL conflated")
		}
		// Both typed nil (ordinary domain calls) and untyped nil (manifest rows)
		// must remain SQL NULL. A non-nil empty slice must remain an empty binary value.
		for _, sample := range []struct {
			value  any
			mark   string
			isNull int
		}{{[]byte(nil), "?", 1}, {[]byte{}, "?", 0}, {nil, store.BinaryPlaceholder(st.Dialect), 1}} {
			if _, e := st.DB.Exec("UPDATE portability_contract SET nullable_payload="+sample.mark+" WHERE id=?", sample.value, "a"); e != nil {
				t.Fatal(e)
			}
			var isNull int
			if e := st.DB.QueryRow("SELECT CASE WHEN nullable_payload IS NULL THEN 1 ELSE 0 END FROM portability_contract WHERE id=?", "a").Scan(&isNull); e != nil || isNull != sample.isNull {
				t.Fatalf("binary NULL semantics: got=%d want=%d error=%v", isNull, sample.isNull, e)
			}
		}
		result, err := st.DB.Exec("UPDATE portability_contract SET label=? WHERE id=?", "", "empty")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("same-value update matched=%d %v", n, err)
		}
		for i := 0; i < 2; i++ {
			result, err = st.DB.Exec("UPDATE portability_contract SET revision=revision+1 WHERE id=? AND revision=?", "a", 1)
			if err != nil {
				t.Fatal(err)
			}
			n, _ := result.RowsAffected()
			if n != int64(1-i) {
				t.Fatalf("CAS result %d", n)
			}
		}
		if _, err = st.DB.Exec("INSERT INTO portability_contract(id,label,enabled,payload,revision) VALUES(?,?,?,?,?)", "a", "", 0, binary, 1); !store.IsUniqueErr(err) {
			t.Fatalf("unique classification: %v", err)
		}
		tx, err := st.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE portability_contract SET label=? WHERE id=?", "rolled back", "a"); err != nil {
			t.Fatal(err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = st.DB.QueryRow("SELECT label FROM portability_contract WHERE id=?", "a").Scan(&label); err != nil || label != "中文🙂Éé_%!?'" {
			t.Fatalf("rollback: %q %v", label, err)
		}
		q := store.InsertSQL(st.Dialect, "portability_contract", []string{"id", "label", "enabled", "payload", "optional", "revision"}, []string{"?", "?", "?", "?", "?", "?"}, []string{"id"}, []string{"label"})
		if _, err = st.DB.Exec(q, "a", "upserted", false, binary, nil, 100); err != nil {
			t.Fatal(err)
		}
		var revision int64
		if err = st.DB.QueryRow("SELECT label,revision FROM portability_contract WHERE id=?", "a").Scan(&label, &revision); err != nil || label != "upserted" || revision != 2 {
			t.Fatal("upsert changed preserved columns", err)
		}
		if _, err = st.DB.Exec("INSERT INTO audit_log(id,timestamp,actor,action,target_type,detail_json,created_at) VALUES(?,?,?,?,?,?,?)", "audit", "now", "test", "test", "test", "{}", "now"); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec("UPDATE audit_log SET actor=? WHERE id=?", "changed", "audit"); err == nil {
			t.Fatal("audit update accepted")
		}
		if _, err = st.DB.Exec("DELETE FROM audit_log WHERE id=?", "audit"); err == nil {
			t.Fatal("audit delete accepted")
		}
		if _, err = st.DB.Exec("INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?)", "orphan", "orphan", "url", "missing", "now", "now"); !store.IsForeignKeyErr(err) {
			t.Fatalf("foreign key classification: %v", err)
		}
		if err = st.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&revision); err != nil || revision != 65 {
			t.Fatalf("migrations=%d %v", revision, err)
		}
		if st.Dialect == store.PostgreSQL {
			deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			_, err = st.DB.ExecContext(deadline, "SELECT pg_sleep(5)")
			cancel()
			if err == nil {
				t.Fatal("query ignored deadline")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Log("driver cancellation returned a PostgreSQL error")
			}
			readonly, err := st.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = readonly.ExecContext(ctx, "UPDATE portability_contract SET revision=3 WHERE id=?", "a"); err == nil {
				t.Fatal("read-only transaction wrote")
			}
			_ = readonly.Rollback()
		}
	})
}
