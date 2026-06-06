package store

import "testing"

func TestUpsertSuffix(t *testing.T) {
	conflict := []string{"project_id"}
	update := []string{"chain_json", "updated_at"}

	if got, want := UpsertSuffix(SQLite, conflict, update),
		"ON CONFLICT(project_id) DO UPDATE SET chain_json = excluded.chain_json, updated_at = excluded.updated_at"; got != want {
		t.Errorf("sqlite:\n got %q\nwant %q", got, want)
	}
	if got, want := UpsertSuffix(MySQL, conflict, update),
		"ON DUPLICATE KEY UPDATE chain_json = VALUES(chain_json), updated_at = VALUES(updated_at)"; got != want {
		t.Errorf("mysql:\n got %q\nwant %q", got, want)
	}
}

func TestUpsertSuffixMultiConflict(t *testing.T) {
	conflict := []string{"run_id", "stage_id"}
	update := []string{"stage_name"}
	if got, want := UpsertSuffix(SQLite, conflict, update),
		"ON CONFLICT(run_id, stage_id) DO UPDATE SET stage_name = excluded.stage_name"; got != want {
		t.Errorf("sqlite:\n got %q\nwant %q", got, want)
	}
	if got, want := UpsertSuffix(MySQL, conflict, update),
		"ON DUPLICATE KEY UPDATE stage_name = VALUES(stage_name)"; got != want {
		t.Errorf("mysql:\n got %q\nwant %q", got, want)
	}
}

func TestUpsertAssignSuffix(t *testing.T) {
	// approval 混合场景:字面量赋值 + 取插入值。
	conflict := []string{"run_id", "stage_id"}
	sqliteAssigns := []string{"status = 'pending'", "decided_by = ''", "decided_at = ''", "stage_name = " + Excluded(SQLite, "stage_name")}
	if got, want := UpsertAssignSuffix(SQLite, conflict, sqliteAssigns),
		"ON CONFLICT(run_id, stage_id) DO UPDATE SET status = 'pending', decided_by = '', decided_at = '', stage_name = excluded.stage_name"; got != want {
		t.Errorf("sqlite:\n got %q\nwant %q", got, want)
	}
	mysqlAssigns := []string{"status = 'pending'", "decided_by = ''", "decided_at = ''", "stage_name = " + Excluded(MySQL, "stage_name")}
	if got, want := UpsertAssignSuffix(MySQL, conflict, mysqlAssigns),
		"ON DUPLICATE KEY UPDATE status = 'pending', decided_by = '', decided_at = '', stage_name = VALUES(stage_name)"; got != want {
		t.Errorf("mysql:\n got %q\nwant %q", got, want)
	}
}

func TestDoNothingSuffix(t *testing.T) {
	conflict := []string{"project_id"}
	if got, want := DoNothingSuffix(SQLite, conflict), "ON CONFLICT(project_id) DO NOTHING"; got != want {
		t.Errorf("sqlite:\n got %q\nwant %q", got, want)
	}
	if got, want := DoNothingSuffix(MySQL, conflict), "ON DUPLICATE KEY UPDATE project_id = project_id"; got != want {
		t.Errorf("mysql:\n got %q\nwant %q", got, want)
	}
}

func TestExcluded(t *testing.T) {
	if got, want := Excluded(SQLite, "x"), "excluded.x"; got != want {
		t.Errorf("sqlite: got %q want %q", got, want)
	}
	if got, want := Excluded(MySQL, "x"), "VALUES(x)"; got != want {
		t.Errorf("mysql: got %q want %q", got, want)
	}
}

func TestDialectString(t *testing.T) {
	if SQLite.String() != "sqlite" || MySQL.String() != "mysql" {
		t.Errorf("unexpected dialect strings: %q %q", SQLite, MySQL)
	}
}
