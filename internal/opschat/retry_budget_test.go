package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

func TestRetryPartialFailureOnlySpecifiedTargetsAndHistoricalCall(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	f := executor(a, b)
	failed := true
	f.run = func(_ context.Context, snap target.ConnectionSnapshot, _ []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if snap.Server.ID == b && failed {
			return &target.LimitedResult{ExitCode: 1}, nil
		}
		return &target.LimitedResult{Stdout: "ok\n"}, nil
	}
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, a, b)
	old := submitTool(t, s, c, "host_ports", "{}")
	snap := waitRun(t, s, c.ID, old.ID, PartialFailed)
	waitJobs(t, s)
	var failure, success Call
	for _, v := range snap.Calls {
		if v.Status == Failed {
			failure = v
		} else {
			success = v
		}
	}
	if _, e := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{success.ID}}); e != ErrConflict {
		t.Fatal(e)
	}
	failed = false
	in := RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{failure.ID}}
	retry, e := s.RetryFailed(context.Background(), c.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := s.RetryFailed(context.Background(), c.ID, in)
	if e != nil || duplicate.ID != retry.ID {
		t.Fatal(duplicate, e)
	}
	result := waitRun(t, s, c.ID, retry.ID, Succeeded)
	waitJobs(t, s)
	n := 0
	for _, call := range result.Calls {
		if call.RunID == retry.ID {
			n++
			if call.ServerID != b || call.ID == failure.ID {
				t.Fatal(call)
			}
		}
	}
	if n != 1 {
		t.Fatal(n)
	}
	f.mu.Lock()
	if len(f.invocations) != 3 || f.invocations[2].server != b {
		t.Fatal(f.invocations)
	}
	f.mu.Unlock()
	var allowed int
	var kind string
	if e = db.QueryRow("SELECT kind,context_allowed FROM ops_chat_entries WHERE run_id=? AND visible=1 ORDER BY seq LIMIT 1", retry.ID).Scan(&kind, &allowed); e != nil || kind != "tool_request" || allowed != 0 {
		t.Fatal(kind, allowed, e)
	}
	// Place the old result outside Snapshot's latest-run window.
	body, _ := s.seal(runBody{})
	for i := 0; i < 101; i++ {
		if _, e = db.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", uuid.NewString(), c.ID, uuid.NewString(), "h", Failed, body, time.Now().Add(time.Duration(i+1)*time.Second).UnixMilli()); e != nil {
			t.Fatal(e)
		}
	}
	other := chat(t, s)
	call, e := s.Call(context.Background(), c.ID, failure.ID)
	if e != nil || call.ID != failure.ID || call.Status != Failed {
		t.Fatal(call, e)
	}
	if _, e = s.Call(context.Background(), other.ID, failure.ID); e != ErrNotFound {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.invocations) != 3 {
		t.Fatal("Call triggered SSH")
	}
}
func TestRetryRejectsUnknownDispatchInterruptedAndChangedTargets(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, _ := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "host_ports", "{}")
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	cid := snap.Calls[0].ID
	update := func(status string, dispatched bool) {
		t.Helper()
		if e := s.transaction(context.Background(), true, func(tx *sql.Tx) error {
			rec, e := s.call(context.Background(), tx, c.ID, cid)
			if e != nil {
				return e
			}
			rec.Body.View.Status = status
			rec.Body.Dispatched = dispatched
			return s.saveCall(context.Background(), tx, rec)
		}); e != nil {
			t.Fatal(e)
		}
	}
	retry := func() error {
		_, e := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{cid}})
		return e
	}
	update(Unknown, true)
	if e := retry(); e != ErrVerifyFirst {
		t.Fatal(e)
	}
	update(Interrupted, true)
	if e := retry(); e != ErrVerifyFirst {
		t.Fatal(e)
	}
	update(Failed, false)
	f.mu.Lock()
	old := f.snapshots[id]
	changed := old
	changed.CredentialVersion = "changed"
	f.snapshots[id] = changed
	f.mu.Unlock()
	if e := retry(); e != ErrConflict {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.snapshots[id] = old
	f.mu.Unlock()
	update(Interrupted, false)
	run, e := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{cid}})
	if e != nil {
		t.Fatal(e)
	}
	waitRun(t, s, c.ID, run.ID, Succeeded)
	waitJobs(t, s)
	ids := []string{}
	updated, e := s.Patch(context.Background(), c.ID, PatchInput{Revision: c.Revision, ServerIDs: &ids})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: updated.Revision, CallIDs: []string{cid}}); e != ErrConflict {
		t.Fatal(e)
	}
}
func TestMutationReadBudgetPersistsAcrossRenewalAndConfirmation(t *testing.T) {
	for _, exhaust := range []bool{false, true} {
		t.Run(fmt.Sprint(exhaust), func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			// Whitespace is valid around the immutable ID and consumes 998 lines.
			resolution := strings.Repeat("a", 64) + "\n" + strings.Repeat("\n", 997)
			f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
				if argv[1] == "inspect" && argv[4] == "--format" && argv[5] == "{{.Id}}" {
					return &target.LimitedResult{Stdout: resolution}, nil
				}
				if argv[1] == "restart" {
					if l.Bytes != 64<<10-len(resolution) || l.Lines != 2 {
						t.Error("approval recharged read budget", l)
					}
					if exhaust {
						return &target.LimitedResult{Stdout: "ok\nanother\n"}, nil
					}
					return &target.LimitedResult{Stdout: "ok\n"}, nil
				}
				if l.Bytes != 64<<10-len(resolution)-3 || l.Lines != 1 {
					t.Error("verification budget not shared", l)
				}
				return &target.LimitedResult{Stdout: `{"Running":true,"Paused":false}`}, nil
			}
			s, _, _ := fixture(t, f, nil)
			c := chat(t, s, id)
			r := submitTool(t, s, c, "docker_action", `{"container":"app","action":"restart"}`)
			snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
			waitJobs(t, s)
			cid := snap.Calls[0].ID
			before, e := s.call(context.Background(), s.db, c.ID, cid)
			if e != nil {
				t.Fatal(e)
			}
			if before.Body.ReadBytes != len(resolution) || before.Body.ReadLines != 998 {
				t.Fatal(before.Body)
			}
			renewed, e := s.ReissueConfirmation(context.Background(), c.ID, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			after, e := s.call(context.Background(), s.db, c.ID, cid)
			if e != nil || after.Body.ReadBytes != before.Body.ReadBytes || after.Body.ReadLines != before.Body.ReadLines {
				t.Fatal(after, e)
			}
			if _, e = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: renewed.Nonce, Calls: renewed.Calls}); e != nil {
				t.Fatal(e)
			}
			status := Succeeded
			if exhaust {
				status = Unknown
			}
			done := waitRun(t, s, c.ID, r.ID, status)
			waitJobs(t, s)
			rec, e := s.call(context.Background(), s.db, c.ID, cid)
			if e != nil || rec.Body.ReadBytes > 64<<10 || rec.Body.ReadLines != 1000 {
				t.Fatal(rec, e)
			}
			if exhaust && !done.Calls[0].Truncated {
				t.Fatal("exhausted verification did not flag truncated")
			}
			f.mu.Lock()
			n := len(f.invocations)
			f.mu.Unlock()
			expected := 3
			if exhaust {
				expected = 2
			}
			if n != expected {
				t.Fatal(n)
			}
		})
	}
}
func TestLeaseLossBlocksSubmissionsAndNextDispatch(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	s, _, db := fixture(t, f, nil)
	c := chat(t, s, id)
	count := 0
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		count++
		if _, e := db.Exec("UPDATE ops_chat_state SET lease_owner=?,lease_until=? WHERE id=1", uuid.NewString(), time.Now().Add(time.Minute).UnixMilli()); e != nil {
			t.Error(e)
		}
		return &target.LimitedResult{Stdout: validDF}, nil
	}
	r := submitTool(t, s, c, "host_resources", "{}")
	waitJobs(t, s)
	if count != 1 {
		t.Fatal("dispatched after lease loss", count)
	}
	if _, e := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "host_ports", Args: json.RawMessage("{}")}); e != ErrLease {
		t.Fatal(e)
	}
	got, e := s.Call(context.Background(), c.ID, func() string {
		calls, e := s.calls(context.Background(), db, c.ID, r.ID)
		if e != nil || len(calls) != 1 {
			t.Fatal(calls, e)
		}
		return calls[0].Body.View.ID
	}())
	if e != nil || got.Status != Running {
		t.Fatal(got, e)
	}
	// The new consumer will recover the retained dispatch intent as unknown, never replay it.
}

