package trigger

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

type authorizedRunRecorder struct {
	calls int
	check func(context.Context, string) error
}

func (f *authorizedRunRecorder) CreateWebhookRun(ctx context.Context, project string, _ RunRequest) (string, error) {
	if err := f.check(ctx, project); err != nil {
		return "", err
	}
	f.calls++
	return "accepted-run", nil
}
func TestWebhookUsesSavedApprovalAndRejectsChangedSource(t *testing.T) {
	db, _ := testDB(t)
	project := seedProject(t, db)
	base := context.Background()
	v := vault.New(db, testMasterKey())
	svc := New(db, v)
	if err := auth.NewService(db, nil).Bootstrap("admin", "trigger-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.NewRepository(db).ReconcileMode(base, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	login, err := accounts.Login(base, "admin", "trigger-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.WithActor(base, login.Actor, team)
	reset, err := svc.ResetSecret(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	input := SaveInput{Events: Events{Push: true}, BranchMappings: []BranchMapping{{BranchPattern: "main"}}, UnmatchedPolicy: PolicyIgnore}
	cfg, err := svc.Save(ctx, project, input)
	if err != nil {
		t.Fatal(err)
	}
	runner := &authorizedRunRecorder{check: func(c context.Context, p string) error { return tenancy.CheckRunAdmission(c, db, p, "next-run") }}
	receiver := NewReceiver(db, v, runner)
	delivery := Delivery{Token: cfg.WebhookToken, Event: eventPush, TokenHdr: reset.Secret, DeliveryID: "first", RawBody: pushBody("main", "abc")}
	if err = accounts.Logout(base, login.Token); err != nil {
		t.Fatal(err)
	}
	result, err := receiver.Handle(base, delivery)
	if err != nil || !result.Accepted || runner.calls != 1 {
		t.Fatal(result, err, runner.calls)
	}
	if _, err = db.Exec(`UPDATE projects SET default_branch='changed' WHERE id=?`, project); err != nil {
		t.Fatal(err)
	}
	delivery.DeliveryID = "changed"
	if _, err = receiver.Handle(base, delivery); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("changed source allowed", err)
	}
	if runner.calls != 1 {
		t.Fatal("unapproved runner call")
	}
	var claims int
	if err = db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE delivery_id='changed'`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Fatal("failed authorization retained claim")
	}
}
