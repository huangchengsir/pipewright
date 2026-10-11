package opschat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

type continuationModel struct {
	*fakeModel
	steps         []Plan
	secondStarted chan struct{}
	secondError   error
	blockSecond   bool
}

func (m *continuationModel) Plan(ctx context.Context, in PlanRequest) (Plan, error) {
	m.mu.Lock()
	m.plans = append(m.plans, in)
	n := len(m.plans)
	m.mu.Unlock()
	if n == 2 {
		if m.secondStarted != nil {
			close(m.secondStarted)
		}
		if m.blockSecond {
			<-ctx.Done()
			return Plan{}, ctx.Err()
		}
		if m.secondError != nil {
			return Plan{}, m.secondError
		}
	}
	if n > len(m.steps) {
		return Plan{}, errors.New("unexpected extra planning")
	}
	return m.steps[n-1], nil
}

func discoveryPlan(indexes ...int) Plan {
	return Plan{Text: "Find the container first.", NeedsDiscovery: true, Actions: []Action{{ToolID: "docker_containers", Args: json.RawMessage(`{}`), TargetIndexes: indexes}}}
}

func containerExecPlan() Plan {
	return Plan{Text: "Review the exact container command before execution.", Actions: []Action{{ToolID: "docker_exec", Args: json.RawMessage(`{"container":"redis","argv":["redis-cli","INFO","keyspace"]}`), TargetIndexes: []int{0}}}}
}

func continuationExecutor(t *testing.T, id string) *fakeExecutor {
	t.Helper()
	f := executor(id)
	f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
		switch argv[1] {
		case "ps":
			return &target.LimitedResult{Stdout: `{"id":"` + strings.Repeat("a", 64) + `","names":"redis","image":"redis:latest","state":"running","ports":"6379/tcp"}`}, nil
		case "inspect":
			return &target.LimitedResult{Stdout: strings.Repeat("a", 64) + "\n"}, nil
		case "exec":
			return &target.LimitedResult{Stdout: "db0:keys=3,expires=1,avg_ttl=5000"}, nil
		default:
			t.Errorf("unexpected command %q", argv)
			return nil, ErrInvalid
		}
	}
	return f
}

func TestDiscoveryContinuationExecRequiresExactApprovalAndSummarizesOnce(t *testing.T) {
	id := uuid.NewString()
	f := continuationExecutor(t, id)
	m := &continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(0), containerExecPlan()}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Redis container internal key distribution", AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation)
	if len(snap.Calls) != 2 || len(snap.Confirmations) != 1 || len(execInvocations(f)) != 0 {
		t.Fatal("missing continuation or executed before confirmation", snap.Calls)
	}
	m.mu.Lock()
	if len(m.plans) == 2 && (len(m.plans[1].Messages) != 1 || m.plans[1].Messages[0].Role != "user" || m.plans[1].Messages[0].Text != "Redis container internal key distribution") {
		t.Error("discovery narration displaced the original question", m.plans[1].Messages)
	}
	if len(m.plans) != 2 || !m.plans[1].Continuation || len(m.plans[1].ContainerFacts) != 1 || len(m.plans[1].ContainerFacts[0].Containers) != 1 || m.plans[1].ContainerFacts[0].Containers[0].Name != "redis" || len(m.analyses) != 0 {
		t.Error("discovery facts not used before exec", m.plans)
	}
	m.mu.Unlock()
	approval := snap.Confirmations[0]
	if len(approval.Calls) != 1 || approval.Calls[0].ToolID != "docker_exec" || approval.Calls[0].Object != strings.Repeat("a", 64) {
		t.Fatal(approval)
	}
	if _, err = s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: approval.Nonce, Calls: approval.Calls}); err != nil {
		t.Fatal(err)
	}
	snap = waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	if len(execInvocations(f)) != 1 {
		t.Fatal("exec repeated")
	}
	m.mu.Lock()
	if len(m.plans) != 2 || len(m.analyses) != 1 || len(m.analyses[0].Items) != 2 || m.analyses[0].Question != "Redis container internal key distribution" {
		t.Error("not one complete original-question analysis", m.analyses)
	}
	m.mu.Unlock()
	record, err := s.run(context.Background(), s.db, c.ID, r.ID)
	if err != nil || record.Body.ModelCalls != 3 || !record.Body.ContinuationStarted || record.Body.NeedsDiscovery {
		t.Fatal(record.Body, err)
	}
	for _, call := range snap.Calls {
		if call.Status != Succeeded {
			t.Fatal(call)
		}
	}
}

