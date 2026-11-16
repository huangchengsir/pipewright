package target

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
	"time"
)

func TestExecutionNodeTeamOwnershipSurvivesServerDeletion(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	ca, cb := newSSHCred(t, v, "fixture-a"), newSSHCred(t, v, "fixture-b")
	svc := New(db, v, &capturingDialer{})
	a, e := svc.Create(ctx, CreateInput{Name: "A", Host: "node.example", User: "builder", CredentialID: ca})
	if e != nil {
		t.Fatal(e)
	}
	b, e := svc.Create(ctx, CreateInput{Name: "B", Host: "other.example", User: "builder", CredentialID: cb})
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "assignment-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	login, e := tenancy.NewAccounts(db).Login(ctx, "admin", "assignment-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, login.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	for _, ref := range []struct{ kind, id string }{{"server", b.ID}, {"credential", cb}} {
		if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind=? AND resource_id=?`, other.ID, ref.kind, ref.id); e != nil {
			t.Fatal(e)
		}
	}
	admin := tenancy.WithActor(ctx, login.Actor, team)
	adminB := tenancy.WithActor(ctx, login.Actor, other.ID)
	assign := svc.(ExecutionAssignmentService)
	key := "SHA256:" + strings.Repeat("A", 43)
	if _, e = assign.AssignExecution(admin, a.ID, key); e != nil {
		t.Fatal(e)
	}
	if _, e = assign.AssignExecution(adminB, b.ID, key); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("same SSH identity assigned to other team", e)
	}
	// A different key cannot evade the canonical host ownership either.
	host := "NODE.EXAMPLE."
	if _, e = svc.Update(adminB, b.ID, UpdateInput{Host: &host}); e != nil {
		t.Fatal(e)
	}
	if _, e = assign.AssignExecution(adminB, b.ID, "SHA256:"+strings.Repeat("B", 43)); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("same endpoint assigned to other team", e)
	}
	if _, e = assign.AssignExecution(admin, a.ID, ""); e != nil {
		t.Fatal(e)
	}
	if e = svc.Delete(admin, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = assign.AssignExecution(adminB, b.ID, key); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("deleting registration released node identity", e)
	}
	host = "other.example"
	if _, e = svc.Update(adminB, b.ID, UpdateInput{Host: &host}); e != nil {
		t.Fatal(e)
	}
	if _, e = assign.AssignExecution(adminB, b.ID, "SHA256:"+strings.Repeat("B", 43)); e != nil {
		t.Fatal("separate node", e)
	}
	bound, e := svc.(PinnedExecutor).Capture(adminB, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	host = "changed.example"
	if _, e = svc.Update(adminB, b.ID, UpdateInput{Host: &host}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Exec(adminB, b.ID, []string{"true"}); !errors.Is(e, tenancy.ErrConflict) {
		t.Fatal("changed connection retained approval", e)
	}
	if _, e = svc.(PinnedExecutor).ExecPinned(adminB, bound, []string{"true"}, ExecutionLimits{}); !errors.Is(e, ErrLimitedUnavailable) && !errors.Is(e, ErrTargetChanged) {
		t.Fatal(e)
	}
}
func TestAssignedSSHHostKeyPinnedBeforeRemoteCommand(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "mismatch"}[wrong], func(t *testing.T) {
			reached := false
			address, key, done := privateTestSSH(t, func(ch ssh.Channel, requests <-chan *ssh.Request) {
				reached = true
				for request := range requests {
					if request.Type != "exec" {
						_ = request.Reply(false, nil)
						continue
					}
					_ = request.Reply(true, nil)
					_, _ = ch.Write([]byte("ok"))
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				}
			})
			if wrong {
				key = "SHA256:" + strings.Repeat("A", 43)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, e := (sshDialer{}).Run(ctx, address, SSHConfig{User: "fixture", Password: "fixture-only", HostKey: key}, []string{"true"})
			<-done
			if wrong {
				if e == nil || reached {
					t.Fatal("wrong key opened command channel", e)
				}
			} else if e != nil || out.Stdout != "ok" || !reached {
				t.Fatal("matching key", e)
			}
		})
	}
}
