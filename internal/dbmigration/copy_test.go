package dbmigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
	"path/filepath"
	"reflect"
	"testing"
)

func database(t *testing.T, initialized bool) *store.Store {
	t.Helper()
	c := store.OpenConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "test.db")}
	var s *store.Store
	var err error
	if initialized {
		s, err = store.OpenWithConfig(c)
	} else {
		s, err = store.Connect(c)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestManifestMatchesSchema(t *testing.T) {
	s := database(t, true)
	manifest := Manifest()
	if len(manifest) != 106 {
		t.Fatal(len(manifest))
	}
	seen := map[string]bool{}
	cols := 0
	for _, table := range manifest {
		if len(table.keys()) == 0 {
			t.Fatalf("missing key %s", table.Name)
		}
		for _, fk := range table.ForeignKeys {
			if fk.Table != table.Name && !seen[fk.Table] {
				t.Fatalf("dependency %s -> %s", table.Name, fk.Table)
			}
		}
		seen[table.Name] = true
		rows, err := s.DB.Query("PRAGMA table_info(" + store.Identifier(s.Dialect, table.Name) + ")")
		if err != nil {
			t.Fatal(err)
		}
		i := 0
		for rows.Next() {
			var cid, required, pk int
			var name, kind string
			var def any
			if err := rows.Scan(&cid, &name, &kind, &required, &def, &pk); err != nil {
				t.Fatal(err)
			}
			if i >= len(table.Columns) || table.Columns[i].Name != name || table.Columns[i].Key != pk || table.Columns[i].Nullable != (required == 0 && pk == 0) {
				t.Fatalf("schema drift %s.%s", table.Name, name)
			}
			i++
		}
		rows.Close()
		if i != len(table.Columns) {
			t.Fatalf("column count %s", table.Name)
		}
		cols += i
	}
	if cols != 852 {
		t.Fatal(cols)
	}
	var tables int
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != len(manifest)+3 {
		t.Fatalf("unlisted table: %d", tables)
	}
}
func TestCopyResumeAndDetectCorruption(t *testing.T) {
	ctx := context.Background()
	source := database(t, true)
	destination := database(t, false)
	for i, payload := range [][]byte{{}, {0, 255, 128, 1}} {
		_, err := source.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at,username) VALUES(?,?,?,?,?,?,?,?,?)", string(rune('a'+i)), "凭据 α", "git_token", "", payload, "***", "2026-11-11T00:00:00Z", "2026-11-11T00:00:00Z", "")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := source.DB.Exec("INSERT INTO audit_log(id,timestamp,actor,action,target_type,detail_json,created_at) VALUES(?,?,?,?,?,?,?)", "audit", "2026-11-11T00:00:00Z", "test", "test", "test", "{}", "2026-11-11T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	task := uuid.NewString()
	target, err := InitializeTarget(ctx, destination, task)
	if err != nil {
		t.Fatal(err)
	}
	first, err := CopySnapshot(ctx, source, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tables) != 106 {
		t.Fatal(len(first.Tables))
	}
	reopened, err := ResumeTarget(ctx, destination, task, target.Identity())
	if err != nil {
		t.Fatal(err)
	}
	again, err := CopySnapshot(ctx, source, reopened)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, again) {
		t.Fatal("resume proof differs")
	}
	if _, err := destination.DB.Exec("UPDATE audit_log SET actor='tampered'"); err == nil {
		t.Fatal("audit immutability lost")
	}
	if _, err := destination.DB.Exec("UPDATE credentials SET name='changed' WHERE id='a'"); err != nil {
		t.Fatal(err)
	}
	if _, err := CopySnapshot(ctx, source, reopened); !errors.Is(err, ErrMismatch) {
		t.Fatalf("corruption accepted: %v", err)
	}
}
func TestTargetRefusesExistingSchemaAndWrongIdentity(t *testing.T) {
	ctx := context.Background()
	s := database(t, true)
	if _, err := InitializeTarget(ctx, s, uuid.NewString()); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatal(err)
	}
	if _, err := ResumeTarget(ctx, s, uuid.NewString(), uuid.NewString()); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
	s2 := database(t, false)
	target, err := InitializeTarget(ctx, s2, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CopySnapshot(ctx, s2, target); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
}
func TestCanonicalDistinctValuesAndLargeIntegers(t *testing.T) {
	table := Table{Name: "test", Columns: []Column{{Name: "v", Kind: Binary, Nullable: true}}}
	sum := func(v any) string {
		h := sha256.New()
		hashRow(h, table, []any{v})
		return hex.EncodeToString(h.Sum(nil))
	}
	if sum(nil) == sum([]byte{}) {
		t.Fatal("NULL collapsed into empty bytes")
	}
	col := Column{Name: "integer", Kind: Integer}
	got, err := normalize(col, []byte("9223372036854775807"))
	if err != nil || got != int64(9223372036854775807) {
		t.Fatal(got, err)
	}
	if _, err := normalize(col, "secret-invalid-number"); err == nil || err.Error() != ErrMismatch.Error() {
		t.Fatal("unsafe conversion error")
	}
}

func TestUnlistedSourceDataCannotBeSilentlyOmitted(t *testing.T) {
	for _, sql := range []string{"CREATE TABLE user_extension(id INTEGER PRIMARY KEY)", "ALTER TABLE credentials ADD COLUMN unlisted TEXT"} {
		t.Run(sql, func(t *testing.T) {
			ctx := context.Background()
			source := database(t, true)
			dest := database(t, false)
			target, err := InitializeTarget(ctx, dest, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = source.DB.Exec(sql); err != nil {
				t.Fatal(err)
			}
			if _, err = CopySnapshot(ctx, source, target); !errors.Is(err, ErrMismatch) {
				t.Fatal("source schema drift accepted", err)
			}
		})
	}
}
