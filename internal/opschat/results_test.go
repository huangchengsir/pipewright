package opschat

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyPlanServerOrderAndSummaryOnce(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	f := executor(a, b)
	m := model()
	for _, tool := range []string{"host_ports", "host_processes", "docker_stats", "docker_images"} {
		m.plan.Actions = append(m.plan.Actions, Action{ToolID: tool, Args: json.RawMessage(`{}`), TargetIndexes: []int{1, 0}})
	}
	s, v, _ := fixture(t, f, m)
	if _, err := v.Create(vault.CreateInput{Name: "secret", Type: vault.TypeSSHPassword, Secret: "known-password"}); err != nil {
		t.Fatal(err)
	}
	f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: "known-password " + strings.Repeat("x", 1024)}, nil
	}
	c := chat(t, s, a, b)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "compare"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	f.mu.Lock()
	for i, call := range f.invocations {
		expected := a
		if i >= 4 {
			expected = b
		}
		if call.server != expected {
			t.Errorf("call %d server %s expected %s", i, call.server, expected)
		}
	}
	f.mu.Unlock()
	if err = s.convergeRun(c.ID, r.ID, Failed, false); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Entries(context.Background(), c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries.Entries {
		if e.Kind != "summary" {
			continue
		}
		n++
		raw, _ := json.Marshal(e.Summary)
		if strings.Contains(string(raw), "known-password") || len(raw) > 24<<10 {
			t.Fatal("unsafe/unbounded summary")
		}
		if len(e.Summary.Servers) != 2 || len(e.Summary.Servers[0].Facts) != 4 {
			t.Fatal(e.Summary)
		}
		if !e.Summary.Servers[0].Facts[0].Truncated {
			t.Fatal("excerpt truncation unmarked")
		}
	}
	if n != 1 || len(snap.Calls) != 8 {
		t.Fatal(n, len(snap.Calls))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.analyses) != 0 {
		t.Fatal("legacy request triggered analysis")
	}
	messages, err := s.contextMessages(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Text, "xxxx") {
			t.Fatal("local facts entered planning")
		}
	}
}

type streamingResultsModel struct {
	*fakeModel
	entered chan struct{}
	release chan struct{}
}

type failingAnalysisStream struct{ *fakeModel }

