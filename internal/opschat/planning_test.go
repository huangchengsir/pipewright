package opschat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type planningTestModel struct {
	*fakeModel
	failure error
	partial bool
}

func TestNoTargetsRejectsOperationsBeforeModelRequest(t *testing.T) {
	f := executor()
	m := model()
	s, _, _ := fixture(t, f, m)
	c := chat(t, s)
	_, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Check resources", AnalyzeResults: true, Provider: m.Info()})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	waitJobs(t, s)
	m.mu.Lock()
	plans, analyses := len(m.plans), len(m.analyses)
	m.mu.Unlock()
	if plans != 0 || analyses != 0 {
		t.Fatal("empty targets called model", plans, analyses)
	}
	snap, err := s.Snapshot(context.Background(), c.ID)
	if err != nil || len(snap.Calls) != 0 || len(snap.Runs) != 0 {
		t.Fatal("empty targets created operations", snap, err)
	}
}

func (m *planningTestModel) PlanStream(ctx context.Context, in PlanRequest, emit func(StreamChunk) error) (Plan, error) {
	plan, err := m.Plan(ctx, in)
	if err != nil {
		return plan, err
	}
	if m.partial {
		if err := emit(StreamChunk{Text: "Checking postgres."}); err != nil {
			return Plan{}, err
		}
	}
	return plan, m.failure
}

func (m *planningTestModel) AnalyzeStream(ctx context.Context, in AnalysisRequest, emit func(StreamChunk) error) (string, error) {
	return m.Analyze(ctx, in)
}

