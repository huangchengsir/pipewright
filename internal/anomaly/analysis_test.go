package anomaly

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

type analyzerFunc func(context.Context, AnalysisInput) (*Analysis, error)

func (f analyzerFunc) AnalyzeAlert(ctx context.Context, in AnalysisInput) (*Analysis, error) {
	return f(ctx, in)
}

type channelNotifier chan Alert

func (c channelNotifier) NotifyAlert(_ context.Context, a *Alert) { c <- *a }
func boolPtr(v bool) *bool                                        { return &v }

func readyAnalysis() *Analysis {
	return &Analysis{Status: AnalysisReady, Hypothesis: "Observed high memory; confirm the responsible workload", Confidence: "medium", FixSuggestions: []string{"Inspect the workload before changing limits"}}
}
func receiveAnalysis(t *testing.T, events channelNotifier) Alert {
	t.Helper()
	select {
	case a := <-events:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("notification did not arrive")
		return Alert{}
	}
}
func newAnalysisRule(t *testing.T, s Service, enabled *bool) *Rule {
	t.Helper()
	rule, err := s.CreateRule(context.Background(), CreateRuleInput{Metric: MetricMemory, Operator: OperatorGreaterThan, Threshold: 80, Enabled: true, AIAnalysisEnabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}
func memoryCollector(ids ...string) MetricsCollector {
	snaps := []ServerMetricsSnapshot{}
	for _, id := range ids {
		snaps = append(snaps, ServerMetricsSnapshot{ServerID: id, ServerName: id, Available: true, MemoryPercent: fptr(95)})
	}
	return stubCollector{snaps: snaps}
}

func TestAnalysisRuleDefaultAndOmittedUpdate(t *testing.T) {
	s := New(testDB(t), nil)
	defer s.Close()
	rule := newAnalysisRule(t, s, nil)
	if rule.AIAnalysisEnabled {
		t.Fatal("old rules implicitly enable AI")
	}
	in := CreateRuleInput{Metric: MetricMemory, Operator: OperatorGreaterThan, Threshold: 80, Enabled: true, AIAnalysisEnabled: boolPtr(true)}
	updated, err := s.UpdateRule(context.Background(), rule.ID, in)
	if err != nil || !updated.AIAnalysisEnabled {
		t.Fatalf("enable: %+v %v", updated, err)
	}
	in.AIAnalysisEnabled = nil
	updated, err = s.UpdateRule(context.Background(), rule.ID, in)
	if err != nil || !updated.AIAnalysisEnabled {
		t.Fatalf("old client cleared setting: %+v %v", updated, err)
	}
	in.AIAnalysisEnabled = boolPtr(false)
	updated, err = s.UpdateRule(context.Background(), rule.ID, in)
	if err != nil || updated.AIAnalysisEnabled {
		t.Fatalf("disable: %+v %v", updated, err)
	}
}

func TestAnalysisOffNeverInvokesAnalyzer(t *testing.T) {
	var calls atomic.Int32
	events := make(channelNotifier, 2)
	s := New(testDB(t), memoryCollector("server"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(context.Context, AnalysisInput) (*Analysis, error) { calls.Add(1); return readyAnalysis(), nil })))
	defer s.Close()
	newAnalysisRule(t, s, nil)
	alerts, err := s.Check(context.Background())
	if err != nil || len(alerts) != 1 {
		t.Fatalf("check: %v %v", alerts, err)
	}
	a := receiveAnalysis(t, events)
	if a.Analysis != nil || calls.Load() != 0 {
		t.Fatalf("AI ran while disabled: %+v", a)
	}
}

func TestAnalysisReadyPersistsAndLeavesReturnedSnapshotAlone(t *testing.T) {
	gate := make(chan struct{})
	events := make(channelNotifier, 2)
	s := New(testDB(t), memoryCollector("server"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(ctx context.Context, in AnalysisInput) (*Analysis, error) {
		select {
		case <-gate:
			return readyAnalysis(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})))
	defer s.Close()
	newAnalysisRule(t, s, boolPtr(true))
	alerts, err := s.Check(context.Background())
	if err != nil || len(alerts) != 1 || alerts[0].Analysis.Status != AnalysisPending {
		t.Fatalf("check did not return pending: %v %v", alerts, err)
	}
	close(gate)
	notified := receiveAnalysis(t, events)
	if notified.Analysis.Status != AnalysisReady || notified.Message != alerts[0].Message {
		t.Fatal("original alert missing from enriched notification")
	}
	if alerts[0].Analysis.Status != AnalysisPending {
		t.Fatal("background worker mutated the returned snapshot")
	}
	stored, err := s.ListAlerts(context.Background(), "server", 10)
	if err != nil || len(stored) != 1 || stored[0].Analysis.Hypothesis != notified.Analysis.Hypothesis {
		t.Fatalf("result not stored: %+v %v", stored, err)
	}
	again, err := s.Check(context.Background())
	if err != nil || len(again) != 0 {
		t.Fatal("cooldown failed")
	}
	select {
	case <-events:
		t.Fatal("duplicate notification")
	default:
	}
}

func TestAnalysisFailurePathsStillNotify(t *testing.T) {
	for _, mode := range []string{"missing", "error", "panic", "timeout", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			events := make(channelNotifier, 2)
			options := []Option{WithNotifier(events), WithAnalysisTimeout(20 * time.Millisecond)}
			if mode != "missing" {
				options = append(options, WithAnalyzer(analyzerFunc(func(ctx context.Context, _ AnalysisInput) (*Analysis, error) {
					switch mode {
					case "error":
						return nil, errors.New("unsafe provider detail")
					case "panic":
						panic("unexpected adapter failure")
					case "timeout":
						<-ctx.Done()
						return readyAnalysis(), nil
					default:
						return &Analysis{Status: AnalysisReady}, nil
					}
				})))
			}
			s := New(testDB(t), memoryCollector("server"), options...)
			defer s.Close()
			newAnalysisRule(t, s, boolPtr(true))
			alerts, err := s.Check(context.Background())
			if err != nil || len(alerts) != 1 {
				t.Fatal(err)
			}
			a := receiveAnalysis(t, events)
			if a.Analysis == nil || a.Analysis.Status != AnalysisUnavailable || a.Message != alerts[0].Message {
				t.Fatalf("fallback missing: %+v", a)
			}
			stored, _ := s.ListAlerts(context.Background(), "", 10)
			if stored[0].Analysis.Status != AnalysisUnavailable {
				t.Fatal("fallback not persisted")
			}
		})
	}
}

