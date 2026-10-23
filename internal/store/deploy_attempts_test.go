package store

import (
	"io/fs"
	"strings"
	"testing"
)

func TestDeployAttemptMigrationContracts(t *testing.T) {
	for dialect, fsys := range map[string]fs.FS{"sqlite": sqliteMigrationFS, "mysql": mysqlMigrationFS} {
		body, err := fs.ReadFile(fsys, "migrations/"+dialect+"/0056_deploy_attempts.sql")
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, token := range []string{"CREATE TABLE deployment_attempts", "CREATE TABLE deployment_attempt_targets", "CREATE TABLE deployment_attempt_receipts", "source_json", "target_json", "receipt_json", "workload_key", "sequence", "ON DELETE CASCADE"} {
			if !strings.Contains(text, token) {
				t.Fatalf("%s missing %s", dialect, token)
			}
		}
		for _, source := range []string{"REFERENCES pipeline_runs", "REFERENCES projects", "REFERENCES servers", "REFERENCES run_artifacts"} {
			if strings.Contains(text, source) {
				t.Fatalf("%s source retention would delete identity snapshot: %s", dialect, source)
			}
		}
		if len(splitStatements(text)) < 3 {
			t.Fatalf("%s migration statements missing", dialect)
		}
	}
}
