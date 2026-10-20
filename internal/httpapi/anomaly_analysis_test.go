package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/metrics"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type monitoringModel struct {
	ai.Service
	enabled bool
	calls   int
	input   ai.DiagnoseInput
	result  *ai.Diagnosis
}

func (m *monitoringModel) Get(context.Context) (*ai.Config, error) {
	return &ai.Config{Configured: true, Enabled: m.enabled}, nil
}
func (m *monitoringModel) Diagnose(_ context.Context, in ai.DiagnoseInput) (*ai.Diagnosis, error) {
	m.calls++
	m.input = in
	return m.result, nil
}

type monitoringExecutor struct {
	target.Service
	captures              int
	argv                  [][]string
	fail                  bool
	processes, containers string
}

func (e *monitoringExecutor) Capture(context.Context, string) (target.ConnectionSnapshot, error) {
	e.captures++
	return target.ConnectionSnapshot{Server: target.Server{ID: "private-server", Name: "private-name", Host: "192.0.2.2", CredentialID: "private-credential"}}, nil
}
func (e *monitoringExecutor) ExecLimited(ctx context.Context, _ target.ConnectionSnapshot, argv []string, limits target.ExecutionLimits) (*target.LimitedResult, error) {
	e.argv = append(e.argv, append([]string{}, argv...))
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 4*time.Second || limits.Bytes != 8192 || limits.Lines != 26 {
		return nil, target.ErrLimitedUnavailable
	}
	if e.fail {
		return &target.LimitedResult{ExitCode: 1, Stderr: "internal path and secrets"}, nil
	}
	if argv[0] == "env" {
		return &target.LimitedResult{Stdout: e.processes}, nil
	}
	return &target.LimitedResult{Stdout: e.containers, Truncated: true}, nil
}

type monitoringHistory struct {
	metrics.Service
	points []metrics.Point
}

func (h monitoringHistory) QueryRange(context.Context, string, time.Time) ([]metrics.Point, error) {
	return append([]metrics.Point{}, h.points...), nil
}

func monitoringInput() anomaly.AnalysisInput {
	v := 94.0
	return anomaly.AnalysisInput{Alert: anomaly.Alert{ServerID: "private-server", ServerName: "private-name", Metric: anomaly.MetricMemory, Operator: "gt", Threshold: 80, Value: 94, CreatedAt: time.Now().UTC()}, Metrics: anomaly.ServerMetricsSnapshot{MemoryPercent: &v}}
}

