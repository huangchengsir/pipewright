package store

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Replays the actual paired migrations against pre-0055 data. No test repairs
// schema with ad-hoc DDL; MySQL uses its own disposable schema when configured.
func TestConfigControlUpgrade(t *testing.T) {
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
				if strings.Contains(entry, "0055_") {
					continue
				}
				body, err := fs.ReadFile(fsys, entry)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.applyMigration(migrationVersion(entry), string(body)); err != nil {
					t.Fatal(err)
				}
			}
			for _, stmt := range []string{
				`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES ('c','c','git_token','',X'00','m','old','old')`,
				`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES ('p','p','https://example.invalid/r','main','c','old','old')`,
				`INSERT INTO mcp_tokens(id,name,token_hash,project_ids_json,can_trigger,created_at,expires_at) VALUES ('t','old-token','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','["p"]',1,'old','later')`,
				`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES ('queued','p','queued','old'),('running','p','running','old'),('waiting','p','waiting_approval','old'),('done','p','success','old')`,
			} {
				if _, err := s.DB.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.migrate(); err != nil {
				t.Fatal(err)
			}
			if err := s.migrate(); err != nil {
				t.Fatalf("upgrade replay: %v", err)
			}
			var grants string
			var authz, rev, active, canTrigger int64
			if err := s.DB.QueryRow(`SELECT config_grants_json,authz_revision,can_trigger FROM mcp_tokens WHERE id='t'`).Scan(&grants, &authz, &canTrigger); err != nil || grants != "[]" || authz != 0 || canTrigger != 1 {
				t.Fatalf("old token %q/%d/%d/%v", grants, authz, canTrigger, err)
			}
			if err := s.DB.QueryRow(`SELECT revision FROM project_config_versions WHERE project_id='p'`).Scan(&rev); err != nil || rev != 1 {
				t.Fatalf("old revision %d/%v", rev, err)
			}
			if err := s.DB.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE owner_epoch='legacy-unreconciled' AND phase='cleanup_unknown' AND released_at IS NULL`).Scan(&active); err != nil || active != 3 {
				t.Fatalf("old readers %d/%v", active, err)
			}
			for _, key := range []string{"Request", "request"} {
				_, err := s.DB.Exec(`INSERT INTO mcp_config_operations(id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,before_revision,result_json,created_at,updated_at) VALUES (?,?,?,?,?,'draft','approval','owner',1,'{}',1,1)`, key, "t", "p", key, strings.Repeat("a", 64))
				if err != nil {
					t.Fatalf("binary identity %s: %v", key, err)
				}
			}
			if _, err := s.DB.Exec(`DELETE FROM mcp_tokens WHERE id='t'`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(`DELETE FROM pipeline_runs WHERE project_id='p'`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(`DELETE FROM projects WHERE id='p'`); err != nil {
				t.Fatal(err)
			}
			var retained int
			if err := s.DB.QueryRow(`SELECT COUNT(*) FROM mcp_config_operations WHERE token_id='t'`).Scan(&retained); err != nil || retained != 2 {
				t.Fatalf("request identities cascaded %d/%v", retained, err)
			}
			if err := s.DB.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE project_id='p'`).Scan(&retained); err != nil || retained != 3 {
				t.Fatalf("activity evidence cascaded %d/%v", retained, err)
			}
		})
	}
}

func uninitializedUpgradeStore(t *testing.T, dialect Dialect) *Store {
	t.Helper()
	if dialect == SQLite {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "upgrade.db")+"?_pragma=foreign_keys(on)")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return &Store{DB: db, Dialect: SQLite}
	}
	dsn := strings.TrimSpace(os.Getenv("PIPEWRIGHT_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("PIPEWRIGHT_TEST_MYSQL_DSN not configured")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test MySQL DSN")
	}
	cfg.DBName = ""
	cfg.ClientFoundRows = true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("pwupgrade_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4"); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS `" + schema + "`"); _ = admin.Close() })
	cfg.DBName = schema
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{DB: db, Dialect: MySQL}
}
