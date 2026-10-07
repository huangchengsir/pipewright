package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// Force a timestamp collision and reverse lexical UUID order after saving the plan.
func reversePlanUUIDs(t *testing.T, s *Service, id, run string) Confirmation {
	t.Helper()
	err := s.transaction(context.Background(), true, func(tx *sql.Tx) error {
		cs, err := s.calls(context.Background(), tx, id, run)
		if err != nil || len(cs) != 2 {
			t.Fatalf("calls: %v %v", cs, err)
		}
		for i := range cs {
			old := cs[i].Body.View.ID
			cs[i].Body.View.ID = []string{"ffffffff-ffff-4fff-8fff-ffffffffffff", "00000000-0000-4000-8000-000000000001"}[i]
			body, err := s.seal(cs[i].Body)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("UPDATE ops_chat_calls SET id=?,body=?,created_at=1 WHERE id=?", cs[i].Body.View.ID, body, old); err != nil {
				return err
			}
			if _, err = tx.Exec("UPDATE ops_chat_entries SET call_id=? WHERE call_id=?", cs[i].Body.View.ID, old); err != nil {
				return err
			}
		}
		return s.issueApproval(context.Background(), tx, id, run, cs)
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.approval(context.Background(), s.db, id, run)
	if err != nil {
		t.Fatal(err)
	}
	return a.View
}

func TestPlanOrdinalSurvivesSaveConfirmAndReversedRetrySelection(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirm", true: "retry"}[retry], func(t *testing.T) {
			id := uuid.NewString()
			f, m := executor(id), model()
			m.plan = Plan{Actions: []Action{
				{ToolID: "systemd_action", Args: json.RawMessage(`{"unit":"app.service","action":"stop"}`), TargetIndexes: []int{0}},
				{ToolID: "systemd_action", Args: json.RawMessage(`{"unit":"app.service","action":"start"}`), TargetIndexes: []int{0}},
			}}
			var mu sync.Mutex
			state := "active"
			f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
				mu.Lock()
				defer mu.Unlock()
				if argv[1] == "show" {
					return &target.LimitedResult{Stdout: state}, nil
				}
				if argv[1] == "stop" {
					state = "inactive"
				} else {
					state = "active"
				}
				return &target.LimitedResult{}, nil
			}
			s, _, _ := fixture(t, f, m)
			c := chat(t, s, id)
			r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "stop then start"})
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
			waitJobs(t, s)
			a := reversePlanUUIDs(t, s, c.ID, r.ID)
			cs, err := s.calls(context.Background(), s.db, c.ID, r.ID)
			if err != nil || cs[0].Ordinal != 0 || cs[1].Ordinal != 1 || cs[0].Body.View.Args.Action != "stop" || cs[1].Body.View.Args.Action != "start" || cs[0].Created != cs[1].Created || cs[0].Body.View.ID < cs[1].Body.View.ID {
				t.Fatal("saved ordinal did not preserve colliding reverse-UUID plan", cs, err)
			}
			recorder := s.audit
			if retry {
				s.audit = nil // Both fail locally, before dispatch, so an explicit retry is safe.
			}
			if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
				t.Fatal(err)
			}
			if retry {
				snap := waitRun(t, s, c.ID, r.ID, Failed)
				waitJobs(t, s)
				s.audit = recorder
				r, err = s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{snap.Calls[1].ID, snap.Calls[0].ID}})
				if err != nil {
					t.Fatal(err)
				}
				pending := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
				waitJobs(t, s)
				for _, confirmation := range pending.Confirmations {
					if confirmation.RunID == r.ID {
						a = confirmation
					}
				}
				if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
					t.Fatal(err)
				}
			}
			waitRun(t, s, c.ID, r.ID, Succeeded)
			waitJobs(t, s)
			f.mu.Lock()
			defer f.mu.Unlock()
			actions := []string{}
			for _, call := range f.invocations {
				if mutationCommand(call.argv) {
					actions = append(actions, call.argv[1])
				}
			}
			if strings.Join(actions, ",") != "stop,start" || state != "active" {
				t.Fatal(actions, state)
			}
		})
	}
}

