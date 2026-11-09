package store

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestKubernetesReleaseMigrationCursorUsesPortableIdentifier(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\bcursor\b`)
	for _, dialect := range []Dialect{SQLite, MySQL} {
		t.Run(dialect.String(), func(t *testing.T) {
			s := &Store{Dialect: dialect}
			files, _ := s.migrationFS()
			body, e := fs.ReadFile(files, "migrations/"+dialect.String()+"/0065_kubernetes_release_suite.sql")
			if e != nil {
				t.Fatal(e)
			}
			if forbidden.Match(body) {
				t.Error("0065 uses MySQL reserved CURSOR as a column identifier")
			}
			if !strings.Contains(string(body), "batch_cursor ") {
				t.Error("paired0065 lacks the portable batch frontier column")
			}
		})
	}
}
func TestKubernetesReleaseMigrationFrom0064CreatesPortableCursor(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, MySQL} {
		t.Run(dialect.String(), func(t *testing.T) {
			s := uninitializedUpgradeStore(t, dialect)
			if _, e := s.DB.Exec(s.schemaMigrationsDDL()); e != nil {
				t.Fatal(e)
			}
			files, glob := s.migrationFS()
			entries, e := fs.Glob(files, glob)
			if e != nil {
				t.Fatal(e)
			}
			for _, name := range entries {
				version := migrationVersion(name)
				if version >= "0065_" {
					continue
				}
				body, e := fs.ReadFile(files, name)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.applyMigration(version, string(body)); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.migrate(); e != nil {
				t.Fatal("release-suite upgrade failed", e)
			}
			statement, e := s.DB.Prepare(`SELECT batch_cursor FROM k8s_releases WHERE id=?`)
			if e != nil {
				t.Fatal("portable frontier column unavailable", e)
			}
			statement.Close()
			var count int
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, "0065_kubernetes_release_suite").Scan(&count); e != nil || count != 1 {
				t.Fatal("0065 not applied exactly once", count, e)
			}
			if e = s.migrate(); e != nil {
				t.Fatal("migration replay", e)
			}
		})
	}
}
