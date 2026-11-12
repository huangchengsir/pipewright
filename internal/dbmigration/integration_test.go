package dbmigration

import (
	"context"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestSQLitePostgresMigrationDirections(t *testing.T)  { testMigrationPair(t, store.PostgreSQL) }
func TestSQLiteSQLServerMigrationDirections(t *testing.T) { testMigrationPair(t, store.SQLServer) }
func TestSQLiteDMMigrationDirections(t *testing.T)        { testMigrationPair(t, store.DM) }
func testMigrationPair(t *testing.T, external store.Dialect) {
	for _, pair := range [][2]store.Dialect{{store.SQLite, external}, {external, store.SQLite}} {
		t.Run(pair[0].String()+"_to_"+pair[1].String(), func(t *testing.T) {
			source := storetest.OpenForDialect(t, pair[0])
			dest := storetest.OpenEmptyForDialect(t, pair[1])
			ctx := context.Background()
			for i, payload := range [][]byte{{}, {0, 255, 128, 1}} {
				_, err := source.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at,username) VALUES(?,?,?,?,?,?,?,?,?)", string(rune('a'+i)), "凭据 α", "git_token", "", payload, "***", "2026-11-11T00:00:00Z", "2026-11-11T00:00:00Z", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := source.DB.Exec("INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", "project", "迁移项目", "https://invalid.example/repo", "main", "a", "2026-11-11T00:00:00Z", "2026-11-11T00:00:00Z")
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.DB.Exec("INSERT INTO audit_log(id,timestamp,actor,action,target_type,detail_json,created_at) VALUES(?,?,?,?,?,?,?)", "audit", "2026-11-11T00:00:00Z", "test", "test", "test", "{}", "2026-11-11T00:00:00Z")
			if err != nil {
				t.Fatal(err)
			}
			target, err := InitializeTarget(ctx, dest, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			report, err := CopySnapshot(ctx, source, target)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := VerifySnapshot(ctx, source, target)
			if err != nil {
				t.Fatal(err)
			}
			if reportDigest(report) != reportDigest(verified) {
				t.Fatal("fresh verification differs")
			}
			if _, err := dest.DB.Exec("UPDATE audit_log SET actor='changed'"); err == nil {
				t.Fatal("audit update allowed")
			}
			if _, err := dest.DB.Exec("DELETE FROM audit_log"); err == nil {
				t.Fatal("audit delete allowed")
			}
			if _, err := dest.DB.Exec("DELETE FROM credentials WHERE id='a'"); err == nil {
				t.Fatal("foreign key guard lost")
			}
			t.Logf("verified %d business tables with Unicode/empty/blob/NULL/FK/audit fixtures", len(report.Tables))
		})
	}
}