func (m *failingAnalysisStream) PlanStream(ctx context.Context, in PlanRequest, emit func(StreamChunk) error) (Plan, error) {
	return m.Plan(ctx, in)
}
func (m *failingAnalysisStream) AnalyzeStream(ctx context.Context, in AnalysisRequest, emit func(StreamChunk) error) (string, error) {
	m.Analyze(ctx, in)
	if err := emit(StreamChunk{Text: "safe incomplete analysis"}); err != nil {
		return "", err
	}
	return "", ErrInvalid
}
func TestFailedAnalysisStreamPreservesToolsAndNeverReexecutes(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := &failingAnalysisStream{model()}
	for _, tool := range []string{"host_ports", "host_processes"} {
		m.plan.Actions = append(m.plan.Actions, Action{ToolID: tool, Args: json.RawMessage(`{}`), TargetIndexes: []int{0}})
	}
	s, _, db := fixture(t, f, m)
	c := chat(t, s, id)
	in := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "compare", AnalyzeResults: true, Provider: m.Info()}
	r, err := s.Submit(context.Background(), c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	partial := 0
	notice := 0
	for _, entry := range snap.Entries.Entries {
		if entry.Text == "safe incomplete analysis" {
			partial++
			if entry.Status != Interrupted {
				t.Fatal(entry)
			}
			var allowed int
			db.QueryRow("SELECT context_allowed FROM ops_chat_entries WHERE session_id=? AND seq=?", c.ID, entry.Seq).Scan(&allowed)
			if allowed != 0 {
				t.Fatal("partial analysis context")
			}
		}
		if entry.Kind == "analysis" && entry.Status == Failed {
			notice++
		}
	}
	if partial != 1 || notice != 1 {
		t.Fatal(partial, notice)
	}
	for _, call := range snap.Calls {
		if call.Status != Succeeded {
			t.Fatal(call)
		}
	}
	if _, err = s.Submit(context.Background(), c.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.convergeRun(c.ID, r.ID, Failed, false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(f.invocations) != 2 || len(m.plans) != 1 || len(m.analyses) != 1 {
		t.Fatal("replayed tools/model", len(f.invocations), len(m.plans), len(m.analyses))
	}
}

func (m *streamingResultsModel) PlanStream(ctx context.Context, in PlanRequest, emit func(StreamChunk) error) (Plan, error) {
	m.Plan(ctx, in)
	if err := emit(StreamChunk{Text: "first part "}); err != nil {
		return Plan{}, err
	}
	close(m.entered)
	select {
	case <-m.release:
	case <-ctx.Done():
		return Plan{}, ctx.Err()
	}
	if err := emit(StreamChunk{Text: "final reply"}); err != nil {
		return Plan{}, err
	}
	return Plan{Text: "first part final reply"}, nil
}
func (m *streamingResultsModel) AnalyzeStream(ctx context.Context, in AnalysisRequest, emit func(StreamChunk) error) (string, error) {
	return m.Analyze(ctx, in)
}

func TestStreamingRevisionSnapshotReplayBudgetAndCancel(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "final", true: "cancel"}[stop], func(t *testing.T) {
			m := &streamingResultsModel{fakeModel: model(), entered: make(chan struct{}), release: make(chan struct{})}
			s, _, db := fixture(t, executor(), m)
			c := chat(t, s)
			r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "question"})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-m.entered:
			case <-time.After(time.Second):
				t.Fatal("no persisted first chunk")
			}
			first, err := s.Snapshot(context.Background(), c.ID)
			if err != nil {
				t.Fatal(err)
			}
			seq := int64(0)
			for _, e := range first.Entries.Entries {
				if e.Kind == "assistant" {
					seq = e.Seq
					if e.Status != "partial" || e.Text != "first part " {
						t.Fatal(e)
					}
				}
			}
			var allowed int
			if db.QueryRow("SELECT context_allowed FROM ops_chat_entries WHERE session_id=? AND seq=?", c.ID, seq).Scan(&allowed) != nil || allowed != 0 {
				t.Fatal("partial entered context")
			}
			status := Succeeded
			if stop {
				status = Interrupted
				if _, err = s.Cancel(context.Background(), c.ID, r.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				close(m.release)
			}
			final := waitRun(t, s, c.ID, r.ID, status)
			waitJobs(t, s)
			if stop {
				again, err := s.Cancel(context.Background(), c.ID, r.ID)
				if err != nil || again.Status != Interrupted {
					t.Fatal("duplicate cancellation changed terminal", again, err)
				}
			}
			n := 0
			expected := "first part final reply"
			if stop {
				expected = "first part "
			}
			for _, e := range final.Entries.Entries {
				if e.Kind == "assistant" && e.Seq == seq {
					n++
					if e.Text != expected || e.Status != status {
						t.Fatal(e)
					}
				}
			}
			if n != 1 {
				t.Fatal("missing or duplicated stream entry", n)
			}
			// Simulate losing the terminal SSE event: durable replay and a snapshot both contain final text.
			replay, err := s.Events(context.Background(), c.ID, EventCursor(c.ID, first.Watermark), 100)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range replay.Entries {
				if e.ReplaceSeq == seq && e.Text == expected && e.Status == status {
					found = true
				}
			}
			if !found {
				t.Fatal("lost terminal revision", replay)
			}
			var bodyBytes, entryBytes int
			if db.QueryRow("SELECT body_bytes FROM ops_chat_sessions WHERE id=?", c.ID).Scan(&bodyBytes) != nil || db.QueryRow("SELECT SUM(body_size) FROM ops_chat_entries WHERE session_id=?", c.ID).Scan(&entryBytes) != nil || bodyBytes != entryBytes {
				t.Fatal("revision charged repeatedly", bodyBytes, entryBytes)
			}
			if db.QueryRow("SELECT context_allowed FROM ops_chat_entries WHERE session_id=? AND seq=?", c.ID, seq).Scan(&allowed) != nil || stop && allowed != 0 {
				t.Fatal("cancelled output entered context")
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.plans) != 1 {
				t.Fatal("replayed model")
			}
		})
	}
}

