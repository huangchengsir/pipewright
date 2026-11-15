package store

import (
	"strings"
	"testing"
)

func TestRunAuthorizationSQLServerForeignKeyTypes(t *testing.T) {
	data, err := sqlserverMigrationFS.ReadFile("migrations/sqlserver/0067_optional_tenancy.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	parts := strings.Split(body, "CREATE TABLE [access_run_authorizations]")
	if len(parts) != 2 {
		t.Fatal("run authorization table missing")
	}
	for _, column := range []string{"run_id", "tenant_id", "user_id", "project_id"} {
		if !strings.Contains(parts[1], "["+column+"] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL") {
			t.Fatal("FK storage incompatible", column)
		}
	}
	for _, file := range []string{"0004_projects.sql", "0006_runs.sql", "0067_optional_tenancy.sql"} {
		parent, err := sqlserverMigrationFS.ReadFile("migrations/sqlserver/" + file)
		if err != nil || !strings.Contains(string(parent), "[id] NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL") {
			t.Fatal("parent ID contract changed", file, err)
		}
	}
}
func TestRunAuthorizationMySQLExplicitStorage(t *testing.T) {
	data, err := mysqlMigrationFS.ReadFile("migrations/mysql/0067_optional_tenancy.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "CREATE TABLE access_run_authorizations")
	if len(parts) != 2 || !strings.Contains(parts[1], "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin") {
		t.Fatal("new table must not depend on server default engine/collation")
	}
}

func TestMySQLTenancyCopiesLegacyIDCollation(t *testing.T) {
	for _, collation := range []string{"utf8mb4_bin", "utf8mb4_general_ci", "utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci"} {
		got, err := mysqlLegacyIDDefinition("varchar", 64, "utf8mb4", collation)
		if err != nil || got != "VARCHAR(64) CHARACTER SET utf8mb4 COLLATE "+collation+" NOT NULL" {
			t.Fatalf("%s: %q %v", collation, got, err)
		}
	}
	for _, bad := range []string{"utf8mb4_bin; DROP TABLE projects", "utf8mb4_bin /*", "latin1_bin", "utf8mb4_bin`"} {
		if _, err := mysqlLegacyIDDefinition("varchar", 64, "utf8mb4", bad); err == nil {
			t.Fatal("accepted invalid metadata", bad)
		}
	}
	if _, err := mysqlLegacyIDDefinition("varchar", 128, "utf8mb4", "utf8mb4_bin"); err == nil {
		t.Fatal("accepted incompatible width")
	}
}

func TestDMOptionalTenancyAlterStatementsAreSeparate(t *testing.T) {
	data, err := dmMigrationFS.ReadFile("migrations/dm/0067_optional_tenancy.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, batch := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n-- pipewright:statement\n") {
		if strings.Count(strings.ToUpper(batch), "ALTER TABLE")+strings.Count(strings.ToUpper(batch), "CREATE TABLE")+strings.Count(strings.ToUpper(batch), "CREATE UNIQUE INDEX")+strings.Count(strings.ToUpper(batch), "DROP INDEX") > 1 {
			t.Fatal("multiple ALTER statements in one DM batch")
		}
	}
}
