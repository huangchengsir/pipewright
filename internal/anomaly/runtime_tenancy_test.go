package anomaly

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

type authorizedCollector struct {
	db     *sql.DB
	calls  int
	before func()
}

func (c *authorizedCollector) Collect(ctx context.Context, ids []string) ([]ServerMetricsSnapshot, error) {
	c.calls++
	if len(ids) != 1 || ids[0] != "s1" {
		return nil, errors.New("sampling scope widened")
	}
	if err := tenancy.Check(ctx, c.db, "server.use", "server", "s1"); err != nil {
		return nil, err
	}
	if err := tenancy.Check(ctx, c.db, "credential.use", "credential", "c1"); err != nil {
		return nil, err
	}
	if c.before != nil {
		c.before()
	}
	return []ServerMetricsSnapshot{{ServerID: "s1", ServerName: "Node", Available: true, CPUPercent: fptr(95)}, {ServerID: "foreign", ServerName: "Unrequested", Available: true, CPUPercent: fptr(95)}}, nil
}
func TestScheduledSamplingUsesSavedPrincipalAndVersion(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO credentials(id,name,type,ciphertext,masked_value,created_at,updated_at) VALUES('c1','Node key','ssh_key',?,'masked','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, []byte("encrypted-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) VALUES('s1','Node','isolated.invalid',22,'worker','c1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "sampling-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	login, err := accounts.Login(ctx, "admin", "sampling-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	collector := &authorizedCollector{db: db}
	svc := New(db, collector, WithCooldown(time.Millisecond)).(*service)
	defer svc.Close()
	input := CreateRuleInput{Metric: MetricCPU, Operator: OperatorGreaterThan, Threshold: 80, Enabled: true, ServerID: ptr("s1")}
	rule, err := svc.CreateRule(actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if rule.SamplingAuthorization != "ready" {
		t.Fatal("approval absent", rule.SamplingAuthorization)
	}
	if _, err = db.Exec(`DELETE FROM access_sessions WHERE user_id=?`, login.User.ID); err != nil {
		t.Fatal(err)
	}
	alerts, err := svc.CheckScheduled(ctx)
	if err != nil || len(alerts) != 1 || alerts[0].ServerID != "s1" {
		t.Fatal("logout or foreign collector data", alerts, err)
	}
	if collector.calls != 1 {
		t.Fatal(collector.calls)
	}
	if _, err = db.Exec(`UPDATE servers SET host='changed.invalid' WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CheckScheduled(ctx); err == nil || collector.calls != 1 {
		t.Fatal("changed target was sampled", collector.calls, err)
	}
	if _, err = db.Exec(`UPDATE servers SET host='isolated.invalid' WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	collector.before = func() {
		if _, e := db.Exec(`UPDATE access_users SET security_generation=security_generation+1 WHERE id=?`, login.User.ID); e != nil {
			t.Error(e)
		}
	}
	if _, err = svc.CheckScheduled(ctx); err == nil {
		t.Fatal("revocation after collection produced alert")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM anomaly_alerts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("denied sample wrote", count, err)
	}
	if _, err = svc.Check(ctx); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("manual endpoint without actor", err)
	}
}