func TestAnalysisConcurrencyBusyFallback(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	events := make(channelNotifier, 4)
	s := New(testDB(t), memoryCollector("a", "b", "c"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(ctx context.Context, _ AnalysisInput) (*Analysis, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-gate:
			return readyAnalysis(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})))
	defer s.Close()
	newAnalysisRule(t, s, boolPtr(true))
	alerts, err := s.Check(context.Background())
	if err != nil || len(alerts) != 3 {
		t.Fatalf("check: %d %v", len(alerts), err)
	}
	busy := receiveAnalysis(t, events)
	if busy.Analysis.Status != AnalysisUnavailable {
		t.Fatal("busy alert was not sent without waiting")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("bounded workers did not start")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("more than two analyses started")
	}
	close(gate)
	for i := 0; i < 2; i++ {
		if receiveAnalysis(t, events).Analysis.Status != AnalysisReady {
			t.Fatal("successful worker not delivered")
		}
	}
}

func TestAnalysisConcurrentChecksDeduplicate(t *testing.T) {
	events := make(channelNotifier, 30)
	var calls atomic.Int32
	s := New(testDB(t), memoryCollector("server"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(context.Context, AnalysisInput) (*Analysis, error) { calls.Add(1); return readyAnalysis(), nil })))
	defer s.Close()
	newAnalysisRule(t, s, boolPtr(true))
	var wg sync.WaitGroup
	var count atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alerts, err := s.Check(context.Background())
			if err != nil {
				t.Error(err)
			}
			count.Add(int32(len(alerts)))
		}()
	}
	wg.Wait()
	receiveAnalysis(t, events)
	if count.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("duplicates: alerts=%d calls=%d", count.Load(), calls.Load())
	}
}

