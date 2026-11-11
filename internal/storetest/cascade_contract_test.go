package storetest_test

import (
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestCascadeAndNullableUniqueContract(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.DB.Exec(q, args...); err != nil {
				t.Fatal(err)
			}
		}
		count := func(table string, want int) {
			t.Helper()
			var n int
			if err := s.DB.QueryRow("SELECT COUNT(*) FROM " + store.Identifier(s.Dialect, table)).Scan(&n); err != nil || n != want {
				t.Fatalf("%s count=%d want=%d err=%v", table, n, want, err)
			}
		}
		exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", "cred", "cred", "git_token", "", []byte{}, "***", "now", "now")
		for _, id := range []string{"one", "two"} {
			exec("INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?)", id, id, "url", "cred", "now", "now")
			exec("INSERT INTO pipeline_runs(id,project_id,created_at) VALUES(?,?,?)", id, id, "now")
			exec("INSERT INTO run_steps(id,run_id,name) VALUES(?,?,?)", id, id, "step")
			exec("INSERT INTO run_artifacts(id,run_id,type,name,"+store.Identifier(s.Dialect, "reference")+",created_at) VALUES(?,?,?,?,?,?)", id, id, "archive", "artifact", "/unchanged/path", "now")
			exec("INSERT INTO selfupdate_tasks(id,instance_id,request_id,target_tag,status,active_key,record_json,created_at) VALUES(?,?,?,?,?,?,?,?)", id, "instance", id, "v1.0.0", "succeeded", nil, "{}", 1)
		}
		exec("INSERT INTO project_chain_targets(id,project_id,downstream_project_id,created_at,updated_at) VALUES(?,?,?,?,?)", "chain", "one", "two", "now", "now")
		tx, err := s.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec("DELETE FROM projects"); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"projects", "pipeline_runs", "run_steps", "run_artifacts"} {
			count(table, 2)
		}
		count("project_chain_targets", 1)
		result, err := s.DB.Exec("DELETE FROM projects")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 2 {
			t.Fatalf("outer delete matched=%d want=2 err=%v", n, err)
		}
		for _, table := range []string{"projects", "pipeline_runs", "run_steps", "run_artifacts", "project_chain_targets"} {
			count(table, 0)
		}
		count("credentials", 1)
		count("selfupdate_tasks", 2)
		// One child is reachable from both session and run: multi-path cascades remain atomic.
		for _, id := range []string{"one", "two"} {
			exec("INSERT INTO ops_chat_sessions(id,revision,body,created_at,updated_at) VALUES(?,?,?,?,?)", id, 1, []byte{}, 1, 1)
			exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", id, id, id, "hash", "done", []byte{}, 1)
			exec("INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,ordinal,args_hash,target_hash,body,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, id, id, "server", "tool", "done", 1, "hash", "hash", []byte{}, 1)
		}
		exec("DELETE FROM ops_chat_sessions")
		for _, table := range []string{"ops_chat_sessions", "ops_chat_runs", "ops_chat_calls"} {
			count(table, 0)
		}
	})
}