func TestPlanningFailureDiagnosticsNeverExecuteOrRetry(t *testing.T) {
	validAction := Action{ToolID: "docker_inspect", Args: json.RawMessage(`{"container":"postgres"}`), TargetIndexes: []int{0}}
	cases := []struct {
		name    string
		failure error
		plan    Plan
		code    string
	}{
		{"token", classifiedAnalysisError("token_limit"), Plan{}, "token_limit"},
		{"timeout", context.DeadlineExceeded, Plan{}, "timeout"},
		{"rejected", classifiedAnalysisError("rejected"), Plan{}, "rejected"},
		{"invalid-response", classifiedAnalysisError("invalid_response"), Plan{}, "invalid_response"},
		{"unavailable", ErrUnavailable, Plan{}, "unavailable"},
		{"input-limit", ErrQuota, Plan{}, "input_limit"},
		{"unknown", errors.New("secret-key http://private.invalid/raw-body"), Plan{}, "unknown"},
		{"unknown-code", classifiedAnalysisError("secret-key"), Plan{}, "unknown"},
		{"model-invalid", fmt.Errorf("private-format: %w", ErrInvalid), Plan{}, "invalid_plan"},
		{"empty", nil, Plan{}, "invalid_plan"},
		{"unknown-tool-after-valid", nil, Plan{Text: "Checking postgres.", Actions: []Action{validAction, {ToolID: "shell_exec", Args: json.RawMessage(`{}`), TargetIndexes: []int{0}}}}, "invalid_plan"},
		{"unknown-argument", nil, Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: json.RawMessage(`{"container":"postgres","shell":"sh"}`), TargetIndexes: []int{0}}}}, "invalid_plan"},
		{"missing-container", nil, Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: json.RawMessage(`{}`), TargetIndexes: []int{0}}}}, "invalid_plan"},
		{"bad-container", nil, Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: json.RawMessage(`{"container":"postgres;id"}`), TargetIndexes: []int{0}}}}, "invalid_plan"},
		{"duplicate-target", nil, Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: validAction.Args, TargetIndexes: []int{0, 0}}}}, "invalid_plan"},
		{"out-of-range", nil, Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: validAction.Args, TargetIndexes: []int{1}}}}, "invalid_plan"},
		{"no-target", nil, Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: validAction.Args}}}, "invalid_plan"},
		{"oversize-plan", nil, Plan{Text: strings.Repeat("x", 33<<10)}, "invalid_plan"},
	}
	tooMany := Plan{Text: "Checking postgres."}
	for i := 0; i < 25; i++ {
		tooMany.Actions = append(tooMany.Actions, validAction)
	}
	cases = append(cases, struct {
		name    string
		failure error
		plan    Plan
		code    string
	}{"too-many-calls", nil, tooMany, "invalid_plan"})
	for _, tc := range cases {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/partial=%v", tc.name, partial), func(t *testing.T) {
				id := uuid.NewString()
				f := executor(id)
				m := &planningTestModel{fakeModel: model(), failure: tc.failure, partial: partial}
				m.plan = tc.plan
				s, _, db := fixture(t, f, m)
				c := chat(t, s, id)
				input := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Check postgres container"}
				r, err := s.Submit(context.Background(), c.ID, input)
				if err != nil {
					t.Fatal(err)
				}
				snap := waitRun(t, s, c.ID, r.ID, Failed)
				waitJobs(t, s)
				if len(snap.Calls) != 0 {
					t.Fatal("invalid plan persisted calls")
				}
				if repeated, err := s.Submit(context.Background(), c.ID, input); err != nil || repeated.ID != r.ID {
					t.Fatal(repeated, err)
				}
				s.planningFailure(c.ID, r.ID, Failed, tc.failure)
				entries, err := s.Entries(context.Background(), c.ID, "", 100)
				if err != nil {
					t.Fatal(err)
				}
				markers, partials := 0, 0
				for _, entry := range entries.Entries {
					if entry.Kind != "assistant" {
						continue
					}
					if entry.Text == "[planning:"+tc.code+"]" {
						markers++
						if entry.Status != Failed {
							t.Fatal(entry)
						}
					} else if entry.Text == "Checking postgres." && partial {
						partials++
						if entry.Status != Interrupted {
							t.Fatal("invalid plan published success", entry)
						}
					} else {
						t.Fatal("unexpected/raw diagnostic", entry)
					}
					var allowed int
					if db.QueryRow("SELECT context_allowed FROM ops_chat_entries WHERE session_id=? AND seq=?", c.ID, entry.Seq).Scan(&allowed) != nil || allowed != 0 {
						t.Fatal("failed planning entered context")
					}
				}
				if markers != 1 || partial && partials != 1 || !partial && partials != 0 {
					t.Fatal(markers, partials)
				}
				f.mu.Lock()
				calls := len(f.invocations)
				f.mu.Unlock()
				m.mu.Lock()
				plans := len(m.plans)
				m.mu.Unlock()
				if calls != 0 || plans != 1 {
					t.Fatal("failure executed or retried", calls, plans)
				}
			})
		}
	}
}

func TestPlanningValidPostgresPlanStillExecutesOnce(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := &planningTestModel{fakeModel: model(), partial: true}
	m.plan = Plan{Text: "Checking postgres.", Actions: []Action{{ToolID: "docker_inspect", Args: json.RawMessage(`{"container":"postgres"}`), TargetIndexes: []int{0}}}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Check postgres container"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	if len(snap.Calls) != 1 || snap.Calls[0].ToolID != "docker_inspect" || snap.Calls[0].Status != Succeeded {
		t.Fatal(snap.Calls)
	}
	n := 0
	for _, e := range snap.Entries.Entries {
		if e.Kind == "assistant" {
			n++
			if e.Status != Succeeded || e.Text != "Checking postgres." {
				t.Fatal(e)
			}
		}
	}
	if n != 1 {
		t.Fatal("duplicate reply", n)
	}
	if err := s.convergeRun(c.ID, r.ID, Failed, false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	calls := len(f.invocations)
	if calls == 2 && (f.invocations[0].argv[len(f.invocations[0].argv)-1] != "postgres" || f.invocations[1].argv[len(f.invocations[1].argv)-1] != strings.Repeat("a", 64)) {
		t.Error("container identity was not frozen")
	}
	f.mu.Unlock()
	m.mu.Lock()
	plans := len(m.plans)
	m.mu.Unlock()
	// One logical call performs one ID resolution and one projected inspect.
	if calls != 2 || plans != 1 {
		t.Fatal(calls, plans)
	}
}
