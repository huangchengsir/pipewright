package store

import (
	"io/fs"
	"testing"
)

// TestMigrationSetsMatch 防漂移:sqlite 与 mysql 两套迁移的版本键集合必须完全一致。
// 任何新增迁移漏写另一方言即在此红。
func TestMigrationSetsMatch(t *testing.T) {
	sq := migrationVersions(t, sqliteMigrationFS, "migrations/sqlite/*.sql")
	my := migrationVersions(t, mysqlMigrationFS, "migrations/mysql/*.sql")

	for v := range sq {
		if !my[v] {
			t.Errorf("版本 %s 有 sqlite 迁移但缺 mysql", v)
		}
	}
	for v := range my {
		if !sq[v] {
			t.Errorf("版本 %s 有 mysql 迁移但缺 sqlite", v)
		}
	}
	if len(sq) == 0 {
		t.Fatal("未找到任何迁移")
	}
}

func migrationVersions(t *testing.T, fsys fs.FS, glob string) map[string]bool {
	t.Helper()
	entries, err := fs.Glob(fsys, glob)
	if err != nil {
		t.Fatalf("glob %s: %v", glob, err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[migrationVersion(e)] = true
	}
	return out
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"comment only", "-- just a comment\n", nil},
		{
			"two statements with comments",
			"-- header\nCREATE TABLE a (id INT);\n-- mid\nCREATE INDEX i ON a (id);\n",
			[]string{"CREATE TABLE a (id INT)", "CREATE INDEX i ON a (id)"},
		},
		{
			"semicolon inside single quote not split",
			"INSERT INTO t VALUES ('a;b');",
			[]string{"INSERT INTO t VALUES ('a;b')"},
		},
		{
			"trigger single statement",
			"CREATE TRIGGER x BEFORE UPDATE ON t FOR EACH ROW\nSIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'no: update';",
			[]string{"CREATE TRIGGER x BEFORE UPDATE ON t FOR EACH ROW\nSIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'no: update'"},
		},
		{
			"inline trailing comment stripped",
			"SELECT 1; -- trailing\nSELECT 2;",
			[]string{"SELECT 1", "SELECT 2"},
		},
	}
	for _, c := range cases {
		got := splitStatements(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %d stmts %q, want %d %q", c.name, len(got), got, len(c.want), c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s[%d]: got %q want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}
