package main

import (
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/dbmigration"
	"testing"
)

func TestDatabaseManagedCredentialOverride(t *testing.T) {
	t.Setenv("PIPEWRIGHT_DB_CREDENTIAL_ROTATION", "1")
	active := &dbmigration.ActiveConfig{Managed: true, Connection: dbmigration.Connection{Driver: "postgres", DSN: "saved"}}
	for _, tc := range []struct {
		name, driver, dsn string
		phase             dbmigration.Phase
		want              bool
	}{
		{"completed same engine", "postgres", "new", dbmigration.Completed, true},
		{"cancelled same engine", "postgres", "new", dbmigration.Aborted, true},
		{"prepared must invalidate", "postgres", "new", dbmigration.Prepared, true},
		{"old source environment", "mysql", "old", dbmigration.Completed, false},
		{"no supplied credentials", "postgres", "", dbmigration.Completed, false},
		{"copying frozen connection", "postgres", "new", dbmigration.Copying, false},
		{"activation frozen connection", "postgres", "new", dbmigration.AwaitingRestart, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{DBDriver: tc.driver, DBDSN: tc.dsn}
			if got := databaseCredentialOverride(cfg, active, &dbmigration.State{Phase: tc.phase}); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestManagedDatabaseIgnoresStaleSameEngineEnvironment(t *testing.T) {
	t.Setenv("PIPEWRIGHT_DB_CREDENTIAL_ROTATION", "")
	for _, driver := range []string{"sqlite", "mysql", "postgres", "dm", "sqlserver"} {
		active := &dbmigration.ActiveConfig{Managed: true, Connection: dbmigration.Connection{Driver: driver, DSN: "new-current"}}
		if databaseCredentialOverride(&config.Config{DBDriver: driver, DBDSN: "old-original-source"}, active, &dbmigration.State{Phase: dbmigration.Completed}) {
			t.Fatal("stale environment overrode managed", driver)
		}
	}
	t.Setenv("PIPEWRIGHT_DB_CREDENTIAL_ROTATION", "1")
	active := &dbmigration.ActiveConfig{Managed: true, Connection: dbmigration.Connection{Driver: "sqlite", DSN: "new-current.db"}}
	if databaseCredentialOverride(&config.Config{DBDriver: "sqlite", DBDSN: "old-source.db"}, active, &dbmigration.State{Phase: dbmigration.Completed}) {
		t.Fatal("SQLite file replacement treated as credential rotation")
	}
}
