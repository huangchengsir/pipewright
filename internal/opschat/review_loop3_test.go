package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestMixedPlanConfirmationBarrierPreservesReadsAndCompleteBatch(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			id := uuid.NewString()
			f, m := executor(id), model()
			m.plan = Plan{Actions: []Action{
				{ToolID: "host_ports", TargetIndexes: []int{0}},
				{ToolID: "docker_action", Args: json.RawMessage(`{"container":"app","action":"restart"}`), TargetIndexes: []int{0}},
				{ToolID: "docker_logs", Args: json.RawMessage(`{"container":"app"}`), TargetIndexes: []int{0}},
				{ToolID: "docker_action", Args: json.RawMessage(`{"container":"app","action":"restart"}`), TargetIndexes: []int{0}},
				{ToolID: "docker_logs", Args: json.RawMessage(`{"container":"app"}`), TargetIndexes: []int{0}},
			}}
			var restarts atomic.Int32
			resolution := strings.Repeat("a", 64) + "\n"
			f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, limits target.ExecutionLimits) (*target.LimitedResult, error) {
				switch argv[1] {
				case "inspect":
					if argv[5] == "{{.Id}}" {
						return &target.LimitedResult{Stdout: resolution}, nil
					}
					state := `{"Running":true,"Paused":false}`
					if unknown {
						state = `{"Running":false,"Paused":false}`
					}
					return &target.LimitedResult{Stdout: state}, nil
				case "restart":
					if limits.Bytes != (64<<10)-len(resolution) || limits.Lines != 999 {
						t.Error("preparation budget was recharged", limits)
					}
					restarts.Add(1)
					return &target.LimitedResult{Stdout: "restarted\n"}, nil
				case "logs":
					return &target.LimitedResult{Stdout: fmt.Sprintf("after restart %d", restarts.Load())}, nil
				default:
					return &target.LimitedResult{Stdout: "ports before mutation"}, nil
				}
			}
			s, _, _ := fixture(t, f, m)
			c := chat(t, s, id)
			r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "restart and inspect logs twice"})
			if err != nil {
				t.Fatal(err)
			}
			pending := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
			waitJobs(t, s)
			cs, err := s.calls(context.Background(), s.db, c.ID, r.ID)
			if err != nil || len(cs) != 5 || cs[0].Body.View.Status != Succeeded || cs[2].Body.View.Status != Queued || cs[4].Body.View.Status != Queued {
				t.Fatal("mixed plan was not suspended in order", cs, err)
			}
			f.mu.Lock()
			before := append([]invocation(nil), f.invocations...)
			f.mu.Unlock()
			if len(before) != 3 || restarts.Load() != 0 {
				t.Fatal("diagnostics crossed the barrier", before)
			}
			a := pending.Confirmations[0]
			if len(a.Calls) != 2 || a.Calls[0] != bound(cs[1].Body.View) || a.Calls[1] != bound(cs[3].Body.View) {
				t.Fatal("confirmation omitted/rebound a mutation", a)
			}
			if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls[:1]}); err != ErrApproval {
				t.Fatal("partial batch accepted", err)
			}
			initial, err := s.run(context.Background(), s.db, c.ID, r.ID)
			if err != nil || initial.Body.ReadBytes != len("ports before mutation")+2*len(resolution) {
				t.Fatal("preparation read budget lost", initial, err)
			}
			if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
				t.Fatal(err)
			}
			status := Succeeded
			if unknown {
				status = Unknown
			}
			waitRun(t, s, c.ID, r.ID, status)
			waitJobs(t, s)
			cs, err = s.calls(context.Background(), s.db, c.ID, r.ID)
			if err != nil || cs[2].Body.View.Output != "after restart 1" || cs[4].Body.View.Output != "after restart 2" || restarts.Load() != 2 {
				t.Fatal("diagnostics did not observe ordered effects", cs, err)
			}
			final, err := s.run(context.Background(), s.db, c.ID, r.ID)
			if err != nil || final.Body.ActiveMillis < initial.Body.ActiveMillis || final.Body.ModelCalls != 1 || final.Body.ReadBytes <= initial.Body.ReadBytes {
				t.Fatal("phase budgets reset", final, err)
			}
			if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != ErrApproval {
				t.Fatal("batch replay accepted", err)
			}
		})
	}
}

func TestMixedPlanPreparationFailureAndCancelConvergeQueuedCalls(t *testing.T) {
	for _, mode := range []string{"failed", "partial", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.NewString()
			f, m := executor(id), model()
			m.plan = Plan{Actions: []Action{
				{ToolID: "docker_action", Args: json.RawMessage(`{"container":"app","action":"restart"}`), TargetIndexes: []int{0}},
				{ToolID: "host_ports", TargetIndexes: []int{0}},
				{ToolID: "docker_action", Args: json.RawMessage(`{"container":"later","action":"restart"}`), TargetIndexes: []int{0}},
			}}
			if mode != "cancel" {
				f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
					if mode == "failed" || (argv[1] == "inspect" && argv[6] == "app") {
						return &target.LimitedResult{ExitCode: 1}, nil
					}
					if argv[1] == "inspect" && argv[5] == "{{.Id}}" {
						return &target.LimitedResult{Stdout: strings.Repeat("a", 64)}, nil
					}
					return &target.LimitedResult{Stdout: `{"Running":true,"Paused":false}`}, nil
				}
			}
			s, _, _ := fixture(t, f, m)
			c := chat(t, s, id)
			r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "mixed"})
			if err != nil {
				t.Fatal(err)
			}
			status := Failed
			if mode == "cancel" {
				waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
				waitJobs(t, s)
				if _, err = s.Cancel(context.Background(), c.ID, r.ID); err != nil {
					t.Fatal(err)
				}
				status = Interrupted
			} else if mode == "partial" {
				pending := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
				waitJobs(t, s)
				if len(pending.Confirmations[0].Calls) != 1 {
					t.Fatal("preparation failure hid the remaining mutation", pending)
				}
				a := pending.Confirmations[0]
				if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
					t.Fatal(err)
				}
				status = PartialFailed
			}
			waitRun(t, s, c.ID, r.ID, status)
			waitJobs(t, s)
			cs, err := s.calls(context.Background(), s.db, c.ID, r.ID)
			if err != nil || len(cs) != 3 {
				t.Fatal(cs, err)
			}
			for _, call := range cs {
				if !terminal(call.Body.View.Status) {
					t.Fatal("unexecuted call stranded", call)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, call := range f.invocations {
				if mode != "partial" && call.argv[1] != "inspect" {
					t.Fatal("unconfirmed diagnostic/action executed", call)
				}
			}
		})
	}
}

