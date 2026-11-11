package store

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresUpgradeReopenAndMigrationRollback(t *testing.T) {
	dsn := os.Getenv("PIPEWRIGHT_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("PIPEWRIGHT_TEST_POSTGRES_DSN is not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "pwtest_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	isolated := cfg.Copy()
	isolated.RuntimeParams["search_path"] = schema
	open := func() *Store {
		return &Store{DB: sql.OpenDB(&bindingConnector{Connector: stdlib.GetConnector(*isolated), dialect: PostgreSQL}), Dialect: PostgreSQL}
	}
	st := open()
	defer func() { _ = st.Close() }()
	if _, err := st.DB.Exec(st.schemaMigrationsDDL()); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.Glob(postgresMigrationFS, "migrations/postgres/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	for _, entry := range entries {
		version := migrationVersion(entry)
		if version >= "0050" {
			break
		}
		sqlText, err := fs.ReadFile(postgresMigrationFS, entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.applyMigration(version, string(sqlText)); err != nil {
			t.Fatal(err)
		}
	}
	binary := []byte{0, 255, 0, 127}
	if _, err := st.DB.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','c','git_token','',?,'m','old','old')`, binary); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES('p','项目','https://example.invalid/r','c','old','old')`,
		`INSERT INTO pipeline_configs(project_id,created_at,updated_at) VALUES('p','old','old')`,
		`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('historic','p','success','old')`,
	} {
		if _, err := st.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.migrate(); err != nil {
		t.Fatal(err)
	}
	var mode, status string
	if err := st.DB.QueryRow(`SELECT execution_mode,status FROM pipeline_runs WHERE id='historic'`).Scan(&mode, &status); err != nil || mode != "legacy_unknown" || status != "success" {
		t.Fatalf("history: %s/%s %v", mode, status, err)
	}
	if _, err := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,created_at) VALUES('new','p','new')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = open()
	if err := st.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT execution_mode FROM pipeline_runs WHERE id='new'`).Scan(&mode); err != nil || mode != "pending" {
		t.Fatalf("reopen modified new row: %s %v", mode, err)
	}
	var got []byte
	if err := st.DB.QueryRow(`SELECT ciphertext FROM credentials WHERE id='c'`).Scan(&got); err != nil || !bytes.Equal(got, binary) {
		t.Fatal("reopen changed ciphertext", err)
	}
	var count int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != 65 {
		t.Fatalf("migration count %d %v", count, err)
	}
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'").Scan(&count); err != nil || count != 109 {
		t.Fatalf("table count %d %v", count, err)
	}
	if err := st.applyMigration("9999_rollback_test", `CREATE TABLE migration_rollback_probe (id BIGINT PRIMARY KEY); INSERT INTO migration_rollback_probe VALUES(1); INSERT INTO migration_rollback_probe VALUES(1);`); err == nil {
		t.Fatal("failing migration succeeded")
	}
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='migration_rollback_probe'").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed DDL was not rolled back", err)
	}
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", "9999_rollback_test").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed migration was recorded", err)
	}
}
