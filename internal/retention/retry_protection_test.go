package retention

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func TestPrunePreservesPinsUnknownResourcesAndActualCounts(t *testing.T) {
	db := storetest.OpenDB(t)
	s := NewService(db)
	project := seedProject(t, db)
	now := time.Now().UTC()
	parent := insertRun(t, db, project, "success", now.Add(-72*time.Hour))
	child := insertRun(t, db, project, "success", now.Add(-48*time.Hour))
	mustExec(t, db, "INSERT INTO run_retry_refs(owner_attempt_id,producer_attempt_id,producer_node_key,object_key,purpose,created_at) VALUES(?,?,?,'node','recovery',0)", child, parent, "node")
	mustExec(t, db, "INSERT INTO run_logs(id,run_id,seq,ts,stream,step_ordinal,text) VALUES('log',?,1,'old','stdout',0,'heavy')", parent)
	if err := s.SetConfig(context.Background(), Config{Enabled: true, KeepPerProject: 1}); err != nil {
		t.Fatal(err)
	}
	n, e := s.Prune(context.Background(), now)
	if e != nil || n != 0 || !runExists(t, db, parent) {
		t.Fatal(n, e, "pinned parent removed")
	}
	if err := s.SetConfig(context.Background(), Config{Enabled: true, MaxAgeDays: 1}); err != nil {
		t.Fatal(err)
	}
	n, e = s.Prune(context.Background(), now)
	if e != nil || n != 2 || runExists(t, db, parent) || runExists(t, db, child) {
		t.Fatal(n, e, "consumer then producer collection failed")
	}
	unknown := insertRun(t, db, project, "failed", now.Add(-72*time.Hour))
	mustExec(t, db, "INSERT INTO run_retry_resources(id,root_id,attempt_id,node_key,state,ciphertext,updated_at) VALUES('unknown',?,?,?,'outcome_unknown',X'00',0)", unknown, unknown, "node")
	n, e = s.Prune(context.Background(), now)
	if e != nil || n != 0 || !runExists(t, db, unknown) {
		t.Fatal(n, e, "unknown resource was expired into proof")
	}
}