func TestAnalysisDisableCancelsInFlight(t *testing.T) {
	events := make(channelNotifier, 2)
	started := make(chan struct{})
	s := New(testDB(t), memoryCollector("server"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(ctx context.Context, _ AnalysisInput) (*Analysis, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})))
	defer s.Close()
	rule := newAnalysisRule(t, s, boolPtr(true))
	s.Check(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("analysis not started")
	}
	_, err := s.UpdateRule(context.Background(), rule.ID, CreateRuleInput{Metric: MetricMemory, Operator: OperatorGreaterThan, Threshold: 80, Enabled: true, AIAnalysisEnabled: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if receiveAnalysis(t, events).Analysis.Status != AnalysisUnavailable {
		t.Fatal("disabled work continued")
	}
}

func TestAnalysisExplicitDisableCancelsBeforeFailedRead(t *testing.T) {
	storetest.SkipIfMySQL(t)
	for _, mode := range []string{"analysis", "rule"} {
		t.Run(mode, func(t *testing.T) {
			db := testDB(t)
			started := make(chan struct{})
			stopped := make(chan struct{})
			events := make(channelNotifier, 2)
			s := New(db, memoryCollector("server"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(ctx context.Context, _ AnalysisInput) (*Analysis, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				return nil, ctx.Err()
			})))
			defer s.Close()
			rule := newAnalysisRule(t, s, boolPtr(true))
			if _, err := s.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("analysis not started")
			}
			if _, err := db.Exec(`CREATE TRIGGER corrupt_disabled_rule AFTER UPDATE ON anomaly_rules
				WHEN NEW.enabled = 0 OR NEW.ai_analysis_enabled = 0
				BEGIN UPDATE anomaly_rules SET created_at = 'invalid' WHERE id = NEW.id; END`); err != nil {
				t.Fatal(err)
			}
			in := CreateRuleInput{Metric: MetricMemory, Operator: OperatorGreaterThan, Threshold: 80}
			if mode == "analysis" {
				in.PreserveEnabled = true
				in.AIAnalysisEnabled = boolPtr(false)
			}
			if _, err := s.UpdateRule(context.Background(), rule.ID, in); err == nil {
				t.Fatal("expected follow-up read failure")
			}
			var enabled, aiEnabled int
			if err := db.QueryRow(`SELECT enabled, ai_analysis_enabled FROM anomaly_rules WHERE id = ?`, rule.ID).Scan(&enabled, &aiEnabled); err != nil {
				t.Fatal(err)
			}
			if (mode == "analysis" && (enabled != 1 || aiEnabled != 0)) || (mode == "rule" && (enabled != 0 || aiEnabled != 1)) {
				t.Fatalf("disable did not commit: enabled=%d AI=%d", enabled, aiEnabled)
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("committed disable left analyzer running after read failure")
			}
			if receiveAnalysis(t, events).Analysis.Status != AnalysisUnavailable {
				t.Fatal("canceled analysis was not terminal")
			}
		})
	}
}

