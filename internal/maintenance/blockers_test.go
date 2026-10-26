package maintenance

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestFenceFindsPersistedBusinessAndUnknownEvidence(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	statements := []string{
		"INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','test','git_token','',X'00','test','old','old')",
		"INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','test','https://example.invalid/r','main','c','old','old')",
		"INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('r','p','waiting_approval','old')",
		"INSERT INTO project_activity(activity_id,project_id,owner_epoch,kind,phase,created_at,heartbeat_at) VALUES('a','p','unknown-old-owner','deploy','cleanup_unknown',0,0)",
		"INSERT INTO mcp_config_operations(id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,state,before_revision,result_json,created_at,updated_at) VALUES('o','t','p','req','digest','d','approval','unknown','unknown',1,'{}',0,0)",
		"INSERT INTO release_guards(id,project_id,run_id,attempt_id,plan_id,idempotency_key,status,created_at,evidence_expires_at,report_expires_at,record_json) VALUES('g','p','r','a','plan','key','observing',0,0,0,'{}')",
		"INSERT INTO ops_chat_sessions(id,revision,body,created_at,updated_at) VALUES('s',1,X'00',0,0)",
		"INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES('op','s','request','digest','analyzing',X'00',0)",
	}
	for _, sql := range statements {
		if _, e := st.DB.Exec(sql); e != nil {
			t.Fatal(e)
		}
	}
	e := Fence(ctx, st.DB, "upgrade")
	var blocked *BlockedError
	if !errors.As(e, &blocked) {
		t.Fatal(e)
	}
	want := map[string]bool{"runs": false, "activity_holds": false, "config_operations": false, "guards": false, "ops_runs": false}
	for _, b := range blocked.Blockers {
		if _, ok := want[b.Kind]; !ok || b.Count != 1 {
			t.Fatal(blocked)
		}
		want[b.Kind] = true
	}
	for kind, seen := range want {
		if !seen {
			t.Fatal("missing blocker", kind, blocked)
		}
	}
	// Merely old evidence is never cleared by the updater.
	var holds int
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM project_activity WHERE released_at IS NULL").Scan(&holds); e != nil || holds != 1 {
		t.Fatal(holds, e)
	}
	for _, sql := range []string{"UPDATE pipeline_runs SET status='success'", "UPDATE project_activity SET released_at=1", "UPDATE mcp_config_operations SET state='failed'", "UPDATE release_guards SET status='completed'", "UPDATE ops_chat_runs SET status='succeeded'"} {
		if _, e = st.DB.Exec(sql); e != nil {
			t.Fatal(e)
		}
	}
	if e = Fence(ctx, st.DB, "upgrade"); e != nil {
		t.Fatal(e)
	}
}
