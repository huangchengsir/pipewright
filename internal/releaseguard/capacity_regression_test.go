package releaseguard

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
)

type capacityLogObserver struct {
	Observer
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	text    string
}

func (o *capacityLogObserver) Logs(ctx context.Context, _ target.ConnectionSnapshot, _ string, _, _ time.Time) (string, bool, string) {
	o.calls.Add(1)
	if o.entered != nil {
		select {
		case o.entered <- struct{}{}:
		default:
		}
		select {
		case <-o.release:
		case <-ctx.Done():
			return "", false, "logs_unavailable"
		}
	}
	if o.text != "" {
		return o.text, false, "fixture_logs"
	}
	return "bounded fixture log", false, "fixture_logs"
}

// Attached consent creates a real persisted guard without launching a worker;
// bind its existing ledger target so boundary scheduling is deterministic.
func capacityFixture(t *testing.T) (serviceIntegrationFixture, string, *capacityLogObserver) {
	t.Helper()
	f := newServiceIntegrationFixture(t)
	ctx := context.Background()
	p, err := f.service.Prepare(ctx, "p", PrepareInput{Mode: "attached", RunID: "r", ArtifactID: f.artifactID, ServerIDs: []string{"s"}, DurationSeconds: 900, IncludeLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := f.service.Attached(ctx, "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "capacity"}, "r", f.artifactID, []string{"s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	d, err := f.service.load(ctx, "p", report.ID)
	if err != nil {
		f.service.mu.Unlock()
		t.Fatal(err)
	}
	d.Report.AttemptID = f.attemptID
	d.Report.Targets[0].ID = f.targetID
	d.Report.Targets[0].ContainerID = strings.Repeat("1", 64)
	d.Private.Bindings[f.targetID] = d.Private.Bindings["s"]
	delete(d.Private.Bindings, "s")
	d.Private.LastLogs[f.targetID] = f.clock.now()
	err = f.service.save(ctx, d, "fixture_bound", nil)
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	o := &capacityLogObserver{Observer: f.service.observer}
	f.service.observer = o
	return f, report.ID, o
}

func setCapacityBytes(t *testing.T, f serviceIntegrationFixture, id string, n int) {
	t.Helper()
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	d, err := f.service.load(context.Background(), "p", id)
	if err != nil {
		t.Fatal(err)
	}
	d.Private.EvidenceBytes = n
	if err = f.service.save(context.Background(), d, "fixture_budget", nil); err != nil {
		t.Fatal(err)
	}
}

func TestLogCapacityStopsBeforeRemoteDispatch(t *testing.T) {
	f, id, observer := capacityFixture(t)
	setCapacityBytes(t, f, id, maxLogEvidenceBytes)
	for i := 0; i < 4; i++ {
		f.clock.add(time.Minute)
		f.service.collectLogs(context.Background(), "p", id, f.targetID)
	}
	if got := observer.calls.Load(); got != 0 {
		t.Fatalf("budget exhausted but dispatched %d log reads", got)
	}
	report, err := f.service.Snapshot(context.Background(), "p", id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Targets[0].Limitations, ","), "logs_capacity_reached") {
		t.Fatal("missing persisted log capacity gap")
	}
}

func TestDiscardedLogReplyStillReservesItsSamplingWindow(t *testing.T) {
	f, id, observer := capacityFixture(t)
	observer.entered, observer.release = make(chan struct{}, 1), make(chan struct{})
	f.clock.add(time.Minute)
	done := make(chan struct{})
	go func() { defer close(done); f.service.collectLogs(context.Background(), "p", id, f.targetID) }()
	select {
	case <-observer.entered:
	case <-time.After(time.Second):
		t.Fatal("log read not dispatched")
	}
	setCapacityBytes(t, f, id, maxLogEvidenceBytes)
	close(observer.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("log read did not finish")
	}
	// Restore headroom to distinguish the frequency fence from the budget fence.
	setCapacityBytes(t, f, id, 0)
	f.clock.add(30 * time.Second)
	f.service.collectLogs(context.Background(), "p", id, f.targetID)
	if got := observer.calls.Load(); got != 1 {
		t.Fatalf("discard retried before one minute: %d reads", got)
	}
	f.clock.add(30 * time.Second)
	f.service.collectLogs(context.Background(), "p", id, f.targetID)
	if got := observer.calls.Load(); got != 2 {
		t.Fatalf("next authorized window not collected: %d reads", got)
	}
}

func TestLogTargetCapacityMarksExactBoundaryAndStopsReads(t *testing.T) {
	f, id, observer := capacityFixture(t)
	observer.text = strings.Repeat("a", 32<<10)
	f.service.mu.Lock()
	d, err := f.service.load(context.Background(), "p", id)
	if err != nil {
		f.service.mu.Unlock()
		t.Fatal(err)
	}
	d.Private.LogBytes[f.targetID] = (256 - 32) << 10
	err = f.service.save(context.Background(), d, "fixture_log_budget", nil)
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.clock.add(time.Minute)
	f.service.collectLogs(context.Background(), "p", id, f.targetID)
	page, err := f.service.Evidence(context.Background(), "p", id, 0, 100)
	if err != nil || len(page.Items) != 1 || !page.Items[0].Truncated {
		t.Fatalf("exact byte boundary was not marked truncated: %+v / %v", page, err)
	}
	f.clock.add(time.Minute)
	f.service.collectLogs(context.Background(), "p", id, f.targetID)
	if observer.calls.Load() != 1 {
		t.Fatal("target byte budget dispatched more logs")
	}
}

func TestLogEvidenceBoundsSerializedExpansionWithoutLosingWindow(t *testing.T) {
	for name, raw := range map[string]string{
		"html":    strings.Repeat("<>&", 10922),
		"control": strings.Repeat("\x00\x01\n", 10922),
		"utf8":    strings.Repeat("界", 4095) + strings.Repeat("<", 20480),
	} {
		t.Run(name, func(t *testing.T) {
			f, id, observer := capacityFixture(t)
			f.service.masker = mask.NewMasker()
			f.service.masker.RegisterSecret("fixture-sensitive-value")
			observer.text = raw[:len(raw)-32] + "fixture-sensitive-value"
			if len(observer.text) > 32<<10 {
				t.Fatal("fixture exceeds legal raw log size")
			}
			f.clock.add(time.Minute)
			f.service.collectLogs(context.Background(), "p", id, f.targetID)
			page, err := f.service.Evidence(context.Background(), "p", id, 0, 100)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("expanded log silently lost: %+v / %v", page, err)
			}
			e := page.Items[0]
			encoded, err := json.Marshal(e)
			if err != nil || len(encoded) > maxEvidenceSize || !e.Truncated {
				t.Fatalf("invalid encoded log boundary: %d bytes, truncated=%v / %v", len(encoded), e.Truncated, err)
			}
			value, ok := e.Value.(map[string]any)
			text, textOK := value["text"].(string)
			if !ok || !textOK || text == "" || !utf8.ValidString(text) || strings.Contains(text, "\uFFFD") || strings.Contains(text, "fixture-sensitive") {
				t.Fatalf("invalid or unmasked clipped log: %q", text)
			}
			f.clock.add(30 * time.Second)
			f.service.collectLogs(context.Background(), "p", id, f.targetID)
			if observer.calls.Load() != 1 {
				t.Fatal("clipped reply retried inside reserved minute")
			}
			f.clock.add(30 * time.Second)
			f.service.collectLogs(context.Background(), "p", id, f.targetID)
			if observer.calls.Load() != 2 {
				t.Fatal("clipped reply suppressed the next authorized minute")
			}
		})
	}
}

func TestRuntimeCapacityReservesRealHardFailureEvidence(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_sample_preserves_reserve", true: "failure_uses_reserve"}[failed], func(t *testing.T) {
			f, id, _ := capacityFixture(t)
			setCapacityBytes(t, f, id, regularEvidenceBytes-32)
			f.observer.exited.Store(failed)
			f.service.sample(context.Background(), "p", id, f.targetID)
			report, err := f.service.Snapshot(context.Background(), "p", id)
			if err != nil {
				t.Fatal(err)
			}
			if report.ExecutionStatus != "tool_error" || report.Reason != "evidence_capacity_reached" {
				t.Fatalf("capacity did not stop collection: %+v", report)
			}
			page, err := f.service.Evidence(context.Background(), "p", id, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if !failed {
				if report.VerificationResult == nil || *report.VerificationResult != "insufficient" || len(page.Items) != 0 {
					t.Fatalf("ordinary sample consumed reserved space: %+v / %+v", report, page)
				}
				return
			}
			if report.VerificationResult == nil || *report.VerificationResult != "failed" || len(page.Items) != 1 {
				t.Fatalf("hard failure lost at capacity: %+v / %+v", report, page)
			}
			evidence := page.Items[0]
			body, _ := json.Marshal(evidence.Value)
			var actual RuntimeSnapshot
			if json.Unmarshal(body, &actual) != nil || actual.Running || actual.ContainerID != strings.Repeat("1", 64) {
				t.Fatalf("failure proof was fabricated or missing: %s", body)
			}
			cited := false
			for _, c := range report.Targets[0].Conditions {
				if c.Name == "liveness" && c.Result == "failed" {
					for _, eid := range c.EvidenceIDs {
						cited = cited || eid == evidence.ID
					}
				}
			}
			if !cited || evidence.ID == "" {
				t.Fatal("failure citation does not resolve to persisted evidence")
			}
			d, err := f.service.load(context.Background(), "p", id)
			if err != nil || d.Private.EvidenceBytes > maxEvidenceBytes || d.Private.EvidenceBytes < regularEvidenceBytes {
				t.Fatalf("failure reserve escaped bounded capacity: %+v / %v", d, err)
			}
		})
	}
}