func TestExistingConversationInventoryFeedsFollowupWithoutNewSession(t *testing.T) {
	id := uuid.NewString()
	f := continuationExecutor(t, id)
	oldStyle := discoveryPlan(0)
	oldStyle.NeedsDiscovery = false
	m := &continuationModel{fakeModel: model(), steps: []Plan{oldStyle, containerExecPlan()}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	first, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Find Redis internal key distribution"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, first.ID, Succeeded)
	waitJobs(t, s)
	c, err = s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	followup, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Confirm execution"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, followup.ID, AwaitingConfirmation)
	waitJobs(t, s)
	if len(execInvocations(f)) != 0 || len(snap.Confirmations) != 1 {
		t.Fatal("chat confirmation granted execution")
	}
	m.mu.Lock()
	request := m.plans[1]
	m.mu.Unlock()
	if request.Continuation || len(request.ContainerFacts) != 1 || len(request.ContainerFacts[0].Containers) != 1 {
		t.Fatal(request)
	}
	if request.ContainerFacts[0].Containers[0].Name != "redis" || request.ContainerFacts[0].TargetAlias != "target_0" {
		t.Fatal(request.ContainerFacts)
	}
	if request.Messages[len(request.Messages)-1].Text != "Confirm execution" {
		t.Fatal("latest followup lost")
	}
}

func TestContinuationFailureIsOneShotAndKeepsDiscoveryResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		next Plan
		err  error
	}{
		{"model-error", Plan{}, errors.New("provider unavailable")},
		{"another-discovery", discoveryPlan(0), nil},
		{"another-discovery-flag-false", Plan{Text: "List again", Actions: discoveryPlan(0).Actions}, nil},
		{"invalid-tool", Plan{Text: "bad", Actions: []Action{{ToolID: "host_shell", Args: json.RawMessage(`{}`), TargetIndexes: []int{0}}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.NewString()
			f := continuationExecutor(t, id)
			m := &continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(0), tc.next}, secondError: tc.err}
			s, _, _ := fixture(t, f, m)
			c := chat(t, s, id)
			in := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Inspect keys", AnalyzeResults: true, Provider: m.Info()}
			r, err := s.Submit(context.Background(), c.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			snap := waitRun(t, s, c.ID, r.ID, Failed)
			waitJobs(t, s)
			if len(snap.Calls) != 1 || snap.Calls[0].Status != Succeeded || len(execInvocations(f)) != 0 {
				t.Fatal("lost discovery or ran invalid command", snap.Calls)
			}
			code := "invalid_plan"
			if tc.err != nil {
				code = "unknown"
			}
			markers, summaries := 0, 0
			for _, entry := range snap.Entries.Entries {
				if entry.Status == Failed && entry.Kind == "assistant" {
					if entry.Text != "[continuation:"+code+"]" {
						t.Fatal("misleading continuation diagnostic", entry)
					}
					markers++
				}
				if entry.Summary != nil {
					summaries++
				}
			}
			if markers != 1 || summaries != 1 || !strings.Contains(snap.Calls[0].Output, "redis") {
				t.Fatal("continuation lost its diagnostic or collected facts", markers, summaries)
			}
			if duplicate, err := s.Submit(context.Background(), c.ID, in); err != nil || duplicate.ID != r.ID {
				t.Fatal(duplicate, err)
			}
			m.mu.Lock()
			plans, analyses := len(m.plans), len(m.analyses)
			m.mu.Unlock()
			if plans != 2 || analyses != 0 {
				t.Fatal("failed continuation retried or analyzed", plans, analyses)
			}
		})
	}
}