func TestMutationSlotIncludesBlockingVerificationAcrossSessions(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	verification, release, competitor := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	f.run = func(ctx context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "show" && argv[2] == "first.service" {
			close(verification)
			select {
			case <-release:
				return &target.LimitedResult{Stdout: "inactive"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if argv[1] == "start" {
			competitor <- struct{}{}
		}
		return &target.LimitedResult{Stdout: "active"}, nil
	}
	s, _, db := fixture(t, f, nil)
	c1, c2 := chat(t, s, id), chat(t, s, id)
	r1 := submitTool(t, s, c1, "systemd_action", `{"unit":"first.service","action":"stop"}`)
	r2 := submitTool(t, s, c2, "systemd_action", `{"unit":"second.service","action":"start"}`)
	a1 := waitRun(t, s, c1.ID, r1.ID, AwaitingConfirmation).Confirmations[0]
	a2 := waitRun(t, s, c2.ID, r2.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	if _, err := s.Confirm(context.Background(), c1.ID, ConfirmInput{RunID: r1.ID, Nonce: a1.Nonce, Calls: a1.Calls}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-verification:
	case <-time.After(time.Second):
		t.Fatal("verification did not start (possible recursive slot deadlock)")
	}
	if _, err := s.Confirm(context.Background(), c2.ID, ConfirmInput{RunID: r2.ID, Nonce: a2.Nonce, Calls: a2.Calls}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		var intents int
		if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ops_chat_intent'").Scan(&intents); err != nil {
			t.Fatal(err)
		}
		if intents == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("competitor did not reach dispatch")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-competitor:
		t.Fatal("competing mutation interleaved with verification")
	case <-time.After(80 * time.Millisecond):
	}
	// Cancel the first verifier: this must release its slot and let the competitor converge.
	if _, err := s.Cancel(context.Background(), c1.ID, r1.ID); err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c1.ID, r1.ID, Unknown)
	waitRun(t, s, c2.ID, r2.ID, Succeeded)
	waitJobs(t, s)
	select {
	case <-competitor:
	default:
		t.Fatal("slot was not released after cancelled verification")
	}
}

func TestNonzeroMutationPartialEffectsRequireVerificationNotBlindRetry(t *testing.T) {
	for _, tool := range []string{"systemd_action", "docker_action"} {
		for _, action := range []string{"restart", "stop", "start"} {
			t.Run(tool+"/"+action, func(t *testing.T) {
				id := uuid.NewString()
				f := executor(id)
				var affected, verified atomic.Bool
				f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
					if argv[len(argv)-2] == "{{.Id}}" {
						return &target.LimitedResult{Stdout: strings.Repeat("a", 64)}, nil
					}
					if mutationCommand(argv) {
						affected.Store(true) // Command changes the target before returning a failure.
						return &target.LimitedResult{ExitCode: 1, Stderr: "partial effect"}, nil
					}
					verified.Store(affected.Load())
					if tool == "docker_action" {
						return &target.LimitedResult{Stdout: `{"Running":true,"Paused":false}`}, nil
					}
					return &target.LimitedResult{Stdout: "active"}, nil
				}
				s, _, _ := fixture(t, f, nil)
				c := chat(t, s, id)
				args := ToolArgs{Action: action}
				if tool == "docker_action" {
					args.Container = "app"
				} else {
					args.Unit = "app.service"
				}
				raw, _ := json.Marshal(args)
				r := submitTool(t, s, c, tool, string(raw))
				a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
				waitJobs(t, s)
				if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
					t.Fatal(err)
				}
				status := Unknown
				if action == "start" {
					status = Succeeded // Read-only state proves this idempotent final effect.
				}
				snap := waitRun(t, s, c.ID, r.ID, status)
				waitJobs(t, s)
				if !affected.Load() || !verified.Load() || snap.Calls[0].ExitCode == nil || *snap.Calls[0].ExitCode != 1 {
					t.Fatal("partial effect or immediate verification missing", snap.Calls)
				}
				_, err := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{snap.Calls[0].ID}})
				if status == Unknown && err != ErrVerifyFirst || status == Succeeded && err != ErrConflict || f.countMutation() != 1 {
					t.Fatal("blind retry accepted", err)
				}
			})
		}
	}
}

