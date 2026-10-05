package store

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
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
	if len(sq) != 49 || len(my) != 49 || !sq["0050_onboarding_evidence"] || !my["0050_onboarding_evidence"] {
		t.Fatalf("expected 49 paired migrations including evidence: sqlite=%d mysql=%d", len(sq), len(my))
	}
}

func TestOnboardingEvidenceUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{DB: db, Dialect: SQLite}
	if _, err := db.Exec(s.schemaMigrationsDDL()); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.Glob(sqliteMigrationFS, "migrations/sqlite/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry, "0050_") {
			continue
		}
		body, err := sqliteMigrationFS.ReadFile(entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.applyMigration(migrationVersion(entry), string(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO credentials (id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES ('c','c','git_token','',X'00','m','old','old')`,
		`INSERT INTO projects (id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES ('p','p','https://example.com/r','main','c','old','old')`,
		`INSERT INTO pipeline_configs (project_id,created_at,updated_at) VALUES ('p','old','old')`,
		`INSERT INTO pipeline_runs (id,project_id,status,created_at) VALUES ('old','p','success','old')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	var mode, status, updated string
	var saved sql.NullString
	if err := db.QueryRow(`SELECT execution_mode,status FROM pipeline_runs WHERE id='old'`).Scan(&mode, &status); err != nil || mode != "legacy_unknown" || status != "success" {
		t.Fatalf("history = %s/%s, %v", mode, status, err)
	}
	if err := db.QueryRow(`SELECT saved_at,updated_at FROM pipeline_configs WHERE project_id='p'`).Scan(&saved, &updated); err != nil || saved.Valid || updated != "old" {
		t.Fatalf("historical config changed: %v/%s, %v", saved, updated, err)
	}
	if _, err := db.Exec(`INSERT INTO pipeline_runs (id,project_id,created_at) VALUES ('new','p','new')`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT execution_mode FROM pipeline_runs WHERE id='new'`).Scan(&mode); err != nil || mode != "pending" {
		t.Fatalf("new run after reopen = %s, %v", mode, err)
	}
}

func TestEvidenceMigrationStatementsBothDialects(t *testing.T) {
	for name, fsys := range map[string]fs.FS{"sqlite": sqliteMigrationFS, "mysql": mysqlMigrationFS} {
		body, err := fs.ReadFile(fsys, "migrations/"+name+"/0050_onboarding_evidence.sql")
		if err != nil {
			t.Fatal(err)
		}
		stmts := splitStatements(string(body))
		if len(stmts) != 3 || !strings.Contains(stmts[0], "saved_at") || !strings.Contains(stmts[0], "NULL") ||
			!strings.Contains(stmts[1], "NOT NULL DEFAULT 'pending'") || stmts[2] != "UPDATE pipeline_runs SET execution_mode = 'legacy_unknown'" {
			t.Fatalf("%s evidence migration contract: %v", name, stmts)
		}
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