func TestHistoricalPublicFieldsRefreshRotatedSecretsWithoutChangingHistory(t *testing.T) {
	for _, endpoint := range []string{"Get", "List", "Snapshot", "Entries", "Events"} {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			f := executor()
			m := &registeringModel{fakeModel: model(), key: "old-provider-key"}
			s, v, db := fixture(t, f, m)
			m.masker, m.db = s.masker, db
			secret, providerSecret := "future-vault-key", "future-provider-key"
			text := "historical " + secret + " " + providerSecret
			cred, err := v.Create(vault.CreateInput{Name: "rotation", Type: vault.TypeSSHPassword, Secret: "old-vault-key"})
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.Create(ctx, CreateInput{Title: text})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Patch(ctx, c.ID, PatchInput{Revision: c.Revision, Draft: &text}); err != nil {
				t.Fatal(err)
			}
			if err = s.transaction(ctx, true, func(tx *sql.Tx) error {
				for _, kind := range []string{"user", "assistant", "analysis"} {
					if err := s.append(ctx, tx, Entry{SessionID: c.ID, Kind: kind, Text: text}, true, false); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ciphertexts := func() []string {
				rows, err := db.QueryContext(ctx, "SELECT body FROM ops_chat_sessions WHERE id=? UNION ALL SELECT body FROM ops_chat_entries WHERE session_id=? ORDER BY body", c.ID, c.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				out := []string{}
				for rows.Next() {
					var body []byte
					if err := rows.Scan(&body); err != nil {
						t.Fatal(err)
					}
					out = append(out, string(body))
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return out
			}
			before := ciphertexts()
			if _, err = v.Update(cred.ID, vault.UpdateInput{Secret: &secret}); err != nil {
				t.Fatal(err)
			}
			m.key = providerSecret
			var out any
			switch endpoint {
			case "Get":
				out, err = s.Get(ctx, c.ID)
			case "List":
				out, err = s.List(ctx, "", 50)
			case "Snapshot":
				out, err = s.Snapshot(ctx, c.ID)
			case "Entries":
				out, err = s.Entries(ctx, c.ID, "", 50)
			case "Events":
				out, err = s.Events(ctx, c.ID, "", 100)
			}
			if err != nil {
				t.Fatal("pure GET/registration deadlocked or failed", err)
			}
			raw, err := json.Marshal(out)
			if err != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), providerSecret) || !strings.Contains(string(raw), "[MASKED]") {
				t.Fatal("historical public projection leaked", string(raw), err)
			}
			if !reflect.DeepEqual(before, ciphertexts()) {
				t.Fatal("GET rewrote encrypted history")
			}
			stored, err := s.session(ctx, db, c.ID)
			if err != nil || stored.Body.Title != text || stored.Body.Draft != text {
				t.Fatal("projection changed internal fields", stored, err)
			}
			if len(f.invocations) != 0 || len(m.plans) != 0 || len(m.analyses) != 0 {
				t.Fatal("GET invoked SSH/model inference")
			}
		})
	}
}

func TestSubscriptionsRejectLostUnstartedClosingAndCloseActive(t *testing.T) {
	s, _, _ := fixture(t, executor(), nil)
	c := chat(t, s)
	wake, unsubscribe, err := s.Subscribe(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.fenceWorker()
	select {
	case _, ok := <-wake:
		if ok {
			t.Fatal("lost executor subscription remained active")
		}
	case <-time.After(time.Second):
		t.Fatal("active subscription did not close on lease loss")
	}
	unsubscribe()
	unsubscribe()
	for i := 0; i < 3; i++ {
		ch, stop, err := s.Subscribe(context.Background(), c.ID)
		if err != ErrLease || ch != nil || stop != nil {
			t.Fatal("lease loss allowed ready/EOF churn", ch, err)
		}
	}
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
	if _, _, err = s.Subscribe(context.Background(), c.ID); err != ErrUnavailable {
		t.Fatal("unstarted service accepted subscription", err)
	}
	s.mu.Lock()
	s.started = true
	s.closing = true
	s.mu.Unlock()
	if _, _, err = s.Subscribe(context.Background(), c.ID); err != ErrUnavailable {
		t.Fatal("closing service accepted subscription", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.wake) != 0 {
		t.Fatal("rejected subscriptions leaked", s.wake)
	}
}
