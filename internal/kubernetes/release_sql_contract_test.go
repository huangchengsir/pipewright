package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Audit every Go SQL literal using suite tables, not a hand-maintained query
// list. Database execution contracts below also run from compiled test binaries.
func TestReleaseSQLIdentifiersAreMySQLCompatible(t *testing.T) {
	if _, e := os.Stat("release_store.go"); e != nil {
		t.Skip("SQL source audit requires repository checkout; compiled binaries run DB contracts separately")
	}
	word := regexp.MustCompile(`(?i)\bcursor\b`)
	quoted := regexp.MustCompile(`'([^']|'')*'`)
	positions := token.NewFileSet()
	checked := 0
	cursorQueries := map[string]bool{}
	e := filepath.WalkDir("..", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, e := parser.ParseFile(positions, path, nil, 0)
		if e != nil {
			return e
		}
		ast.Inspect(file, func(n ast.Node) bool {
			literal, ok := n.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			sql, e := strconv.Unquote(literal.Value)
			if e != nil {
				return true
			}
			lower := strings.ToLower(strings.TrimSpace(sql))
			if !strings.Contains(lower, "k8s_release") && !strings.Contains(lower, "k8s_resource_gates") && !strings.Contains(lower, "k8s_config_payloads") {
				return true
			}
			sqlStart := false
			for _, prefix := range []string{"select ", "insert ", "update ", "delete ", "replace ", "create ", "alter ", "drop "} {
				sqlStart = sqlStart || strings.HasPrefix(lower, prefix)
			}
			if !sqlStart {
				return true
			}
			checked++
			if word.MatchString(quoted.ReplaceAllString(sql, "")) {
				t.Errorf("reserved cursor identifier in release SQL at %s", positions.Position(literal.Pos()))
			}
			if strings.Contains(lower, "batch_cursor") {
				cursorQueries[sql] = true
			}
			return true
		})
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if checked < 20 {
		t.Fatal("release SQL source audit missed statements", checked)
	}
	if len(cursorQueries) < 2 {
		t.Error("batch frontier SELECT/UPDATE contracts missing")
	}
	t.Logf("audited %d release SQL literals", checked)
	if t.Failed() {
		return
	}
	st := storetest.Open(t)
	for sql := range cursorQueries {
		statement, e := st.DB.Prepare(sql)
		if e != nil {
			t.Fatal("frontier SQL does not prepare against migrated schema", e)
		}
		statement.Close()
	}
}
func TestReleaseBatchCursorPersistsWithoutChangingWireKeys(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, plan := releaseStoreFixture(t, st)
		ctx := context.Background()
		r, e := f.s.CreateRelease(ctx, "cursor-contract", "node", "cursor-owner", plan)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.mutateRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, func(_ context.Context, _ dbtx, record *ReleaseRecord) error { record.Cursor = 2; return nil })
		if e != nil {
			t.Fatal(e)
		}
		var cursor int
		if e = st.DB.QueryRow(`SELECT batch_cursor FROM k8s_releases WHERE id=?`, r.ID).Scan(&cursor); e != nil || cursor != 2 {
			t.Fatal("batch frontier did not persist", cursor, e)
		}
		loaded, e := f.s.GetRelease(ctx, r.ID)
		if e != nil || loaded.Cursor != 2 {
			t.Fatal("frontier read did not roundtrip", e)
		}
		for _, value := range []any{loaded, ReleaseSuspension{Cursor: 2}} {
			raw, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			var wire map[string]json.RawMessage
			if json.Unmarshal(raw, &wire) != nil || string(wire["cursor"]) != "2" || wire["batch_cursor"] != nil {
				t.Fatal("public/checkpoint cursor wire key changed")
			}
		}
	})
}
