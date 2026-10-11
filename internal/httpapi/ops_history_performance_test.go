package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/opschat"
	"github.com/huangchengsir/pipewright/internal/vault"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSQLiteHistoryHTTPSizeLatencyAndSend(t *testing.T) {
	s, db, e, a, m := opsFixture(t)
	c, err := s.Create(context.Background(), opschat.CreateInput{ServerIDs: []string{e.id}})
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(db, &[32]byte{1})
	seal := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		cipher, err := v.SealSecret(raw)
		if err != nil {
			t.Fatal(err)
		}
		return cipher
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	seq := int64(0)
	if err = tx.QueryRow("SELECT seq FROM ops_chat_sessions WHERE id=?", c.ID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 16; round++ {
		rid := uuid.NewString()
		if _, err = tx.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", rid, c.ID, uuid.NewString(), "hash", opschat.Succeeded, seal(map[string]any{}), time.Now().UnixMilli()+int64(round)); err != nil {
			t.Fatal(err)
		}
		seq++
		if _, err = tx.Exec("INSERT INTO ops_chat_entries(session_id,seq,kind,run_id,visible,body_size,body,created_at) VALUES(?,?,?,?,?,?,?,?)", c.ID, seq, "user", rid, 1, 7, seal(map[string]string{"Text": "history"}), time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 24; i++ {
			cid := uuid.NewString()
			call := opschat.Call{ID: cid, SessionID: c.ID, RunID: rid, ServerID: e.id, ToolID: "host_ports", Status: opschat.Succeeded, Output: strings.Repeat("x", 8<<10)}
			if _, err = tx.Exec("INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)", cid, c.ID, rid, e.id, "host_ports", opschat.Succeeded, "hash", "hash", seal(map[string]any{"View": call}), time.Now().UnixMilli(), i); err != nil {
				t.Fatal(err)
			}
			seq++
			if _, err = tx.Exec("INSERT INTO ops_chat_entries(session_id,seq,kind,run_id,call_id,visible,body_size,body,created_at) VALUES(?,?,?,?,?,?,?,?,?)", c.ID, seq, "tool_result", rid, cid, 1, 0, seal(map[string]any{}), time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = tx.Exec("UPDATE ops_chat_sessions SET seq=?,visible_count=? WHERE id=?", seq, seq, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h := New(nil, a, WithOpsChat(s))
	times := []time.Duration{}
	size := 0
	for i := 0; i < 10; i++ {
		start := time.Now()
		w := opsHTTP(t, h, "GET", "/api/ai/ops/sessions/"+c.ID, "", 200)
		times = append(times, time.Since(start))
		size = w.Body.Len()
		var snapshot opschat.Snapshot
		if json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || len(snapshot.Calls) != 48 {
			t.Fatal("snapshot page", size)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	start := time.Now()
	w := opsHTTP(t, h, "POST", "/api/ai/ops/sessions/"+c.ID+"/turns", fmt.Sprintf(`{"clientRequestId":%q,"revision":%d,"toolId":"host_ports","args":{}}`, uuid.NewString(), c.Revision), 202)
	sent := time.Since(start)
	if size > 512<<10 || times[9] > time.Second || sent > time.Second {
		t.Fatal(size, times, sent)
	}
	var run opschat.Run
	if json.Unmarshal(w.Body.Bytes(), &run) != nil {
		t.Fatal(w.Body.String())
	}
	opsWait(t, s, c.ID, run.ID, opschat.Succeeded)
	if m.calls.Load() != 0 || e.calls.Load() != 1 {
		t.Fatal("history reran", m.calls.Load(), e.calls.Load())
	}
	t.Logf("real SQLite HTTP 384x8KiB encrypted calls; snapshot bytes=%d p50=%s p95/max=%s; submit response bytes=%d elapsed=%s; one executor, zero model", size, times[4], times[9], w.Body.Len(), sent)
}
