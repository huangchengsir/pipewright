package store

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"
)

func TestAnomalyAnalysisMigrationPreservesOldData(t *testing.T) {
	for dialect, fsys := range map[string]fs.FS{"sqlite": sqliteMigrationFS, "mysql": mysqlMigrationFS} {
		body, err := fs.ReadFile(fsys, "migrations/"+dialect+"/0053_anomaly_ai_analysis.sql")
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range []string{"ai_analysis_enabled", "DEFAULT 0", "analysis_status", "analysis_json", "rule_id"} {
			if !strings.Contains(string(body), column) {
				t.Fatalf("%s missing %s", dialect, column)
			}
		}
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{`CREATE TABLE anomaly_rules(id TEXT PRIMARY KEY)`, `CREATE TABLE anomaly_alerts(id TEXT PRIMARY KEY)`, `INSERT INTO anomaly_rules(id) VALUES('old-rule')`, `INSERT INTO anomaly_alerts(id) VALUES('old-alert')`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := fs.ReadFile(sqliteMigrationFS, "migrations/sqlite/0053_anomaly_ai_analysis.sql")
	for _, q := range splitStatements(string(body)) {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var enabled int
	var status, rule string
	var result sql.NullString
	if err = db.QueryRow(`SELECT ai_analysis_enabled FROM anomaly_rules WHERE id='old-rule'`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("old rule enabled=%d %v", enabled, err)
	}
	if err = db.QueryRow(`SELECT ai_analysis_enabled,rule_id,analysis_status,analysis_json FROM anomaly_alerts WHERE id='old-alert'`).Scan(&enabled, &rule, &status, &result); err != nil || enabled != 0 || rule != "" || status != "" || result.Valid {
		t.Fatalf("old alert changed: %d %s %s %+v %v", enabled, rule, status, result, err)
	}
}
