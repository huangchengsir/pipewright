package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSQLiteLargeHistorySnapshotBoundedAndPaged(t *testing.T) {
	s, _, db := fixture(t, executor(), nil)
	c := chat(t, s)
	ctx := context.Background()
	// A real migrated file-backed SQLite DB, encrypted bodies, within storage limits.
	err := s.transaction(ctx, false, func(tx *sql.Tx) error {
		for round := 0; round < 60; round++ {
			rid := uuid.NewString()
			rb, e := s.seal(runBody{})
			if e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", rid, c.ID, uuid.NewString(), "hash", Succeeded, rb, time.Now().UnixMilli()+int64(round)); e != nil {
				return e
			}
			if e = s.append(ctx, tx, Entry{SessionID: c.ID, RunID: rid, Kind: "user", Text: "check current server"}, true, true); e != nil {
				return e
			}
			for i := 0; i < 24; i++ {
				cid := uuid.NewString()
				cb, e := s.seal(callBody{View: Call{ID: cid, SessionID: c.ID, RunID: rid, ServerID: "history-server", ToolID: "host_ports", Status: Succeeded, Output: strings.Repeat("x", 4<<10)}})
				if e != nil {
					return e
				}
				if _, e = tx.Exec("INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)", cid, c.ID, rid, "history-server", "host_ports", Succeeded, "hash", "hash", cb, time.Now().UnixMilli(), i); e != nil {
					return e
				}
				if e = s.append(ctx, tx, Entry{SessionID: c.ID, RunID: rid, CallID: cid, Kind: "tool_result"}, true, false); e != nil {
					return e
				}
			}
			if e = s.append(ctx, tx, Entry{SessionID: c.ID, RunID: rid, Kind: "assistant", Text: "completed"}, true, true); e != nil {
				return e
			}
		}
		return s.enforceStorage(ctx, tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM ops_chat_calls WHERE session_id=?", c.ID).Scan(&count)
	times := []time.Duration{}
	size := 0
	var latest *Snapshot
	for i := 0; i < 10; i++ {
		start := time.Now()
		latest, err = s.Snapshot(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, e := json.Marshal(latest)
		if e != nil {
			t.Fatal(e)
		}
		times = append(times, time.Since(start))
		size = len(raw)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	if len(latest.Calls) > 72 || size > 512<<10 || times[9] > time.Second {
		t.Fatalf("unbounded/slow snapshot calls=%d bytes=%d max=%s", len(latest.Calls), size, times[9])
	}
	older, err := s.Entries(ctx, c.ID, latest.Entries.Cursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range older.Entries {
		if entry.Call != nil && len(entry.Call.Output) == 4<<10 {
			found = true
		}
	}
	if !found {
		t.Fatal("pagination lost historical call bodies")
	}
	t.Logf("SQLite encrypted history calls=%d; snapshot calls=%d bytes=%d; 10 samples p50=%s p95/max=%s; older-page call bodies preserved", count, len(latest.Calls), size, times[4], times[9])
}

func TestStreamRevisionStorageLimitRollsBack(t *testing.T) {
	s, _, db := fixture(t, executor(), nil)
	c := chat(t, s)
	rid := uuid.NewString()
	rb, err := s.seal(runBody{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", rid, c.ID, uuid.NewString(), "hash", Planning, rb, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	sink := s.reply(context.Background(), c.ID, rid, "assistant", 16<<10)
	if err = sink.chunk(StreamChunk{Text: "partial"}); err != nil {
		t.Fatal(err)
	}
	fillStorage(t, s, db, c.ID, (8<<20)-100)
	before, _ := s.Get(context.Background(), c.ID)
	if err = sink.finish(strings.Repeat("x", 1024), Succeeded, true); err != ErrQuota {
		t.Fatal("expected revision quota", err)
	}
	after, _ := s.Get(context.Background(), c.ID)
	if before.Watermark != after.Watermark {
		t.Fatal("failed revision appended an event")
	}
	var body []byte
	var allowed int
	db.QueryRow("SELECT body,context_allowed FROM ops_chat_entries WHERE session_id=? AND seq=?", c.ID, sink.seq).Scan(&body, &allowed)
	var value struct{ Text, Status string }
	if err = s.open(body, &value); err != nil || value.Text != "partial" || value.Status != "partial" || allowed != 0 {
		t.Fatal(value, allowed, err)
	}
	// Remove only the synthetic quota fixture so cleanup can persist interruption.
	db.Exec("DELETE FROM ops_chat_entries WHERE session_id=? AND kind='historical'", c.ID)
}