func TestNonzeroRestartActuallyStopsTargetBeforeFailure(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	var running atomic.Bool
	var probes atomic.Int32
	running.Store(true)
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		if argv[1] == "restart" {
			running.Store(false) // Stop stage succeeds; subsequent start stage fails.
			return &target.LimitedResult{ExitCode: 1, Stderr: "start stage failed"}, nil
		}
		probes.Add(1)
		state := "inactive"
		if running.Load() {
			state = "active"
		}
		return &target.LimitedResult{Stdout: state}, nil
	}
	s, _, _ := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "systemd_action", `{"unit":"app.service","action":"restart"}`)
	a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
	waitJobs(t, s)
	if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Unknown)
	waitJobs(t, s)
	if running.Load() || probes.Load() != 1 || snap.Calls[0].Status != Unknown {
		t.Fatal("partial restart not represented or probed", snap.Calls)
	}
	if _, err := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{snap.Calls[0].ID}}); err != ErrVerifyFirst {
		t.Fatal("partially effective restart was blindly retryable", err)
	}
}

func TestSecretArgsRejectDecodedFieldsAndMaskHistoricalBindings(t *testing.T) {
	id := uuid.NewString()
	f, m := executor(id), model()
	s, v, db := fixture(t, f, m)
	cred, err := v.Create(vault.CreateInput{Name: "key", Type: vault.TypeSSHPassword, Secret: "secretapp"})
	if err != nil {
		t.Fatal(err)
	}
	c := chat(t, s, id)
	for _, raw := range []string{`{"container":"secretapp"}`, `{"container":"\u0073ecretapp"}`} {
		if _, err = s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, ToolID: "docker_logs", Args: json.RawMessage(raw)}); err != ErrInvalid {
			t.Fatal("decoded secret argument accepted", err)
		}
	}
	m.plan = Plan{Actions: []Action{{ToolID: "systemd_status", Args: json.RawMessage(`{"unit":"secretapp.service"}`), TargetIndexes: []int{0}}}}
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	f.mu.Lock()
	n := len(f.invocations)
	f.mu.Unlock()
	if n != 0 {
		t.Fatal("secret plan reached SSH")
	}
	r = submitTool(t, s, c, "systemd_action", `{"unit":"app.service","action":"start"}`)
	pending := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	waitJobs(t, s)
	var a Confirmation
	for _, confirmation := range pending.Confirmations {
		if confirmation.RunID == r.ID {
			a = confirmation
		}
	}
	secret := "app.service"
	if _, err = v.Update(cred.ID, vault.UpdateInput{Secret: &secret}); err != nil {
		t.Fatal(err)
	}
	view, err := s.Call(context.Background(), c.ID, a.Calls[0].CallID)
	if err != nil || view.Args.Unit != "[MASKED]" || view.Object != "[MASKED]" {
		t.Fatal(view, err)
	}
	snap, err := s.Snapshot(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(snap.Calls)
	bindings, _ := json.Marshal(snap.Confirmations)
	if strings.Contains(string(public)+string(bindings), secret) {
		t.Fatal("rotated secret leaked in historical public args/bindings")
	}
	stored, err := s.call(context.Background(), db, c.ID, view.ID)
	if err != nil || stored.Body.View.Args.Unit != secret || stored.Body.View.ArgsHash != view.ArgsHash || bound(stored.Body.View) != a.Calls[0] {
		t.Fatal("projection altered exact internal binding", stored, err)
	}
	for _, confirmation := range snap.Confirmations {
		if confirmation.RunID == r.ID {
			a = confirmation
		}
	}
	if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
		t.Fatal("public confirmation did not preserve hashed binding", err)
	}
	waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	if f.countMutation() != 0 {
		t.Fatal("rotation into a secret argument reached execution")
	}
}

type unavailableListVault struct {
	vault.Vault
	fail atomic.Bool
}