func TestAnalysisPersistenceFailureRecoversLocallyBeforeNotification(t *testing.T) {
	storetest.SkipIfMySQL(t)
	db := testDB(t)
	if _, err := db.Exec(`CREATE TRIGGER fail_terminal_analysis BEFORE UPDATE OF analysis_status ON anomaly_alerts
		WHEN NEW.analysis_status != 'pending' BEGIN SELECT RAISE(FAIL, 'terminal write failed'); END`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	events := make(channelNotifier, 3)
	analyzer := analyzerFunc(func(context.Context, AnalysisInput) (*Analysis, error) {
		calls.Add(1)
		return readyAnalysis(), nil
	})
	s := New(db, memoryCollector("server"), WithNotifier(events), WithAnalyzer(analyzer)).(*service)
	defer s.Close()
	newAnalysisRule(t, s, boolPtr(true))
	alerts, err := s.Check(context.Background())
	if err != nil || len(alerts) != 1 {
		t.Fatalf("check: %+v %v", alerts, err)
	}
	s.analysisWG.Wait()
	select {
	case <-events:
		t.Fatal("notification preceded successful terminal persistence")
	case <-time.After(2 * analysisRecoveryInterval):
	}
	stored, err := s.ListAlerts(context.Background(), "", 10)
	if err != nil || len(stored) != 1 || stored[0].Analysis.Status != AnalysisPending {
		t.Fatalf("failed database did not retain original pending alert: %+v %v", stored, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_terminal_analysis`); err != nil {
		t.Fatal(err)
	}
	notified := receiveAnalysis(t, events)
	if notified.ID != alerts[0].ID || notified.Message != alerts[0].Message || notified.Analysis.Status != AnalysisUnavailable {
		t.Fatalf("local recovery lost original alert/unavailable result: %+v", notified)
	}
	stored, err = s.ListAlerts(context.Background(), "", 10)
	if err != nil || stored[0].Analysis.Status != AnalysisUnavailable {
		t.Fatalf("terminal state not saved before notification: %+v %v", stored, err)
	}
	s.Close()
	restarted := New(db, nil, WithNotifier(events), WithAnalyzer(analyzer))
	restarted.Close()
	select {
	case <-events:
		t.Fatal("restart repeated locally recovered notification")
	default:
	}
	if calls.Load() != 1 {
		t.Fatalf("persistence recovery retried model: calls=%d", calls.Load())
	}
}

func TestAnalysisRecoverySkipsActiveJobs(t *testing.T) {
	db := testDB(t)
	started := make(chan struct{})
	gate := make(chan struct{})
	events := make(channelNotifier, 3)
	var calls atomic.Int32
	s := New(db, memoryCollector("active"), WithNotifier(events), WithAnalyzer(analyzerFunc(func(ctx context.Context, _ AnalysisInput) (*Analysis, error) {
		calls.Add(1)
		close(started)
		select {
		case <-gate:
			return readyAnalysis(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))).(*service)
	defer s.Close()
	rule := newAnalysisRule(t, s, boolPtr(true))
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("analysis not started")
	}
	orphan, err := s.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: "orphan"}, 95)
	if err != nil {
		t.Fatal(err)
	}
	s.scheduleAnalysisRecovery()
	if a := receiveAnalysis(t, events); a.ID != orphan.ID || a.Analysis.Status != AnalysisUnavailable {
		t.Fatalf("wrong recovered alert: %+v", a)
	}
	stored, err := s.ListAlerts(context.Background(), "active", 10)
	if err != nil || len(stored) != 1 || stored[0].Analysis.Status != AnalysisPending {
		t.Fatalf("active analysis was recovered prematurely: %+v %v", stored, err)
	}
	close(gate)
	if receiveAnalysis(t, events).Analysis.Status != AnalysisReady {
		t.Fatal("recovery replaced active model result")
	}
	s.Close()
	if calls.Load() != 1 || len(events) != 0 {
		t.Fatalf("duplicate model/notification: calls=%d events=%d", calls.Load(), len(events))
	}
}

func TestAnalysisConstructorSchedulesFailedRecovery(t *testing.T) {
	storetest.SkipIfMySQL(t)
	db := testDB(t)
	old := New(db, nil).(*service)
	rule := newAnalysisRule(t, old, boolPtr(true))
	if _, err := old.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: "server"}, 95); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_terminal_analysis BEFORE UPDATE OF analysis_status ON anomaly_alerts
		BEGIN SELECT RAISE(FAIL, 'terminal write failed'); END`); err != nil {
		t.Fatal(err)
	}
	events := make(channelNotifier, 2)
	s := New(db, nil, WithNotifier(events)).(*service)
	defer s.Close()
	if len(events) != 0 {
		t.Fatal("constructor notified despite failed terminal write")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_terminal_analysis`); err != nil {
		t.Fatal(err)
	}
	if receiveAnalysis(t, events).Analysis.Status != AnalysisUnavailable {
		t.Fatal("constructor did not schedule remaining pending recovery")
	}
}

type drainingNotifier struct {
	channelNotifier
	drain func(context.Context) error
}

func (n drainingNotifier) Drain(ctx context.Context) error { return n.drain(ctx) }

func TestAnalysisCloseDrainsNotifierAfterWorkers(t *testing.T) {
	started := make(chan struct{})
	drainStarted := make(chan struct{})
	gate := make(chan struct{})
	events := make(channelNotifier, 2)
	var s *service
	notifier := drainingNotifier{channelNotifier: events, drain: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > notifierDrainTimeout {
			t.Error("notifier drain has no bounded live context")
		}
		s.analysisMu.Lock()
		if len(s.analysisJobs) != 0 || s.recoveryRunning {
			t.Error("drain started before analysis/recovery stopped")
		}
		s.analysisMu.Unlock()
		if len(events) != 1 {
			t.Error("drain started before final analysis notification")
		}
		close(drainStarted)
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	s = New(testDB(t), memoryCollector("server"), WithNotifier(notifier), WithAnalyzer(analyzerFunc(func(ctx context.Context, _ AnalysisInput) (*Analysis, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}))).(*service)
	newAnalysisRule(t, s, boolPtr(true))
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		s.Close()
		t.Fatal("analysis not started")
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-drainStarted:
	case <-time.After(time.Second):
		close(gate)
		<-closed
		t.Fatal("Close did not drain notifier")
	}
	select {
	case <-closed:
		t.Error("Close returned before notifier drained")
	default:
	}
	close(gate)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after notifier drained")
	}
}

func TestAnalysisRecoveryScheduleRacesClose(t *testing.T) {
	s := New(testDB(t), nil).(*service)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.scheduleAnalysisRecovery()
			}
		}()
	}
	s.Close()
	wg.Wait()
	s.analysisMu.Lock()
	defer s.analysisMu.Unlock()
	if s.recoveryRunning {
		t.Fatal("recovery survived Close")
	}
}

type notifierFunc func(context.Context, *Alert)

func (f notifierFunc) NotifyAlert(ctx context.Context, a *Alert) { f(ctx, a) }

func TestAnalysisRecoveryDoesNotLoseScheduleDuringSweep(t *testing.T) {
	db := testDB(t)
	events := make(channelNotifier, 3)
	var s *service
	var rule *Rule
	notifier := notifierFunc(func(_ context.Context, a *Alert) {
		if a.ID == "z-first" {
			late, err := s.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: "late"}, 95)
			if err != nil {
				t.Error(err)
				return
			}
			// This pending row falls behind the current sweep's keyset cursor.
			if _, err := db.Exec(`UPDATE anomaly_alerts SET id = 'a-late' WHERE id = ?`, late.ID); err != nil {
				t.Error(err)
				return
			}
			s.scheduleAnalysisRecovery()
		}
		events <- *a
	})
	s = New(db, nil, WithNotifier(notifier)).(*service)
	defer s.Close()
	rule = newAnalysisRule(t, s, boolPtr(true))
	first, err := s.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: "first"}, 95)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE anomaly_alerts SET id = 'z-first' WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	s.scheduleAnalysisRecovery()
	if a := receiveAnalysis(t, events); a.ID != "z-first" || a.Analysis.Status != AnalysisUnavailable {
		t.Fatalf("first recovery: %+v", a)
	}
	if a := receiveAnalysis(t, events); a.ID != "a-late" || a.Analysis.Status != AnalysisUnavailable {
		t.Fatalf("schedule during sweep lost: %+v", a)
	}
	s.Close()
	if len(events) != 0 {
		t.Fatal("duplicate recovery notification")
	}
}

func TestAnalysisCloseCancelsBlockedRecoveryAndRetainsPending(t *testing.T) {
	storetest.SkipIfMySQL(t)
	db := testDB(t)
	db.SetMaxOpenConns(1)
	events := make(channelNotifier, 2)
	var calls atomic.Int32
	analyzer := analyzerFunc(func(context.Context, AnalysisInput) (*Analysis, error) {
		calls.Add(1)
		return readyAnalysis(), nil
	})
	s := New(db, nil, WithNotifier(events), WithAnalyzer(analyzer)).(*service)
	defer s.Close()
	rule := newAnalysisRule(t, s, boolPtr(true))
	if _, err := s.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: "pending"}, 95); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waitCount := db.Stats().WaitCount
	s.scheduleAnalysisRecovery()
	deadline := time.Now().Add(time.Second)
	for db.Stats().WaitCount == waitCount {
		if time.Now().After(deadline) {
			t.Fatal("recovery did not wait on occupied database")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel and drain blocked recovery")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM anomaly_alerts WHERE analysis_status = 'pending'`).Scan(&pending); err != nil || pending != 1 || len(events) != 0 {
		t.Fatalf("blocked persistence did not retain pending: pending=%d notifications=%d err=%v", pending, len(events), err)
	}
	restarted := New(db, nil, WithNotifier(events), WithAnalyzer(analyzer))
	restarted.Close()
	if a := receiveAnalysis(t, events); a.Analysis.Status != AnalysisUnavailable || calls.Load() != 0 {
		t.Fatalf("restart lost pending or retried model: alert=%+v calls=%d", a, calls.Load())
	}
}

func TestAnalysisRestartDoesNotRepeatModelCall(t *testing.T) {
	db := testDB(t)
	old := New(db, nil).(*service)
	rule := newAnalysisRule(t, old, boolPtr(true))
	_, err := old.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: "server", ServerName: "server"}, 95)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	var calls atomic.Int32
	events := make(channelNotifier, 2)
	s := New(db, nil, WithNotifier(events), WithAnalyzer(analyzerFunc(func(context.Context, AnalysisInput) (*Analysis, error) { calls.Add(1); return readyAnalysis(), nil })))
	defer s.Close()
	a := receiveAnalysis(t, events)
	if a.Analysis.Status != AnalysisUnavailable || calls.Load() != 0 {
		t.Fatal("restart repeated a paid analysis")
	}
	stored, _ := s.ListAlerts(context.Background(), "", 10)
	if stored[0].Analysis.Status == AnalysisPending {
		t.Fatal("restart left an infinite pending state")
	}
}

