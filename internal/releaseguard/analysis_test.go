package releaseguard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
)

type guardModelFixture struct {
	info        ModelInfo
	calls       atomic.Int64
	text        string
	err         error
	entered     chan struct{}
	seenPayload string
}

func (m *guardModelFixture) Info(context.Context) (ModelInfo, error) { return m.info, nil }
func (m *guardModelFixture) Explain(ctx context.Context, p ModelInfo, payload string) (string, error) {
	m.calls.Add(1)
	m.seenPayload = payload
	if m.entered != nil {
		close(m.entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return m.text, m.err
}

func analysisFixture(t *testing.T) (serviceIntegrationFixture, *Report, *guardModelFixture) {
	t.Helper()
	f := newServiceIntegrationFixture(t)
	m := &guardModelFixture{info: ModelInfo{Name: "fixture", Model: "test", Destination: "http://fixture.invalid", ConfigHash: strings.Repeat("e", 64)}}
	f.service.model = m
	f.service.masker = mask.NewMasker()
	f.service.masker.RegisterSecret("registered-fixture-secret")
	r := f.start(t, "analysis-fixture")
	f.wait(t, r.ID, func(r *Report) bool { return len(r.Targets[0].Conditions) > 0 })
	if _, e := f.service.Stop(context.Background(), "p", r.ID); e != nil {
		t.Fatal(e)
	}
	r = f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
	return f, r, m
}

func TestAnalysisExplicitScopeProviderSnapshotAndExactlyOnce(t *testing.T) {
	f, r, m := analysisFixture(t)
	ctx := context.Background()
	if _, e := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{}); e != ErrInvalid {
		t.Fatal(e)
	}
	f.service.mu.Lock()
	d, _ := f.service.load(ctx, "p", r.ID)
	e := Evidence{ID: newEvidenceID(), TargetID: f.targetID, Kind: "logs", EventAt: f.clock.now(), CollectedAt: f.clock.now(), Summary: "untrusted_windowed_logs", Value: map[string]any{"text": "token=unregistered-fixture-secret registered-fixture-secret", "from": f.clock.now().Add(-time.Minute), "until": f.clock.now()}}
	if err := f.service.save(ctx, d, "test_fixture_logs", &e); err != nil {
		f.service.mu.Unlock()
		t.Fatal(err)
	}
	f.service.mu.Unlock()
	a, err := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != 0 || strings.Contains(a.Payload, "fixture-secret") || strings.Contains(a.Payload, "connectionFingerprint") || strings.Contains(a.Payload, `"kind":"logs"`) {
		t.Fatal("preview sent or exceeded scope", a.Payload)
	}
	bad := SendAnalysisInput{PayloadHash: a.PayloadHash, ProviderConfigHash: a.Provider.ConfigHash}
	if _, err = f.service.SendAnalysis(ctx, "p", r.ID, a.ID, bad); err != ErrInvalid || m.calls.Load() != 0 {
		t.Fatal(err, m.calls.Load())
	}
	bad.Confirmed = true
	m.info.ConfigHash = strings.Repeat("f", 64)
	if _, err = f.service.SendAnalysis(ctx, "p", r.ID, a.ID, bad); err != ErrConflict || m.calls.Load() != 0 {
		t.Fatal(err, m.calls.Load())
	}
	m.info = a.Provider
	m.text = `{"summary":"Candidate explanation only","candidates":[{"id":"c1","title":"Needs investigation","supportingEvidenceIds":["` + a.EvidenceIDs[0] + `"],"counterEvidenceIds":[],"missingEvidence":["causation"],"nextReadOnlyCheck":"Review the recorded evidence"}]}`
	got, err := f.service.SendAnalysis(ctx, "p", r.ID, a.ID, bad)
	if err != nil || got.Status != "completed" || m.calls.Load() != 1 || m.seenPayload != a.Payload {
		t.Fatal(got, err, m.calls.Load())
	}
	if again, e := f.service.SendAnalysis(ctx, "p", r.ID, a.ID, bad); e != nil || again.ID != a.ID || m.calls.Load() != 1 {
		t.Fatal(again, e, m.calls.Load())
	}
	latest, _ := f.service.Snapshot(ctx, "p", r.ID)
	if *latest.VerificationResult != *r.VerificationResult {
		t.Fatal("model overwrote rule result")
	}
	logs, e2 := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeLogs: true})
	if e2 != nil {
		t.Fatal(e2)
	}
	if strings.Contains(logs.Payload, "unregistered-fixture-secret") || strings.Contains(logs.Payload, "registered-fixture-secret") || strings.Contains(logs.Payload, `"kind":"runtime"`) {
		t.Fatal("unsafe logs payload", logs.Payload)
	}
	if _, err = f.service.RevokeAnalysis(ctx, "p", r.ID, logs.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.SendAnalysis(ctx, "p", r.ID, logs.ID, SendAnalysisInput{Confirmed: true, PayloadHash: logs.PayloadHash, ProviderConfigHash: logs.Provider.ConfigHash}); err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != 1 {
		t.Fatal("revoked preview sent")
	}
}

