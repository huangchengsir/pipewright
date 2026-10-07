package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

func fillStorage(t *testing.T, s *Service, db *sql.DB, id string, targetBytes int64) {
	t.Helper()
	current, e := s.StorageBytes(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	empty, e := s.seal(struct {
		Text   string
		Status string
	}{})
	if e != nil {
		t.Fatal(e)
	}
	count := int(targetBytes-current) - len(empty)
	if count < 0 {
		t.Fatal("invalid test storage target")
	}
	blob, e := s.seal(struct {
		Text   string
		Status string
	}{Text: strings.Repeat("f", count)})
	if e != nil {
		t.Fatal(e)
	}
	sr, e := s.Get(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO ops_chat_entries(session_id,seq,kind,body,body_size,created_at) VALUES(?,?,?,?,?,?)", id, sr.Watermark+1, "historical", blob, count, time.Now().UnixMilli()); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE ops_chat_sessions SET seq=seq+1 WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	actual, e := s.StorageBytes(context.Background(), id)
	if e != nil || actual != targetBytes {
		t.Fatal(actual, targetBytes, e)
	}
}
func TestActualCipherStorageLedgerDraftQuotaRollbackAndSnapshots(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	draft := strings.Repeat("\x01", 8192)
	updated, e := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &draft})
	if e != nil {
		t.Fatal(e)
	}
	total, e := s.StorageBytes(context.Background(), c.ID)
	if e != nil || total < 6*8192 {
		t.Fatal(total, e)
	}
	r := submitTool(t, s, updated, "docker_action", `{"container":"app","action":"restart"}`)
	waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	waitJobs(t, s)
	// Each real encrypted body participates: sessions, entries, runs, calls, approvals.
	sum := int64(0)
	for _, table := range []string{"ops_chat_sessions", "ops_chat_entries", "ops_chat_runs", "ops_chat_calls", "ops_chat_approvals"} {
		key := "session_id"
		if table == "ops_chat_sessions" {
			key = "id"
		}
		rows, e := db.Query("SELECT body FROM "+table+" WHERE "+key+"=?", c.ID)
		if e != nil {
			t.Fatal(e)
		}
		n := 0
		for rows.Next() {
			var b []byte
			if rows.Scan(&b) != nil {
				t.Fatal("scan")
			}
			sum += int64(len(b))
			n++
		}
		e = rows.Err()
		rows.Close()
		if e != nil || n == 0 {
			t.Fatal(table, n, e)
		}
	}
	total, e = s.StorageBytes(context.Background(), c.ID)
	if e != nil || total != sum {
		t.Fatal(total, sum, e)
	}
	if _, e = s.Cancel(context.Background(), c.ID, r.ID); e != nil {
		t.Fatal(e)
	}
	shrink := "old"
	if _, e = s.Patch(context.Background(), c.ID, PatchInput{Revision: updated.Revision, Draft: &shrink}); e != nil {
		t.Fatal(e)
	}
	// A read-only operation cannot silently repair/delete oversized history.
	fillStorage(t, s, db, c.ID, (8<<20)-100)
	prior, e := s.Get(context.Background(), c.ID)
	if e != nil {
		t.Fatal(e)
	}
	grow := strings.Repeat("\x02", 8192)
	title := "changed title"
	if _, e = s.Patch(context.Background(), c.ID, PatchInput{Revision: prior.Revision, Draft: &grow, Title: &title}); e != ErrQuota {
		t.Fatal(e)
	}
	after, e := s.Get(context.Background(), c.ID)
	if e != nil || after.Revision != prior.Revision || after.Draft != prior.Draft || after.Title != prior.Title {
		t.Fatal(after, e)
	}
	// Frozen target snapshots also consume storage and are bounded before dispatch.
	f.mu.Lock()
	large := f.snapshots[id]
	large.Server.Name = strings.Repeat("n", 9<<10)
	f.snapshots[id] = large
	f.mu.Unlock()
	if _, e = s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: prior.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")}); e != ErrQuota {
		t.Fatal(e)
	}
}
func TestFourMiBReserveCannotBeSpentByDraftAndEscapedOutputFits(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f.run = func(ctx context.Context, _ target.ConnectionSnapshot, _ []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return &target.LimitedResult{Stdout: strings.Repeat("\x01", l.Bytes)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	fillStorage(t, s, db, c.ID, (4<<20)-1024)
	r := submitTool(t, s, c, "host_ports", "{}")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not dispatched")
	}
	// State body growth from even a valid 8KiB draft would steal reserved terminal space.
	draft := strings.Repeat("d", 8192)
	if _, e := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &draft}); e != ErrQuota {
		t.Fatal(e)
	}
	close(release)
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	if len(snap.Calls[0].Output) != 64<<10 {
		t.Fatal(len(snap.Calls[0].Output))
	}
	used, e := s.StorageBytes(context.Background(), c.ID)
	if e != nil || used > 8<<20 || used < 4<<20 {
		t.Fatal(used, e)
	}
	current, e := s.Get(context.Background(), c.ID)
	if e != nil || current.Draft != "" || current.Revision != c.Revision {
		t.Fatal(current, e)
	}
}
func TestStorageAdmissionReservesBeforeAnySSH(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	fillStorage(t, s, db, c.ID, (4<<20)+1)
	if _, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")}); e != ErrQuota {
		t.Fatal(e)
	}
	var n int
	if e := db.QueryRow("SELECT COUNT(*) FROM ops_chat_runs WHERE session_id=?", c.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.invocations) != 0 {
		t.Fatal("quota was enforced after dispatch")
	}
}
func TestTargetCallMessageDraftAndSessionBoundaries(t *testing.T) {
	ids := []string{}
	for i := 0; i < 9; i++ {
		ids = append(ids, uuid.NewString())
	}
	f := executor(ids...)
	m := model()
	s, _, _ := fixture(t, f, m)
	if _, e := s.Create(context.Background(), CreateInput{ServerIDs: ids}); e != ErrInvalid {
		t.Fatal(e)
	}
	c := chat(t, s, ids[:8]...)
	tooLarge := strings.Repeat("d", 8193)
	if _, e := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &tooLarge}); e != ErrInvalid {
		t.Fatal(e)
	}
	draft := strings.Repeat("d", 8192)
	saved, e := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, Draft: &draft})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: saved.Revision, Text: strings.Repeat("m", 8193)}); e != ErrInvalid {
		t.Fatal(e)
	}
	run, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: saved.Revision, Text: strings.Repeat("m", 8192)})
	if e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, run.ID, Succeeded)
	waitJobs(t, s)
	indexes := []int{0, 1, 2, 3, 4, 5, 6, 7}
	actions := []Action{}
	for i := 0; i < 3; i++ {
		actions = append(actions, Action{ToolID: "host_ports", Args: json.RawMessage("{}"), TargetIndexes: indexes})
	}
	m.mu.Lock()
	m.plan = Plan{Actions: actions}
	m.mu.Unlock()
	run, e = s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: saved.Revision, Text: "24 calls"})
	if e != nil {
		t.Fatal(e)
	}
	result := waitRun(t, s, c.ID, run.ID, Succeeded)
	waitJobs(t, s)
	n := 0
	for _, call := range result.Calls {
		if call.RunID == run.ID {
			n++
		}
	}
	if n != 24 {
		t.Fatal(n)
	}
	f.mu.Lock()
	before := len(f.invocations)
	f.mu.Unlock()
	m.mu.Lock()
	m.plan = Plan{Actions: append(actions, Action{ToolID: "host_ports", Args: json.RawMessage("{}"), TargetIndexes: []int{0}})}
	m.mu.Unlock()
	run, e = s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: saved.Revision, Text: "25 calls"})
	if e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, run.ID, Failed)
	waitJobs(t, s)
	f.mu.Lock()
	after := len(f.invocations)
	f.mu.Unlock()
	if after != before {
		t.Fatal("over-limit plan partially executed")
	}
	many := make([]string, 25)
	for i := range many {
		many[i] = uuid.NewString()
	}
	if _, e = s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: saved.Revision, CallIDs: many}); e != ErrInvalid {
		t.Fatal(e)
	}
	// The instance already has one session.
	for i := 0; i < 99; i++ {
		chat(t, s)
	}
	if _, e = s.Create(context.Background(), CreateInput{}); e != ErrQuota {
		t.Fatal(e)
	}
}
func TestOldPlaintextCounterIsNotStorageAuthority(t *testing.T) {
	id := uuid.NewString()
	s, _, db := fixture(t, executor(id), nil)
	c := chat(t, s, id)
	if _, e := db.Exec("UPDATE ops_chat_sessions SET body_bytes=8000000 WHERE id=?", c.ID); e != nil {
		t.Fatal(e)
	}
	r := submitTool(t, s, c, "host_ports", "{}")
	waitRun(t, s, c.ID, r.ID, Succeeded)
	actual, e := s.StorageBytes(context.Background(), c.ID)
	if e != nil || actual > 1<<20 {
		t.Fatal(actual, e)
	}
}