func TestUndispatchedMutationRetryRequiresFreshConfirmation(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	writes := 0
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "show" {
			return &target.LimitedResult{Stdout: "active\n"}, nil
		}
		writes++
		return &target.LimitedResult{}, nil
	}
	s, _, _ := fixture(t, f, nil)
	recorder := s.audit
	s.audit = nil
	c := chat(t, s, id)
	r := submitTool(t, s, c, "systemd_action", `{"unit":"app.service","action":"restart"}`)
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	first := snap.Confirmations[0]
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: first.Nonce, Calls: first.Calls}); err != nil {
		t.Fatal(err)
	}
	snap = waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	oldCall := snap.Calls[0]
	s.audit = recorder
	retry, err := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{oldCall.ID}})
	if err != nil {
		t.Fatal(err)
	}
	snap = waitRun(t, s, c.ID, retry.ID, AwaitingConfirmation)
	waitJobs(t, s)
	var next Confirmation
	for _, a := range snap.Confirmations {
		if a.RunID == retry.ID {
			next = a
		}
	}
	if writes != 0 || next.Nonce == first.Nonce || len(next.Calls) != 1 || next.Calls[0].CallID == oldCall.ID {
		t.Fatal(writes, first, next)
	}
	if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: retry.ID, Nonce: first.Nonce, Calls: first.Calls}); err != ErrApproval {
		t.Fatal(err)
	}
	if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: retry.ID, Nonce: next.Nonce, Calls: next.Calls}); err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, retry.ID, Succeeded)
	waitJobs(t, s)
	if writes != 1 {
		t.Fatal(writes)
	}
}