func TestAnalysisRestartRecoversAllPendingAlerts(t *testing.T) {
	db := testDB(t)
	old := New(db, nil).(*service)
	rule := newAnalysisRule(t, old, boolPtr(true))
	for i := 0; i < 40; i++ {
		if _, err := old.recordAlert(context.Background(), rule, ServerMetricsSnapshot{ServerID: fmt.Sprint(i)}, 95); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	events := make(channelNotifier, 40)
	s := New(db, nil, WithNotifier(events))
	defer s.Close()
	for i := 0; i < 40; i++ {
		if receiveAnalysis(t, events).Analysis.Status != AnalysisUnavailable {
			t.Fatal("pending survived recovery")
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM anomaly_alerts WHERE analysis_status='pending'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("remaining pending=%d error=%v", count, err)
	}
}

func TestDecodeAnalysisRejectsUnknownStatusAndBoundsOldData(t *testing.T) {
	if got := decodeAnalysis("invalid", `{"hypothesis":"untrusted"}`); got.Status != AnalysisUnavailable {
		t.Fatal("invalid stored status")
	}
	if got := decodeAnalysis(AnalysisReady, `{"hypothesis":"","confidence":"high"}`); got.Status != AnalysisUnavailable {
		t.Fatal("empty stored conclusion accepted")
	}
	got := decodeAnalysis(AnalysisReady, `{"hypothesis":"known","generatedAt":"2026-01-01T00:00:00Z"}`)
	if got.GeneratedAt != "2026-01-01T00:00:00Z" {
		t.Fatal("read changed analysis timestamp")
	}
}