func TestAnalysisUnknownAndRevocationNeverResend(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "revoke_inflight"}[inFlight], func(t *testing.T) {
			f, r, m := analysisFixture(t)
			ctx := context.Background()
			a, err := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeRuntime: true})
			if err != nil {
				t.Fatal(err)
			}
			in := SendAnalysisInput{Confirmed: true, PayloadHash: a.PayloadHash, ProviderConfigHash: a.Provider.ConfigHash}
			if inFlight {
				m.entered = make(chan struct{})
				done := make(chan *Analysis, 1)
				go func() {
					result, e := f.service.SendAnalysis(ctx, "p", r.ID, a.ID, in)
					if e != nil {
						done <- nil
					} else {
						done <- result
					}
				}()
				select {
				case <-m.entered:
				case <-time.After(time.Second):
					t.Fatal("model not entered")
				}
				if _, err = f.service.RevokeAnalysis(ctx, "p", r.ID, a.ID); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-done:
					if got == nil || got.Status != "unknown" || !got.Revoked {
						t.Fatal(got)
					}
				case <-time.After(time.Second):
					t.Fatal("revoke did not cancel")
				}
			} else {
				m.err = context.DeadlineExceeded
				got, e := f.service.SendAnalysis(ctx, "p", r.ID, a.ID, in)
				if e != nil || got.Status != "unknown" {
					t.Fatal(got, e)
				}
			}
			if _, err = f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeRuntime: true}); err != ErrConflict {
				t.Fatal("unknown bypassed with new ID", err)
			}
			if got, e := f.service.SendAnalysis(ctx, "p", r.ID, a.ID, in); e != nil || got.Status != "unknown" || m.calls.Load() != 1 {
				t.Fatal(got, e, m.calls.Load())
			}
		})
	}
}