func (v *unavailableListVault) List() ([]vault.Credential, error) {
	if v.fail.Load() {
		return nil, errors.New("raw sensitive vault error")
	}
	return v.Vault.List()
}

func TestRotationDuringExecRefreshesSecretsBeforeStreamsOrFailsClosed(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "mask", true: "unavailable"}[fail], func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			s, v, db := fixture(t, f, nil)
			wrapped := &unavailableListVault{Vault: v}
			s.vault = wrapped
			cred, err := v.Create(vault.CreateInput{Name: "rotating", Type: vault.TypeSSHPassword, Secret: "before-exec-key"})
			if err != nil {
				t.Fatal(err)
			}
			secret := "during-exec-new-secret"
			f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
				if _, err := v.Update(cred.ID, vault.UpdateInput{Secret: &secret}); err != nil {
					t.Error(err)
				}
				wrapped.fail.Store(fail)
				return &target.LimitedResult{Stdout: secret, Stderr: secret[:len(secret)-2], Truncated: true}, nil
			}
			c := chat(t, s, id)
			r := submitTool(t, s, c, "host_ports", "{}")
			waitJobs(t, s)
			cs, err := s.calls(context.Background(), db, c.ID, r.ID)
			if err != nil || len(cs) != 1 {
				t.Fatal(cs, err)
			}
			call := cs[0].Body.View
			if strings.Contains(call.Output, secret[:len(secret)-2]) || fail && (call.Output != "" || call.Status != Failed) || !fail && (!strings.Contains(call.Output, "[MASKED]") || call.Status != Succeeded) {
				t.Fatal("returned stream persisted before latest secrets", call)
			}
			if fail {
				if _, err := s.Call(context.Background(), c.ID, call.ID); err != ErrUnavailable {
					t.Fatal("public projection did not fail closed", err)
				}
			}
		})
	}
}

type failingReviewModel struct{ *fakeModel }

func (m *failingReviewModel) Plan(context.Context, PlanRequest) (Plan, error) {
	return Plan{}, errors.New("unknown-sensitive-provider-response")
}
func (m *failingReviewModel) Analyze(context.Context, AnalysisRequest) (string, error) {
	return "raw-sensitive-model-text", errors.New("unknown-sensitive-provider-response")
}

func TestPlanningAndAnalysisFailuresPersistVisibleSafeEntry(t *testing.T) {
	for _, analysis := range []bool{false, true} {
		t.Run(map[bool]string{false: "planning", true: "analysis"}[analysis], func(t *testing.T) {
			id := uuid.NewString()
			s, _, db := fixture(t, executor(id), &failingReviewModel{model()})
			c := chat(t, s, id)
			var r *Run
			var err error
			if analysis {
				collected := submitTool(t, s, c, "host_ports", "{}")
				snap := waitRun(t, s, c.ID, collected.ID, Succeeded)
				waitJobs(t, s)
				preview, e := s.AnalysisPreview(context.Background(), c.ID, []string{snap.Calls[0].ID})
				if e != nil {
					t.Fatal(e)
				}
				r, err = s.Analyze(context.Background(), c.ID, AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: preview.CallIDs, PreviewHash: preview.Hash, Provider: preview.Provider, Consent: true})
			} else {
				r, err = s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "inspect"})
			}
			if err != nil {
				t.Fatal(err)
			}
			waitRun(t, s, c.ID, r.ID, Failed)
			waitJobs(t, s)
			page, err := s.Entries(context.Background(), c.ID, "", 100)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, e := range page.Entries {
				if strings.Contains(e.Text, "sensitive") {
					t.Fatal("raw model error exposed", e)
				}
				if e.RunID == r.ID && e.Kind == "assistant" && e.Status == Failed && e.Text != "" {
					found++
					var allowed int
					if err := db.QueryRow("SELECT context_allowed FROM ops_chat_entries WHERE session_id=? AND seq=?", c.ID, e.Seq).Scan(&allowed); err != nil || allowed != 0 {
						t.Fatal("failure entered future model context", allowed, err)
					}
				}
			}
			if found != 1 {
				t.Fatal("missing or duplicate persisted visible failure", page)
			}
		})
	}
}
