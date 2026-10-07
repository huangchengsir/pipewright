package store

import (
	"io/fs"
	"strings"
	"testing"
)

func TestOpsChatSchemaBothDialects(t *testing.T) {
	names := []string{"ops_chat_sessions", "ops_chat_entries", "ops_chat_runs", "ops_chat_calls", "ops_chat_approvals", "ops_chat_state"}
	for dialect, fsys := range map[string]fs.FS{"sqlite": sqliteMigrationFS, "mysql": mysqlMigrationFS} {
		body, err := fs.ReadFile(fsys, "migrations/"+dialect+"/0051_ops_chat.sql")
		if err != nil {
			t.Fatal(err)
		}
		stmts := splitStatements(string(body))
		if len(stmts) != 8 {
			t.Fatal(dialect, len(stmts))
		}
		for _, name := range names {
			if !strings.Contains(string(body), "CREATE TABLE IF NOT EXISTS "+name) {
				t.Fatal(dialect, name)
			}
		}
		for _, clause := range []string{"request_id", "UNIQUE", "payload_hash", "args_hash", "target_hash", "context_allowed", "ON DELETE CASCADE", "keycheck", "lease_until", "ordinal INTEGER NOT NULL", "CREATE UNIQUE INDEX ops_chat_calls_run ON ops_chat_calls(run_id,ordinal)"} {
			if !strings.Contains(string(body), clause) {
				t.Fatal(dialect, clause)
			}
		}
	}
	s, err := Open(t.TempDir() + "/ops.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range names {
		var count int
		if err = s.DB.QueryRow("SELECT COUNT(*) FROM " + name).Scan(&count); err != nil {
			t.Fatal(name, err)
		}
	}
	var n int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version='0051_ops_chat'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err = s.DB.Exec(`INSERT INTO ops_chat_sessions(id,revision,body,created_at,updated_at) VALUES('session',0,X'01',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES('run','session','request','hash','queued',X'01',1)`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,'session','run','server','systemd_action','queued','hash','target',X'01',1,?)`
	for i, id := range []string{"z-stop", "a-start"} {
		if _, err = s.DB.Exec(insert, id, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.Exec(insert, "duplicate", 0); err == nil {
		t.Fatal("duplicate plan ordinal accepted")
	}
	rows, err := s.DB.Query(`SELECT id FROM ops_chat_calls WHERE run_id='run' ORDER BY ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil || strings.Join(ids, ",") != "z-stop,a-start" {
		t.Fatal("schema did not preserve ordinal order", ids, rows.Err())
	}
}