func TestAnalysisStaleSnapshotRecoveryAndSchema(t *testing.T) {
	f, r, m := analysisFixture(t)
	ctx := context.Background()
	a, e := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeRuntime: true})
	if e != nil {
		t.Fatal(e)
	}
	f.service.mu.Lock()
	d, _ := f.service.load(ctx, "p", r.ID)
	if e = f.service.save(ctx, d, "new_evidence", &Evidence{ID: newEvidenceID(), Kind: "runtime", TargetID: f.targetID, EventAt: f.clock.now(), CollectedAt: f.clock.now(), Value: RuntimeSnapshot{Outcome: "unavailable", Reason: "test"}}); e != nil {
		t.Fatal(e)
	}
	f.service.mu.Unlock()
	if _, e = f.service.SendAnalysis(ctx, "p", r.ID, a.ID, SendAnalysisInput{Confirmed: true, PayloadHash: a.PayloadHash, ProviderConfigHash: a.Provider.ConfigHash}); e != ErrConflict || m.calls.Load() != 0 {
		t.Fatal(e, m.calls.Load())
	}
	if _, e = f.store.DB.Exec(`UPDATE release_guard_analyses SET status='sending' WHERE id=?`, a.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.service.recover(ctx); e != nil {
		t.Fatal(e)
	}
	list, e := f.service.Analyses(ctx, "p", r.ID)
	if e != nil || len(list) != 1 || list[0].Status != "unknown" || !list[0].Stale || m.calls.Load() != 0 {
		t.Fatal(list, e)
	}
	for _, bad := range []string{
		`{"summary":"ok","summary":"changed","candidates":[]}`,
		`{"summary":"ok","candidates":[{"id":"c","title":"x","supportingEvidenceIds":["foreign"],"counterEvidenceIds":[],"missingEvidence":[],"nextReadOnlyCheck":"x"}]}`,
		`{"summary":"ok","candidates":[{"id":"c","title":"x","supportingEvidenceIds":["E1"],"supportingEvidenceIds":["foreign"]}]}`,
		`{"summary":"ok","candidates":[],"tools":["exec"]}`,
		`{"summary":"ok","candidates":[]} {"summary":"second"}`,
	} {
		if _, err := validateExplanation(bad, []string{"E1"}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid model output accepted", bad, err)
		}
	}
	if _, err := validateExplanation(`{"summary":"insufficient evidence","candidates":[]}`, []string{"E1"}); err != nil {
		t.Fatal(err)
	}
	if payload, _ := json.Marshal(list); strings.Contains(string(payload), "ConnectionSnapshot") {
		t.Fatal("private connection payload exposed")
	}
}

func TestAnalysisUnresolvedSendBlocksOtherPreparedSnapshots(t *testing.T) {
	for _, unresolved := range []string{"sending", "unknown"} {
		t.Run(unresolved, func(t *testing.T) {
			f, r, m := analysisFixture(t)
			ctx := context.Background()
			f.service.mu.Lock()
			d, _ := f.service.load(ctx, "p", r.ID)
			err := f.service.save(ctx, d, "fixture_logs", &Evidence{ID: newEvidenceID(), Kind: "logs", TargetID: f.targetID, CollectedAt: f.clock.now(), Value: map[string]any{"text": "fixture bounded log"}})
			f.service.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeRuntime: true})
			if err != nil {
				t.Fatal(err)
			}
			b, err := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeLogs: true})
			if err != nil || b.ID == a.ID {
				t.Fatal(b, err)
			}
			if _, err = f.store.DB.Exec(`UPDATE release_guard_analyses SET status=? WHERE id=?`, unresolved, a.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.SendAnalysis(ctx, "p", r.ID, b.ID, SendAnalysisInput{Confirmed: true, PayloadHash: b.PayloadHash, ProviderConfigHash: b.Provider.ConfigHash}); err != ErrConflict || m.calls.Load() != 0 {
				t.Fatal("another prepared snapshot bypassed unresolved request", err, m.calls.Load())
			}
			if got, e := f.service.SendAnalysis(ctx, "p", r.ID, a.ID, SendAnalysisInput{Confirmed: true, PayloadHash: a.PayloadHash, ProviderConfigHash: a.Provider.ConfigHash}); e != nil || got.Status != unresolved || m.calls.Load() != 0 {
				t.Fatal("idempotent status lookup changed", got, e)
			}
		})
	}
}

func TestAnalysisExpiredRevokeDoesNotExposePayload(t *testing.T) {
	f, r, m := analysisFixture(t)
	ctx := context.Background()
	a, err := f.service.PreviewAnalysis(ctx, "p", r.ID, AnalysisInput{IncludeRuntime: true})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.add(8 * 24 * time.Hour)
	if got, e := f.service.RevokeAnalysis(ctx, "p", r.ID, a.ID); e != ErrUnavailable || got != nil || m.calls.Load() != 0 {
		t.Fatal("expired analysis payload returned", got, e)
	}
}