func TestSummaryStructuredCPUAndMemoryOccupiers(t *testing.T) {
	s, _, _ := fixture(t, executor(), nil)
	id := uuid.NewString()
	processes := "PID COMMAND %CPU %MEM\n1 cpu-owner 95 1\n2 memory-owner 1 90\n3 worker 10 2\n4 known-password 0 3\n"
	s.masker.RegisterSecret("known-password")
	containers := `{"id":"a","name":"cpu-app","cpu":"90%","memory":"1MiB / 1GiB"}` + "\n" + `{"id":"b","name":"memory-app","cpu":"1%","memory":"900MiB / 1GiB"}`
	summary := s.summarize([]callRecord{{Body: callBody{View: Call{ServerID: id, ToolID: "host_processes", Status: Succeeded, Output: processes}}}, {Body: callBody{View: Call{ServerID: id, ToolID: "docker_stats", Status: Succeeded, Output: containers}}}})
	server := summary.Servers[0]
	if len(server.Missing) != 0 || len(server.Facts[0].Occupants) == 0 || len(server.Facts[1].Occupants) != 2 {
		t.Fatal(server)
	}
	for _, f := range server.Facts {
		if f.Excerpt != "" {
			t.Fatal("raw excerpt used instead of structured occupants")
		}
	}
	raw, _ := json.Marshal(summary)
	if strings.Contains(string(raw), "known-password") || !strings.Contains(string(raw), "memory-owner") || !strings.Contains(string(raw), "memory-app") {
		t.Fatal(string(raw))
	}
}

func TestSummaryMissingResourcesRemainMissing(t *testing.T) {
	s, _, _ := fixture(t, executor(), nil)
	summary := s.summarize([]callRecord{{Body: callBody{View: Call{ServerID: uuid.NewString(), Status: Failed, ToolID: "host_resources", Resources: &HostResources{Unavailable: []string{"disks", "memory", "load"}}}}}})
	f := summary.Servers[0].Facts[0]
	if f.Status != Failed || f.Resources.Memory != nil || f.Resources.Load != nil || len(f.Resources.Unavailable) != 3 {
		t.Fatal(f)
	}
}

func TestCurrentTurnAutomaticAnalysisOnceOriginalQuestion(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	f := executor(a, b)
	m := model()
	for _, tool := range []string{"host_ports", "host_processes", "docker_stats", "docker_images"} {
		m.plan.Actions = append(m.plan.Actions, Action{ToolID: tool, Args: json.RawMessage(`{}`), TargetIndexes: []int{0, 1}})
	}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, a, b)
	in := TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "Which server uses more resources?", AnalyzeResults: true, Provider: m.Info()}
	r, err := s.Submit(context.Background(), c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	if _, err = s.Submit(context.Background(), c.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	if len(m.plans) != 1 || len(m.analyses) != 1 || m.analyses[0].Question != in.Text || len(m.analyses[0].Items) != 8 {
		t.Fatal(m.plans, m.analyses)
	}
	m.mu.Unlock()
	// Explicit whole-run analysis resolves all calls server-side, independent of a history page.
	next, err := s.Analyze(context.Background(), c.ID, AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, RunID: r.ID, AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, next.ID, Succeeded)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.analyses) != 2 || len(m.analyses[1].Items) != 8 || m.analyses[1].Question != in.Text {
		t.Fatal(m.analyses)
	}
}

func TestAutomaticAnalysisQuotaPreservesSSHSuccessAndNoUpload(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := model()
	m.plan.Actions = []Action{{ToolID: "host_ports", Args: json.RawMessage(`{}`), TargetIndexes: []int{0}}}
	f.run = func(context.Context, target.ConnectionSnapshot, []string, target.ExecutionLimits) (*target.LimitedResult, error) {
		return &target.LimitedResult{Stdout: strings.Repeat("x", 31<<10)}, nil
	}
	s, _, _ := fixture(t, f, m)
	c := chat(t, s, id)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "check", AnalyzeResults: true, Provider: m.Info()})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	if snap.Calls[0].Status != Succeeded {
		t.Fatal(snap.Calls)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.analyses) != 0 {
		t.Fatal("oversized upload")
	}
	found := false
	for _, e := range snap.Entries.Entries {
		if e.Kind == "analysis" && e.Status == Failed {
			found = true
		}
	}
	if !found {
		t.Fatal("missing explicit analysis gap")
	}
}
