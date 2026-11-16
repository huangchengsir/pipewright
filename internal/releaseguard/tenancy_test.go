package releaseguard

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"testing"
	"time"
)

func TestTenantGuardReadScopesBeforeChildren(t *testing.T) {
	ctx := context.Background()
	f := newInvestigationFixture(t, "app")
	s, db := f.service, f.service.db
	d, err := s.load(ctx, "source-p", f.id)
	if err != nil {
		t.Fatal(err)
	}
	d.Report.ID = "hidden-guard"
	d.Report.RunID = "source-old"
	d.Report.Plan.ID = "hidden-plan"
	body, _ := json.Marshal(d)
	if _, err = db.Exec(`INSERT INTO release_guards(id,project_id,run_id,attempt_id,plan_id,idempotency_key,status,created_at,evidence_expires_at,report_expires_at,record_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, d.Report.ID, d.Report.ProjectID, d.Report.RunID, d.Report.AttemptID, d.Report.Plan.ID, "hidden-key", "completed", d.Report.CreatedAt.UnixMilli(), d.Report.EvidenceExpiresAt.UnixMilli(), d.Report.EvidenceExpiresAt.UnixMilli(), string(body)); err != nil {
		t.Fatal(err)
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "guard-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "guard-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	in := tenancy.RoleInput{Name: "Guard reader", Pages: []string{"page.guard"}, Grants: []authz.Grant{{Action: "guard.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"source-current"}}}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", in)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "guard-reader", DisplayName: "Guard reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "guard-reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "guard-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	if r, e := s.Snapshot(actor, "source-p", f.id); e != nil || r.ID != f.id {
		t.Fatal("allowed report", r, e)
	}
	if p, e := s.Evidence(actor, "source-p", f.id, 0, 1); e != nil || len(p.Items) != 1 {
		t.Fatal("allowed evidence pagination", p, e)
	}
	if p, e := s.Events(actor, "source-p", f.id, 0); e != nil || len(p.Items) == 0 {
		t.Fatal("allowed events", p, e)
	}
	if _, e := s.Analyses(actor, "source-p", f.id); e != nil {
		t.Fatal("allowed analyses", e)
	}
	for _, id := range []string{"hidden-guard", "missing-guard"} {
		for name, call := range map[string]func() error{
			"snapshot": func() error { _, e := s.Snapshot(actor, "source-p", id); return e },
			"evidence": func() error { _, e := s.Evidence(actor, "source-p", id, 0, 1); return e },
			"events":   func() error { _, e := s.Events(actor, "source-p", id, 0); return e },
			"analyses": func() error { _, e := s.Analyses(actor, "source-p", id); return e },
		} {
			if e := call(); !errors.Is(e, ErrNotFound) {
				t.Fatalf("%s %s leaked: %v", id, name, e)
			}
		}
	}
	if _, e := s.Snapshot(tenancy.WithActor(ctx, login.Actor, "forged-team"), "source-p", f.id); e == nil {
		t.Fatal("forged team allowed")
	}
	if _, e := s.ListAttempts(actor, "source-old"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("hidden attempt list", e)
	}
	if views, e := s.ListAttempts(actor, "source-current"); e != nil || len(views) != 1 {
		t.Fatal("allowed attempt list", views, e)
	}
	for name, call := range map[string]func() error{
		"stop":        func() error { _, e := s.Stop(actor, "source-p", f.id); return e },
		"delete":      func() error { return s.Delete(actor, "source-p", f.id) },
		"investigate": func() error { _, e := s.Investigate(actor, "source-p", f.id); return e },
		"analysis":    func() error { _, e := s.RevokeAnalysis(actor, "source-p", f.id, "fake"); return e },
	} {
		if e := call(); e == nil {
			t.Fatalf("reader could %s", name)
		}
	}
	if _, e := s.Snapshot(actor, "source-p", f.id); e != nil {
		t.Fatal("denied writes changed report", e)
	}

	currentTargets, e := s.ledger.ListTargets(ctx, d.Report.AttemptID)
	if e != nil || len(currentTargets) != 1 {
		t.Fatal("fixture targets", e)
	}
	history, e := s.visibleWorkloadHistory(actor, currentTargets[0].WorkloadKey, 1)
	if e != nil || len(history) != 1 || history[0].Source.RunID != "source-current" {
		t.Fatal("scoped history before limit", history, e)
	}
	source := currentTargets[0].Source
	if sha, reason := s.verifiedSource(actor, "source-p", source); sha != source.ResolvedFullSHA || reason != "" {
		t.Fatal("guard-only minimal source read", sha, reason)
	}
	source.RunID = "source-old"
	if _, reason := s.verifiedSource(actor, "source-p", source); reason != "source_run_unavailable" {
		t.Fatal("hidden source disclosed", reason)
	}
	if _, e := f.runs.Get(actor, "source-current"); !errors.Is(e, run.ErrNotFound) {
		t.Fatal("guard read granted ordinary run read", e)
	}
	// Even when a hidden historical entry sorts first, LIMIT is applied after
	// authorization, not followed by a lossy in-memory filter.
	if _, e = db.Exec(`UPDATE deployment_attempt_targets SET created_at=created_at+100000 WHERE attempt_id IN (SELECT id FROM deployment_attempts WHERE run_id='source-old')`); e != nil {
		t.Fatal(e)
	}
	history, e = s.visibleWorkloadHistory(actor, currentTargets[0].WorkloadKey, 1)
	if e != nil || len(history) != 1 || history[0].Source.RunID != "source-current" {
		t.Fatal("hidden history consumed page", history, e)
	}

	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	full, e := s.Investigate(adminCtx, "source-p", f.id)
	if e != nil || full.Investigation.BaselineStatus != "verified_immediate" {
		t.Fatal("admin investigation", full, e)
	}
	narrowed, e := s.Snapshot(actor, "source-p", f.id)
	if e != nil || narrowed.Investigation.Status != "not_investigated" || len(narrowed.Investigation.Baselines) != 0 {
		t.Fatal("stored history leaked to narrower reader", narrowed, e)
	}
	for _, item := range narrowed.Timeline {
		if item.SourceRunID == "source-old" {
			t.Fatal("stored timeline leaked hidden run", item)
		}
	}
	unchanged, e := s.Snapshot(adminCtx, "source-p", f.id)
	if e != nil || unchanged.Investigation.BaselineStatus != "verified_immediate" {
		t.Fatal("projection modified stored report", unchanged, e)
	}

	in.ExpectedRevision = role.Revision
	in.Grants = append(in.Grants, authz.Grant{Action: "guard.analyze", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"source-current"}}})
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	model := &guardModelFixture{info: ModelInfo{Name: "fixture", Model: "fixture", Destination: "https://fixture.invalid", ConfigHash: strings.Repeat("f", 64)}, entered: make(chan struct{})}
	s.model = model
	preview, e := s.PreviewAnalysis(actor, "source-p", f.id, AnalysisInput{IncludeRuntime: true})
	if e != nil {
		t.Fatal("authorized model preview", e)
	}
	requestCtx, cancelRequest := context.WithCancel(actor)
	defer cancelRequest()
	done := make(chan error, 1)
	go func() {
		_, e := s.SendAnalysis(requestCtx, "source-p", f.id, preview.ID, SendAnalysisInput{Confirmed: true, PayloadHash: preview.PayloadHash, ProviderConfigHash: preview.Provider.ConfigHash})
		done <- e
	}()
	select {
	case <-model.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model not entered")
	}
	in.ExpectedRevision = role.Revision
	in.Grants = nil
	if _, e := repo.SaveRole(ctx, admin.Actor, team, role.ID, in); e != nil {
		t.Fatal(e)
	}

	select {
	case e := <-done:
		if e == nil {
			t.Fatal("revoked analysis returned data")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("revocation did not cancel in-flight model request")
	}
	stored, e := s.loadAnalysis(ctx, f.id, preview.ID)
	if e != nil || stored.Status != "unknown" || model.calls.Load() != 1 {
		t.Fatal("revoked dispatch lost honest unknown result", stored, e)
	}
	if _, e := s.Snapshot(actor, "source-p", f.id); !errors.Is(e, ErrNotFound) {
		t.Fatal("revoked report", e)
	}
	if _, e := s.Evidence(actor, "source-p", f.id, 0, 1); !errors.Is(e, ErrNotFound) {
		t.Fatal("revoked children", e)
	}
}
