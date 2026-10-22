package releaseguard

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func healthBool(v bool) *bool { return &v }

func TestObservedHealthComparisonConditions(t *testing.T) {
	for _, tc := range []struct {
		name            string
		parent, current *bool
		parentKey       string
		wantChange      string
	}{
		{"health_removed", healthBool(true), healthBool(false), "workload", "docker_health_condition_changed"},
		{"health_added", healthBool(false), healthBool(true), "workload", "docker_health_condition_changed"},
		{"health_remains_required", healthBool(true), healthBool(true), "workload", ""},
		{"health_remains_not_applicable", healthBool(false), healthBool(false), "workload", ""},
		{"parent_unknown", nil, healthBool(false), "workload", "health_condition_comparison_unavailable"},
		{"current_unknown", healthBool(false), nil, "workload", "health_condition_comparison_unavailable"},
		{"parent_workload_missing", healthBool(false), healthBool(false), "other", "health_condition_comparison_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := TargetObservation{PlanTarget: PlanTarget{WorkloadKey: tc.parentKey}, HealthRequired: tc.parent}
			current := TargetObservation{PlanTarget: PlanTarget{WorkloadKey: "workload"}, HealthRequired: tc.current}
			got := compareObservedHealth(Comparison{SameConditions: true, Changes: []string{}}, []TargetObservation{parent}, current)
			want := []string{}
			if tc.wantChange != "" {
				want = append(want, tc.wantChange)
			}
			if got.SameConditions != (tc.wantChange == "") || !reflect.DeepEqual(got.Changes, want) {
				t.Fatalf("comparison=%+v want change=%q", got, tc.wantChange)
			}
			prior := Comparison{SameConditions: false, Changes: []string{"duration_changed"}}
			got = compareObservedHealth(prior, []TargetObservation{parent}, current)
			if got.SameConditions || got.Changes[0] != "duration_changed" {
				t.Fatalf("health comparison replaced existing condition differences: %+v", got)
			}
		})
	}
}

func insertHealthComparisonReport(t *testing.T, f serviceIntegrationFixture, id, project, parent string, required *bool) *document {
	t.Helper()
	now := f.clock.now()
	target := ruleTarget()
	target.ID = id + "-target"
	target.WorkloadKey = "workload"
	target.HealthRequired = required
	target.Deadline = now.Add(120 * time.Second)
	d := &document{Report: Report{ID: id, ProjectID: project, RunID: "r", AttemptID: f.attemptID, ParentGuardID: parent, Plan: Plan{ID: id + "-plan", ProjectID: project, DurationSeconds: 300}, Targets: []TargetObservation{target}, ExecutionStatus: "binding", CreatedAt: now, EvidenceExpiresAt: now.Add(7 * 24 * time.Hour), Comparison: Comparison{SameConditions: true, Changes: []string{}}}, Private: privateState{Schedule: map[string]time.Time{}}}
	if parent == "" {
		d.Report.ExecutionStatus = "completed"
		d.Report.VerificationResult = strptr("failed")
		d.Report.Targets[0].VerificationResult = strptr("failed")
		d.Report.Targets[0].Conditions = []Condition{{Name: "health", Result: "failed", Reason: "docker_unhealthy", EvidenceIDs: []string{}}}
	}
	body, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB.Exec(`INSERT INTO release_guards(id,project_id,run_id,attempt_id,plan_id,idempotency_key,status,created_at,evidence_expires_at,report_expires_at,record_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, project, "r", f.attemptID, d.Report.Plan.ID, id+"-key", d.Report.ExecutionStatus, now.UnixMilli(), d.Report.EvidenceExpiresAt.UnixMilli(), now.Add(30*24*time.Hour).UnixMilli(), string(body)); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestHealthComparisonFirstRuntimeUsesScopedParentRecord(t *testing.T) {
	for _, tc := range []struct {
		name, parentProject, want string
		parentHealth              *bool
	}{
		{"removed_health", "p", "docker_health_condition_changed", healthBool(true)},
		{"same_health", "p", "", healthBool(false)},
		{"unknown_parent_health", "p", "health_condition_comparison_unavailable", nil},
		{"foreign_parent_is_unavailable", "another-project", "health_condition_comparison_unavailable", healthBool(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceIntegrationFixture(t)
			parent := insertHealthComparisonReport(t, f, "parent", tc.parentProject, "", tc.parentHealth)
			before, _ := json.Marshal(parent.Report)
			d := insertHealthComparisonReport(t, f, "child", "p", parent.Report.ID, nil)
			v := ruleSnapshot()
			v.CollectedAt = f.clock.now()
			v.Health = "" // The new image has no Docker health condition.
			e := Evidence{TargetID: d.Report.Targets[0].ID, Kind: "runtime", Value: v}
			f.service.mu.Lock()
			err := f.service.recordSampleLocked(context.Background(), d, &d.Report.Targets[0], v, &e)
			f.service.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			if tc.want != "" {
				want = append(want, tc.want)
			}
			if d.Report.Targets[0].HealthRequired == nil || *d.Report.Targets[0].HealthRequired || d.Report.Comparison.SameConditions != (tc.want == "") || !reflect.DeepEqual(d.Report.Comparison.Changes, want) {
				t.Fatalf("first runtime did not freeze and compare actual health participation: %+v", d.Report)
			}
			if tc.name == "removed_health" {
				for i := 1; i <= 10; i++ {
					f.clock.add(30 * time.Second)
					v.CollectedAt = f.clock.now()
					e = Evidence{TargetID: d.Report.Targets[0].ID, Kind: "runtime", Value: v}
					if err = f.service.recordSampleLocked(context.Background(), d, &d.Report.Targets[0], v, &e); err != nil {
						t.Fatal(err)
					}
				}
				f.service.finishLocked(context.Background(), d, "completed", "virtual_window_ended")
				if d.Report.VerificationResult == nil || *d.Report.VerificationResult != "passed" || d.Report.Comparison.SameConditions {
					t.Fatalf("condition comparison altered the new verification or claimed equality: %+v", d.Report)
				}
			}
			if got := f.observer.reads.Load(); got != 0 {
				t.Fatalf("local parent comparison dispatched %d observer reads", got)
			}
			unchanged, err := f.service.load(context.Background(), tc.parentProject, parent.Report.ID)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(unchanged.Report)
			if string(before) != string(after) {
				t.Fatal("current comparison rewrote the historical parent report")
			}
		})
	}
}
