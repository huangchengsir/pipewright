package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

type boundaryStreamModel struct{ *continuationModel }

func (m *boundaryStreamModel) PlanStream(ctx context.Context, in PlanRequest, emit func(StreamChunk) error) (Plan, error) {
	p, err := m.Plan(ctx, in)
	if err == nil {
		err = emit(StreamChunk{Text: p.Text, Fallback: true})
	}
	return p, err
}
func (m *boundaryStreamModel) AnalyzeStream(ctx context.Context, in AnalysisRequest, emit func(StreamChunk) error) (string, error) {
	m.Analyze(ctx, in)
	if err := emit(StreamChunk{Text: "partial analysis", Fallback: true}); err != nil {
		return "", err
	}
	return "", ErrInvalid
}

func TestReleaseQuotaReservesDiscoveryContinuationAndAnalysisFailure(t *testing.T) {
	ids, indexes := make([]string, 8), make([]int, 8)
	for i := range ids {
		ids[i], indexes[i] = uuid.NewString(), i
	}
	f := executor(ids...)
	next := Plan{Text: "Collect follow-up facts."}
	for i := 0; i < 2; i++ {
		next.Actions = append(next.Actions, Action{ToolID: "host_ports", Args: json.RawMessage(`{}`), TargetIndexes: indexes})
	}
	m := &boundaryStreamModel{&continuationModel{fakeModel: model(), steps: []Plan{discoveryPlan(indexes...), next}}}
	s, _, db := fixture(t, f, m)
	c := chat(t, s, ids...)
	base := resourceLimits.Entries - resourceLimits.Calls - 11
	if err := s.persist(c.ID, func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < base; i++ {
			if err := s.append(ctx, tx, Entry{SessionID: c.ID, Kind: "notice"}, true, false); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Check all hosts", AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	var actual, ledger int
	if err := db.QueryRow("SELECT COUNT(*) FROM ops_chat_entries WHERE session_id=? AND visible=1", c.ID).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT visible_count FROM ops_chat_sessions WHERE id=?", c.ID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	notices, replies, errors, summaries := 0, 0, 0, 0
	for _, e := range snap.Entries.Entries {
		if e.RunID != r.ID {
			continue
		}
		switch e.Kind {
		case "notice":
			notices++
		case "assistant":
			replies++
		case "analysis":
			if e.Status == Failed {
				errors++
			}
		case "summary":
			summaries++
		}
	}
	if len(snap.Calls) != 24 || actual != ledger || actual > resourceLimits.Entries || notices != 3 || replies != 2 || errors != 1 || summaries != 1 || snap.Session.ActiveRunID != "" || s.ready() != nil {
		t.Fatal("whole-turn quota invariant failed", len(snap.Calls), actual, ledger, notices, replies, errors, summaries, snap.Session.ActiveRunID)
	}
}

func TestReleaseQuotaRejectsFormerUnsafeAdmissionBeforeWork(t *testing.T) {
	id := uuid.NewString()
	f, m := executor(id), model()
	s, _, db := fixture(t, f, m)
	c := chat(t, s, id)
	if _, err := db.Exec("UPDATE ops_chat_sessions SET visible_count=1973 WHERE id=?", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Check"}); err != ErrQuota {
		t.Fatal(err)
	}
	if len(m.plans) != 0 || len(f.invocations) != 0 {
		t.Fatal("quota rejection dispatched work")
	}
}

func TestReleasePlan25KiBIsAcceptedWithAndWithoutStreaming(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "stream"}[streaming], func(t *testing.T) {
			id := uuid.NewString()
			text := strings.Repeat("x", 25<<10)
			base := &continuationModel{fakeModel: model(), steps: []Plan{{Text: text}}}
			var m Model = base
			if streaming {
				m = &boundaryStreamModel{base}
			}
			s, _, _ := fixture(t, executor(id), m)
			c := chat(t, s, id)
			r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Explain"})
			if err != nil {
				t.Fatal(err)
			}
			snap := waitRun(t, s, c.ID, r.ID, Succeeded)
			waitJobs(t, s)
			replies := 0
			for _, entry := range snap.Entries.Entries {
				if entry.Kind == "assistant" {
					replies++
					if entry.Text != text {
						t.Fatal("valid plan text was truncated", len(entry.Text))
					}
				}
			}
			if replies != 1 || len(base.plans) != 1 {
				t.Fatal(replies, len(base.plans))
			}
		})
	}
}

func TestReleaseEscapedSSEUsesHintAndSnapshotPreservesFullOutput(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	output := strings.Repeat("\x01", 64<<10)
	f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: output}, nil
	}
	s, _, _ := fixture(t, f, nil)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "host_ports", "{}")
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	if len(snap.Calls) != 1 || snap.Calls[0].Output != output {
		t.Fatal("snapshot lost output")
	}
	page, err := s.Events(context.Background(), c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	hints := 0
	for _, e := range page.Entries {
		b, err := json.Marshal(e)
		if err != nil || len(b)+1024 > 256<<10 {
			t.Fatal("SSE frame exceeds parser cap", len(b), err)
		}
		if e.Kind == "snapshot_required" {
			hints++
		}
	}
	if hints == 0 || page.Cursor != EventCursor(c.ID, snap.Watermark) {
		t.Fatal("missing bounded replay hint", page.Cursor, hints)
	}
}

func TestReleaseRetryPreservesQuestionButNeverInheritsAnalysisConsent(t *testing.T) {
	id := uuid.NewString()
	f, m := executor(id), model()
	f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
		return nil, ErrUnavailable
	}
	m.plan.Actions = []Action{{ToolID: "host_ports", Args: json.RawMessage(`{}`), TargetIndexes: []int{0}}}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	const question = "Why is the service unreachable?"
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: question, AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	first := waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	before := len(m.analyses)
	f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: "recovered"}, nil
	}
	c, err = s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryFailed(context.Background(), c.ID, RetryInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: []string{first.Calls[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, retry.ID, Succeeded)
	waitJobs(t, s)
	body, err := s.run(context.Background(), s.db, c.ID, retry.ID)
	if err != nil || body.Body.Question != question || body.Body.AnalysisAuthorized || body.Body.Provider != (ProviderInfo{}) || len(m.analyses) != before {
		t.Fatal("retry inherited consent or lost provenance", body.Body, err)
	}
	c, err = s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	ar, err := s.Analyze(context.Background(), c.ID, AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, RunID: retry.ID, AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, ar.ID, Succeeded)
	waitJobs(t, s)
	if len(m.analyses) != before+1 || m.analyses[before].Question != question {
		t.Fatal("explicit retry analysis lost question", m.analyses)
	}
}