func TestDiscoveryContinuationCancelAndProviderChangeDoNotExecute(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		id := uuid.NewString()
		f := continuationExecutor(t, id)
		m := &continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(0), containerExecPlan()}, secondStarted: make(chan struct{}), blockSecond: true}
		s, _, _ := fixture(t, f, m)
		c := chat(t, s, id)
		r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Inspect keys"})
		if err != nil {
			t.Fatal(err)
		}
		<-m.secondStarted
		if _, err = s.Cancel(context.Background(), c.ID, r.ID); err != nil {
			t.Fatal(err)
		}
		snap := waitRun(t, s, c.ID, r.ID, Interrupted)
		waitJobs(t, s)
		if len(snap.Calls) != 1 || len(execInvocations(f)) != 0 {
			t.Fatal(snap.Calls)
		}
	})
	t.Run("provider-change", func(t *testing.T) {
		id := uuid.NewString()
		f := continuationExecutor(t, id)
		m := &continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(0), containerExecPlan()}}
		original := f.run
		f.run = func(ctx context.Context, snap target.ConnectionSnapshot, argv []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
			out, err := original(ctx, snap, argv, l)
			if argv[1] == "ps" {
				m.mu.Lock()
				m.info.ConfigHash = strings.Repeat("1", 64)
				m.mu.Unlock()
			}
			return out, err
		}
		s, _, _ := fixture(t, f, m)
		c := chat(t, s, id)
		r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Inspect keys"})
		if err != nil {
			t.Fatal(err)
		}
		waitRun(t, s, c.ID, r.ID, Failed)
		waitJobs(t, s)
		m.mu.Lock()
		n := len(m.plans)
		m.mu.Unlock()
		if n != 1 || len(execInvocations(f)) != 0 {
			t.Fatal("changed destination used continuation", n)
		}
	})
}

func TestDiscoveryMarkerOnlyAllowsUniqueContainerDiscoveryTargets(t *testing.T) {
	s, _, _ := fixture(t, executor(), nil)
	r := runRecord{Body: runBody{Targets: []target.ConnectionSnapshot{{}}}}
	for _, p := range []Plan{
		{Text: "empty", NeedsDiscovery: true},
		{NeedsDiscovery: true, Actions: []Action{{ToolID: "docker_exec", Args: json.RawMessage(`{"container":"redis","argv":["true"]}`), TargetIndexes: []int{0}}}},
		{NeedsDiscovery: true, Actions: append(discoveryPlan(0).Actions, discoveryPlan(0).Actions...)},
	} {
		if _, err := s.planCalls(r, p); err != ErrInvalid {
			t.Fatal("invalid discovery accepted", p, err)
		}
	}
}

func TestContinuationTotalCallLimitIncludesDiscovery(t *testing.T) {
	ids := []string{uuid.NewString(), uuid.NewString()}
	f := executor(ids...)
	f.run = continuationExecutor(t, ids[0]).run
	next := Plan{Text: "Confirm commands."}
	for i := 0; i < 23; i++ {
		next.Actions = append(next.Actions, containerExecPlan().Actions[0])
	}
	m := &continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(0, 1), next}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, ids...)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Inspect both containers"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	if len(snap.Calls) != 2 || len(execInvocations(f)) != 0 {
		t.Fatal("continued plan exceeded total limit", snap.Calls)
	}
}

func TestInterruptedContinuationIsNotResumedAfterRestart(t *testing.T) {
	id := uuid.NewString()
	f := continuationExecutor(t, id)
	m := &continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(0), Plan{}}, secondError: errors.New("provider failure")}
	s, _, db := fixture(t, f, m)
	c := chat(t, s, id)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Inspect keys"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate a process exit during the persisted, already-started continuation.
	if _, err = db.Exec("UPDATE ops_chat_runs SET status='planning' WHERE id=?", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE ops_chat_sessions SET active_run=? WHERE id=?", r.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	next, _ := newService(t, db, &[32]byte{1}, f, m)
	snap, err := next.Snapshot(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range snap.Runs {
		if run.ID == r.ID && run.Status != Interrupted {
			t.Fatal(run)
		}
	}
	m.mu.Lock()
	n := len(m.plans)
	m.mu.Unlock()
	if n != 2 || len(execInvocations(f)) != 0 || snap.Session.ActiveRunID != "" {
		t.Fatal("restart replanned", n, snap.Session)
	}
}
