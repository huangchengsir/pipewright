package store

import (
	"io/fs"
	"testing"
)

// The pre-retry v1.9.6 database is upgraded with both real migration sets.
// Historical failure flags never become invented manifests/checkpoints.
func TestRunRetryUpgradePreservesHistoricalEvidenceBoundary(t *testing.T) {
	for _, d := range []Dialect{SQLite, MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			s := uninitializedUpgradeStore(t, d)
			if _, e := s.DB.Exec(s.schemaMigrationsDDL()); e != nil {
				t.Fatal(e)
			}
			fsys, glob := s.migrationFS()
			entries, e := fs.Glob(fsys, glob)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				version := migrationVersion(entry)
				if version >= "0061_" {
					continue
				}
				body, e := fs.ReadFile(fsys, entry)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.applyMigration(version, string(body)); e != nil {
					t.Fatal(e)
				}
			}
			for _, q := range []string{
				`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('old-c','old','git_token','',X'00','m','old','old')`,
				`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('old-p','old','https://example.invalid/p','main','old-c','old','old')`,
				`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('old-r','old-p','failed','old')`,
			} {
				if _, e = s.DB.Exec(q); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.migrate(); e != nil {
				t.Fatal(e)
			}
			if e = s.migrate(); e != nil {
				t.Fatal("reopen", e)
			}
			for _, table := range []string{"run_retry_manifests", "run_retry_sources", "run_retry_lineages", "run_retry_attempts", "run_retry_nodes", "run_retry_requests", "run_retry_dispatch", "run_retry_refs", "run_retry_effects", "run_retry_external_targets", "run_retry_resources", "run_retry_reader_exits"} {
				var count int
				if e = s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil || count != 0 {
					t.Fatal("invented recovery evidence", table, count, e)
				}
			}
			var status string
			if e = s.DB.QueryRow("SELECT status FROM pipeline_runs WHERE id='old-r'").Scan(&status); e != nil || status != "failed" {
				t.Fatal(status, e)
			}
		})
	}
}
