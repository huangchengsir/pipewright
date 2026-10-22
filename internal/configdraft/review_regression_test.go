package configdraft

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"testing"
	"time"
)

func TestValidationAndBrowserApprovalReportActiveReaders(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	ctx := context.Background()
	if _, err := f.db.Exec(`INSERT INTO project_activity(activity_id,project_id,owner_epoch,kind,phase,created_at,heartbeat_at) VALUES (?,?,'held','run','finalizing',0,0)`, uuid.NewString(), f.project); err != nil {
		t.Fatal(err)
	}
	v, err := f.svc.Validate(ctx, f.token, d.DraftID)
	if err != nil || v.ApplyAllowed || v.State != "blocked" {
		t.Fatal("validation hid active readers", v, err)
	}
	found := false
	for _, issue := range v.Issues {
		found = found || issue.Code == "active_runs"
	}
	if !found {
		t.Fatal("missing active_runs", v)
	}
	_, err = f.svc.Approve(ctx, "fixture-admin", d.DraftID, ApprovalRequest{ReviewDigest: d.ReviewDigest, BaseRevision: d.BaseRevision, PolicyVersion: d.PolicyVersion})
	if !errors.Is(err, ErrApproval) {
		t.Fatal("approved busy project", err)
	}
}

func TestApproverExpiryBeforeCommitRollsBackAllChanges(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	checks := 0
	f.svc.opts.RevalidateApprover = func(context.Context, configstate.DBTX, string) error {
		checks++
		if checks >= 2 {
			return ErrForbidden
		}
		return nil
	}
	op, err := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
	if err != nil || op.State != "rejected" || op.Reason != "approval_invalid" || checks != 2 {
		t.Fatal("commit skipped approver check", op, err, checks)
	}
	var revision int
	_ = f.db.QueryRow(`SELECT revision FROM project_config_versions WHERE project_id=?`, f.project).Scan(&revision)
	if revision != 1 {
		t.Fatal("expired approval committed")
	}
}

func TestAppliedDraftStateSurvivesItsOriginalExpiry(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	a := f.approve(t, d)
	op, err := f.svc.Apply(context.Background(), f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()})
	if err != nil || op.State != "applied" {
		t.Fatal(op, err)
	}
	later := time.Now().Add(25 * time.Hour)
	f.svc.opts.Now = func() time.Time { return later }
	view, err := f.svc.Get(context.Background(), f.token, d.DraftID)
	if err != nil || view.State != "applied" {
		t.Fatal("applied state overwritten by expiry", view, err)
	}
}

func TestDisabledApplyNeverAdvertisesBrowserApproval(t *testing.T) {
	f := setup(t)
	d := f.create(t, timeoutOp(120))
	f.svc.opts.ApplyEnabled = false
	view, err := f.svc.Review(context.Background(), "fixture-admin", d.DraftID)
	if err != nil || view.Validation.ApplyAllowed || view.Approval.Eligible || view.Approval.Reason != "apply_disabled" {
		t.Fatal("disabled approval advertised", view, err)
	}
}
