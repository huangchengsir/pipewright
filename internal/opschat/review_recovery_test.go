package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Seed committed work without starting a worker, as after Submit's transaction.
func seedReviewRun(t *testing.T, s *Service, db *sql.DB, c *Session, status string, body runBody) string {
	t.Helper()
	id := uuid.NewString()
	sealed, err := s.seal(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO ops_chat_runs(id,session_id,request_id,payload_hash,status,body,created_at) VALUES(?,?,?,?,?,?,?)", id, c.ID, uuid.NewString(), "review", status, sealed, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE ops_chat_sessions SET active_run=? WHERE id=?", id, c.ID); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestImmediateCrashRestartWaitsForOldLeaseWithoutReplay(t *testing.T) {
	id := uuid.NewString()
	f := executor(id)
	m := model()
	s, v, db := fixture(t, f, m)
	var chats []*Session
	var runs []string
	statuses := []string{Queued, Planning, Running, Running}
	for i, status := range statuses {
		c := chat(t, s, id)
		chats = append(chats, c)
		run := seedReviewRun(t, s, db, c, status, runBody{Turn: TurnInput{Text: "question"}})
		runs = append(runs, run)
		if i >= 2 {
			cid := uuid.NewString()
			body, err := s.seal(callBody{Dispatched: i == 3, View: Call{ID: cid, SessionID: c.ID, RunID: run, ServerID: id, ToolID: "systemd_action", Status: Running}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO ops_chat_calls(id,session_id,run_id,server_id,tool_id,status,args_hash,target_hash,body,created_at,ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?)", cid, c.ID, run, id, "systemd_action", Running, "args", "target", body, time.Now().UnixMilli(), 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Stop heartbeat without Close: retain the old owner's lease, as a crash does.
	s.stop()
	<-s.leaseDone
	expires := time.Now().Add(250 * time.Millisecond).UnixMilli()
	if _, err := db.Exec("UPDATE ops_chat_state SET lease_until=? WHERE id=1", expires); err != nil {
		t.Fatal(err)
	}
	next, err := New(Options{DB: db, Vault: v, Executor: f, Model: m})
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Start(context.Background()); err != nil {
		t.Fatal("immediate restart discarded service", err)
	}
	t.Cleanup(func() { _ = next.Close(context.Background()) })
	if time.Now().UnixMilli() < expires || !next.Capabilities().Available {
		t.Fatal("started before old lease expired")
	}
	for i, c := range chats {
		want := Interrupted
		if i == 3 {
			want = Unknown
		}
		snap := waitRun(t, next, c.ID, runs[i], want)
		if snap.Session.ActiveRunID != "" || (i >= 2 && snap.Calls[0].Status != want) {
			t.Fatal("recovery did not release or distinguish dispatch", snap)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(f.invocations) != 0 || len(m.plans) != 0 || len(m.analyses) != 0 {
		t.Fatal("restart replayed remote work")
	}
}

func TestStartupLeaseWaitHonorsRootAndRejectsRenewedConsumer(t *testing.T) {
	s, v, db := fixture(t, executor(), nil)
	other, err := New(Options{DB: db, Vault: v})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err = other.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// A shortened fake heartbeat proves that renewal, rather than expiration,
	// rejects a live second consumer without waiting the full production lease.
	finished := make(chan error, 1)
	go func() { finished <- other.Start(context.Background()) }()
	time.Sleep(150 * time.Millisecond)
	if _, err = db.Exec("UPDATE ops_chat_state SET lease_until=lease_until+1000 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != ErrLease || other.Capabilities().Available || !s.Capabilities().Available {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("live consumer not rejected on renewal")
	}
}

func TestModelCountPersistenceFailureConvergesWithoutModelReplay(t *testing.T) {
	m := model()
	s, _, db := fixture(t, executor(), m)
	c := chat(t, s)
	run := seedReviewRun(t, s, db, c, Queued, runBody{Turn: TurnInput{Text: "question"}})
	if _, err := db.Exec(`CREATE TRIGGER fail_model_count BEFORE UPDATE ON ops_chat_runs
		WHEN OLD.status='planning' AND NEW.status='planning'
		BEGIN SELECT RAISE(FAIL,'injected model count failure'); END`); err != nil {
		t.Fatal(err)
	}
	s.launch(c.ID, run)
	snap := waitRun(t, s, c.ID, run, Failed)
	waitJobs(t, s)
	if snap.Session.ActiveRunID != "" || !s.Capabilities().Available {
		t.Fatal("failed worker left active run", snap.Session)
	}
	m.mu.Lock()
	n := len(m.plans)
	m.mu.Unlock()
	if n != 0 {
		t.Fatal("model ran without persisted count", n)
	}
	if _, err := db.Exec("DROP TRIGGER fail_model_count"); err != nil {
		t.Fatal(err)
	}
	next, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "new question"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, c.ID, next.ID, Succeeded)
	waitJobs(t, s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.plans) != 1 {
		t.Fatal("old model work replayed", len(m.plans))
	}
}

func TestPersistenceFailureAfterMutationConvergesWithoutRedispatch(t *testing.T) {
	for _, phase := range []string{"result_save", "read_count", "finish_save", "active_release"} {
		t.Run(phase, func(t *testing.T) {
			id := uuid.NewString()
			f := executor(id)
			s, _, db := fixture(t, f, nil)
			injected := make(chan struct{}, 1)
			f.run = func(_ context.Context, _ target.ConnectionSnapshot, argv []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
				if argv[1] == "show" {
					return &target.LimitedResult{Stdout: "active\n"}, nil
				}
				statement := map[string]string{
					"result_save":    `CREATE TRIGGER fail_result BEFORE UPDATE ON ops_chat_calls WHEN NEW.status='succeeded' BEGIN SELECT RAISE(FAIL,'injected'); END`,
					"read_count":     `CREATE TRIGGER fail_result BEFORE UPDATE ON ops_chat_runs WHEN NEW.status=OLD.status BEGIN SELECT RAISE(FAIL,'injected'); END`,
					"finish_save":    `CREATE TRIGGER fail_result BEFORE UPDATE ON ops_chat_runs WHEN NEW.status IN ('succeeded','unknown','failed','interrupted','partial_failed') BEGIN SELECT RAISE(FAIL,'injected'); END`,
					"active_release": `CREATE TRIGGER fail_result BEFORE UPDATE OF active_run ON ops_chat_sessions WHEN NEW.active_run='' AND OLD.active_run<>'' BEGIN SELECT RAISE(FAIL,'injected'); END`,
				}[phase]
				if _, err := db.Exec(statement); err != nil {
					t.Error(err)
				}
				injected <- struct{}{}
				return &target.LimitedResult{Stdout: "mutation completed\n"}, nil
			}
			c := chat(t, s, id)
			r := submitTool(t, s, c, "systemd_action", `{"unit":"nginx.service","action":"start"}`)
			a := waitRun(t, s, c.ID, r.ID, AwaitingConfirmation).Confirmations[0]
			waitJobs(t, s)
			if _, err := s.Confirm(context.Background(), c.ID, ConfirmInput{RunID: r.ID, Nonce: a.Nonce, Calls: a.Calls}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-injected:
			case <-time.After(time.Second):
				t.Fatal("mutation did not run")
			}
			// Outlast one three-attempt persistence loop; reconciliation must finish.
			time.Sleep(350 * time.Millisecond)
			if _, err := db.Exec("DROP TRIGGER fail_result"); err != nil {
				t.Fatal(err)
			}
			snap := waitRun(t, s, c.ID, r.ID, Succeeded, Unknown, Failed)
			waitJobs(t, s)
			if snap.Session.ActiveRunID != "" || f.countMutation() != 1 || !s.Capabilities().Available {
				t.Fatal("worker did not converge locally", snap.Session, f.countMutation())
			}
			if phase == "result_save" && snap.Calls[0].Status != Unknown {
				t.Fatal("lost dispatched result not preserved as unknown", snap.Calls)
			}
			var intents int
			if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='ops_chat_intent'").Scan(&intents); err != nil || intents != 1 {
				t.Fatal("audit intent repeated or missing", intents, err)
			}
			record, err := s.run(context.Background(), db, c.ID, r.ID)
			if err != nil || record.Body.ReadBytes > 512<<10 || record.Body.OutputBytes > 512<<10 {
				t.Fatal("quota changed during convergence", record, err)
			}
			entries, err := s.Events(context.Background(), c.ID, EventCursor(c.ID, 0), 100)
			if err != nil {
				t.Fatal(err)
			}
			finals := 0
			for _, entry := range entries.Entries {
				if entry.RunID == r.ID && entry.Kind == "run_status" && terminal(entry.Status) {
					finals++
				}
			}
			if finals != 1 {
				t.Fatal("final state emitted more than once", finals)
			}
		})
	}
}

type reviewFaultModel struct {
	*fakeModel
	afterPlan     func()
	afterAnalysis func()
}

func (m *reviewFaultModel) Plan(ctx context.Context, in PlanRequest) (Plan, error) {
	plan, err := m.fakeModel.Plan(ctx, in)
	if m.afterPlan != nil {
		m.afterPlan()
	}
	return plan, err
}
func (m *reviewFaultModel) Analyze(ctx context.Context, in AnalysisRequest) (string, error) {
	text, err := m.fakeModel.Analyze(ctx, in)
	if m.afterAnalysis != nil {
		m.afterAnalysis()
	}
	return text, err
}

func TestPlanSaveFailureNeverCallsModelAgain(t *testing.T) {
	id := uuid.NewString()
	m := &reviewFaultModel{fakeModel: model()}
	m.plan.Actions = []Action{{ToolID: "host_ports", Args: json.RawMessage("{}"), TargetIndexes: []int{0}}}
	f := executor(id)
	s, _, db := fixture(t, f, m)
	m.afterPlan = func() {
		if _, err := db.Exec(`CREATE TRIGGER fail_plan BEFORE INSERT ON ops_chat_calls BEGIN SELECT RAISE(FAIL,'injected plan save'); END`); err != nil {
			t.Error(err)
		}
	}
	c := chat(t, s, id)
	r, err := s.Submit(context.Background(), c.ID, TurnInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, Text: "list ports"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitRun(t, s, c.ID, r.ID, Failed)
	waitJobs(t, s)
	if _, err := db.Exec("DROP TRIGGER fail_plan"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(m.plans) != 1 || len(f.invocations) != 0 || snap.Session.ActiveRunID != "" || len(snap.Calls) != 0 {
		t.Fatal("plan save failure replayed or orphaned work", snap)
	}
}

func TestAnalysisFinalizationFailureNeverCallsModelAgain(t *testing.T) {
	id := uuid.NewString()
	m := &reviewFaultModel{fakeModel: model()}
	s, _, db := fixture(t, executor(id), m)
	c := chat(t, s, id)
	r := submitTool(t, s, c, "host_ports", "{}")
	snap := waitRun(t, s, c.ID, r.ID, Succeeded)
	waitJobs(t, s)
	preview, err := s.AnalysisPreview(context.Background(), c.ID, []string{snap.Calls[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	m.afterAnalysis = func() {
		if _, err := db.Exec(`CREATE TRIGGER fail_analysis BEFORE INSERT ON ops_chat_entries WHEN NEW.kind='analysis' BEGIN SELECT RAISE(FAIL,'injected analysis save'); END`); err != nil {
			t.Error(err)
		}
	}
	ar, err := s.Analyze(context.Background(), c.ID, AnalysisInput{ClientRequestID: uuid.NewString(), Revision: c.Revision, CallIDs: preview.CallIDs, PreviewHash: preview.Hash, Provider: preview.Provider, Consent: true})
	if err != nil {
		t.Fatal(err)
	}
	snap = waitRun(t, s, c.ID, ar.ID, Failed)
	waitJobs(t, s)
	if _, err := db.Exec("DROP TRIGGER fail_analysis"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.analyses) != 1 || snap.Session.ActiveRunID != "" {
		t.Fatal("analysis save failure replayed or orphaned model work", snap)
	}
	data, _ := json.Marshal(snap.Entries)
	if strings.Contains(string(data), "derived-sensitive-analysis") {
		t.Fatal("rolled-back analysis leaked into history")
	}
}