func TestMonitoringProjectionAndSecrets(t *testing.T) {
	db := storetest.OpenDB(t)
	v := vault.New(db, &[32]byte{1})
	secret := "private-test-secret-778899"
	if _, err := v.Create(vault.CreateInput{Name: "test", Type: vault.TypeGitToken, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	m := &monitoringModel{enabled: true, result: &ai.Diagnosis{Status: "ready", Hypothesis: "Check " + secret, Confidence: "low", FixSuggestions: []string{"Check " + secret}, FixScript: "rm -rf /", Evidence: []ai.DiagnosisEvidence{{Line: 3, Text: "model invented evidence"}, {Line: 99, Text: "invalid"}}}}
	e := &monitoringExecutor{processes: "1337 " + secret + " 31 10\n9999 worker --password=" + secret + " 40 10\n666 bad NaN 2\n", containers: `{"name":"` + secret + `","cpu":"12%","memory":"10 MiB / 1 GiB","env":"TOKEN=` + secret + `"}`}
	a := NewAnomalyAnalyzer(m, e, v, nil, mask.NewMasker())
	out, err := a.AnalyzeAlert(context.Background(), monitoringInput())
	if err != nil || out.Status != "ready" || m.calls != 1 || !m.input.Monitoring {
		t.Fatalf("analysis: %+v %v", out, err)
	}
	for _, private := range []string{secret, "1337", "9999", "--password", "private-name", "private-server", "192.0.2.2", "private-credential", "TOKEN="} {
		if strings.Contains(m.input.FailureLog, private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
	if !strings.Contains(m.input.FailureLog, `"truncated":true`) || !strings.Contains(m.input.FailureLog, "[MASKED]") {
		t.Fatal("missing bounded-data markers")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "rm -rf") || strings.Contains(string(raw), "model invented") {
		t.Fatal("unsafe model output")
	}
	if len(out.Evidence) != 1 || !strings.Contains(out.Evidence[0].Text, `"kind":"processes"`) {
		t.Fatalf("evidence is not from local facts: %+v", out.Evidence)
	}
	if e.captures != 1 || len(e.argv) != 2 || !reflect.DeepEqual(e.argv[0], []string{"env", "LC_ALL=C", "ps", "-eo", "pid,comm,pcpu,pmem", "--no-headers", "--sort=-pmem"}) || !reflect.DeepEqual(e.argv[1], []string{"docker", "stats", "--no-stream", "--format", `{"name":{{json .Name}},"cpu":{{json .CPUPerc}},"memory":{{json .MemUsage}}}`}) {
		t.Fatalf("unexpected commands: %+v", e.argv)
	}
}

func TestMonitoringDisabledAvoidsCollectionAndModel(t *testing.T) {
	m := &monitoringModel{}
	e := &monitoringExecutor{}
	a := NewAnomalyAnalyzer(m, e, nil, nil, nil)
	out, err := a.AnalyzeAlert(context.Background(), monitoringInput())
	if err != nil || out.Status != "unavailable" || e.captures != 0 || m.calls != 0 {
		t.Fatalf("disabled analysis performed work: %+v", out)
	}
}

func TestMonitoringVaultReadHonorsAnalysisCancellation(t *testing.T) {
	db := storetest.OpenDB(t)
	db.SetMaxOpenConns(1)
	v := vault.New(db, &[32]byte{1})
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m := &monitoringModel{enabled: true}
	e := &monitoringExecutor{}
	a := NewAnomalyAnalyzer(m, e, v, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	out, err := a.AnalyzeAlert(ctx, monitoringInput())
	if err != nil || out.Status != "unavailable" || m.calls != 0 || e.captures != 0 || time.Since(started) > time.Second {
		t.Fatal("cancelled vault registration escaped bounds", err)
	}
}

func TestMonitoringMissingCollectionStillAnalyzesKnownMetrics(t *testing.T) {
	m := &monitoringModel{enabled: true, result: &ai.Diagnosis{Status: "ready", Hypothesis: "Only metric evidence is available"}}
	e := &monitoringExecutor{fail: true}
	a := NewAnomalyAnalyzer(m, e, vault.New(storetest.OpenDB(t), &[32]byte{1}), nil, nil)
	out, err := a.AnalyzeAlert(context.Background(), monitoringInput())
	if err != nil || out.Status != "ready" || !strings.Contains(m.input.FailureLog, `"unavailable":true`) || strings.Contains(m.input.FailureLog, "internal path") {
		t.Fatalf("missing data was mishandled: %+v %v", out, err)
	}
}

func TestMonitoringHistoryExcludesFuturePointsBeforeSampling(t *testing.T) {
	in := monitoringInput()
	points := []metrics.Point{{At: in.Alert.CreatedAt.Add(-time.Minute)}}
	for i := 1; i <= 20; i++ {
		points = append(points, metrics.Point{At: in.Alert.CreatedAt.Add(time.Duration(i) * time.Minute)})
	}
	m := &monitoringModel{enabled: true, result: &ai.Diagnosis{Status: "ready", Hypothesis: "Historical metric"}}
	a := NewAnomalyAnalyzer(m, nil, vault.New(storetest.OpenDB(t), &[32]byte{1}), monitoringHistory{points: points}, nil)
	_, err := a.AnalyzeAlert(context.Background(), in)
	if err != nil || strings.Count(m.input.FailureLog, `"kind":"history"`) != 1 {
		t.Fatalf("history filter: %v %s", err, m.input.FailureLog)
	}
}

func TestMonitoringProcessParserBounds(t *testing.T) {
	items, truncated := analysisProcesses(strings.Repeat("2 work 100 10\n", 25)+"3 invalid -1 2\n", mask.NewMasker())
	if len(items) != 20 || !truncated {
		t.Fatal("unbounded processes")
	}
	items, truncated = analysisProcesses("2 work -1 10\n3 work 1 101", mask.NewMasker())
	if len(items) != 0 || !truncated {
		t.Fatal("invalid resource percentages accepted")
	}
}

type monitoringCollector struct{}

func (monitoringCollector) Collect(context.Context, []string) ([]anomaly.ServerMetricsSnapshot, error) {
	v := 94.0
	return []anomaly.ServerMetricsSnapshot{{ServerID: "private-server", ServerName: "acceptance server", Available: true, MemoryPercent: &v}}, nil
}

func TestMonitoringAlertToWebhookOnce(t *testing.T) {
	for _, status := range []string{"ready", "unavailable"} {
		t.Run(status, func(t *testing.T) {
			bodies := make(chan string, 5)
			hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				bodies <- string(b)
				w.WriteHeader(200)
			}))
			defer hook.Close()
			db := storetest.OpenDB(t)
			v := vault.New(db, &[32]byte{1})
			n := notify.New(db, v, hook.Client())
			ch, err := n.Create(context.Background(), notify.CreateInput{Name: "local", Type: notify.TypeWebhook, Enabled: true, Config: notify.Config{URL: hook.URL}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = n.CreateRoute(context.Background(), notify.CreateRouteInput{Event: notify.EventAnomalyDetected, ChannelID: ch.ID, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			m := &monitoringModel{enabled: true, result: &ai.Diagnosis{Status: status, Hypothesis: "Known metric exceeds threshold", Reason: "Model unavailable", FixSuggestions: []string{"Inspect workload"}}}
			e := &monitoringExecutor{processes: "1 work 10 20", containers: `{"name":"work","cpu":"10%","memory":"10 MiB / 1 GiB"}`}
			s := anomaly.New(db, monitoringCollector{}, anomaly.WithNotifier(NewAnomalyNotifier(n)), anomaly.WithAnalyzer(NewAnomalyAnalyzer(m, e, v, nil, nil)))
			defer s.Close()
			enabled := true
			if _, err = s.CreateRule(context.Background(), anomaly.CreateRuleInput{Metric: anomaly.MetricMemory, Operator: "gt", Threshold: 80, Enabled: true, AIAnalysisEnabled: &enabled}); err != nil {
				t.Fatal(err)
			}
			alerts, err := s.Check(context.Background())
			if err != nil || len(alerts) != 1 {
				t.Fatalf("check: %v %v", alerts, err)
			}
			var raw string
			select {
			case raw = <-bodies:
			case <-time.After(3 * time.Second):
				t.Fatal("notification missing")
			}
			var payload struct {
				Body   string            `json:"body"`
				Fields map[string]string `json:"fields"`
			}
			if err = json.Unmarshal([]byte(raw), &payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Body, alerts[0].Message) || !strings.Contains(payload.Body, "AI") || payload.Fields["aiAnalysis"] == "" {
				t.Fatalf("original or analysis missing: %s", raw)
			}
			if status == "ready" && !strings.Contains(payload.Body, m.result.Hypothesis) {
				t.Fatal("reason missing")
			}
			if duplicate, err := s.Check(context.Background()); err != nil || len(duplicate) != 0 {
				t.Fatal("cooldown ignored")
			}
			stored, err := s.ListAlerts(context.Background(), "", 10)
			if err != nil || len(stored) != 1 || stored[0].Analysis.Status != status || m.calls != 1 {
				t.Fatalf("persistence/model calls wrong: %v %v", stored, err)
			}
			select {
			case extra := <-bodies:
				t.Fatalf("duplicate notification: %s", extra)
			case <-time.After(30 * time.Millisecond):
			}
		})
	}
}
