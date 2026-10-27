package store

import (
	"io/fs"
	"strings"
	"testing"
)

// Upgrade the released pre-guard schema rather than merely building a fresh
// database. Both dialects use the real paired migration files; MySQL runs only
// against the explicitly configured disposable test instance.
func TestReleaseGuardUpgradePreservesLegacyWithoutInventedEvidence(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, MySQL} {
		t.Run(dialect.String(), func(t *testing.T) {
			s := uninitializedUpgradeStore(t, dialect)
			if _, err := s.DB.Exec(s.schemaMigrationsDDL()); err != nil {
				t.Fatal(err)
			}
			fsys, glob := s.migrationFS()
			entries, err := fs.Glob(fsys, glob)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				version := migrationVersion(entry)
				if version >= "0056_" {
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
				`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('legacy-c','legacy','git_token','',X'00','m','old','old')`,
				`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('legacy-p','legacy','https://example.invalid/app','main','legacy-c','old','old')`,
				`INSERT INTO pipeline_runs(id,project_id,status,trigger_commit,resolved_environment,created_at) VALUES('legacy-r','legacy-p','success','abc1234','prod','old')`,
				`INSERT INTO deploy_targets(id,run_id,server_id,server_name,status,message,started_at,finished_at) VALUES('legacy-target','legacy-r','deleted-server','old-server','success','historical','old','old')`,
			} {
				if _, e := s.DB.Exec(q); e != nil {
					t.Fatal(e)
				}
			}
			if err = s.migrate(); err != nil {
				t.Fatal(err)
			}
			if err = s.migrate(); err != nil {
				t.Fatal("reopen migration replay", err)
			}
			var count int
			if err = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 59 {
				t.Fatal(count, err)
			}
			for _, table := range []string{"deployment_attempts", "deployment_attempt_targets", "deployment_attempt_receipts", "release_guard_plans", "release_guards", "release_guard_evidence", "release_guard_events", "release_guard_analyses"} {
				if err = s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatal("legacy data was invented as verified evidence", table, count, err)
				}
			}
			var commit, full, status string
			var conflict int
			if err = s.DB.QueryRow(`SELECT trigger_commit,resolved_commit_sha,resolved_commit_conflict,status FROM pipeline_runs WHERE id='legacy-r'`).Scan(&commit, &full, &conflict, &status); err != nil || commit != "abc1234" || full != "" || conflict != 0 || status != "success" {
				t.Fatal(commit, full, conflict, status, err)
			}
			var message string
			if err = s.DB.QueryRow(`SELECT message FROM deploy_targets WHERE id='legacy-target'`).Scan(&message); err != nil || strings.TrimSpace(message) != "historical" {
				t.Fatal(message, err)
			}
		})
	}
}
