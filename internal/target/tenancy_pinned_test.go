package target

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"strings"
	"testing"
	"time"
)

type tenancyTransport struct{ limitedCapture }

func (d *tenancyTransport) RunLimitedWithStdin(c context.Context, a string, s SSHConfig, cmd []string, _ io.Reader, l ExecutionLimits) (*LimitedResult, error) {
	return d.RunLimited(c, a, s, cmd, l)
}
func (d *tenancyTransport) RunSecret(context.Context, string, SSHConfig, string, []string, []byte) (*SecretResult, error) {
	d.calls++
	return &SecretResult{}, nil
}
func (d *tenancyTransport) PullSecret(context.Context, string, SSHConfig, string, string, []byte) error {
	d.calls++
	return nil
}
func TestTenantPinnedTransportsRequireIndependentUsePermissions(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	cred := newSSHCred(t, v, "transport-password")
	d := &tenancyTransport{}
	svc := New(db, v, d)
	server, e := svc.Create(ctx, CreateInput{Name: "pinned", Host: "127.0.0.1", User: "fixture", CredentialID: cred})
	if e != nil {
		t.Fatal(e)
	}
	pin := svc.(PinnedExecutor)
	secret := svc.(SecretExecutor)
	snap, e := pin.Capture(ctx, server.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "admin-pinned-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "admin-pinned-password")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.(ExecutionAssignmentService).AssignExecution(tenancy.WithActor(ctx, admin.Actor, team), server.ID, "SHA256:"+strings.Repeat("A", 43)); e != nil {
		t.Fatal(e)
	}
	snap, e = pin.Capture(tenancy.WithActor(ctx, admin.Actor, team), server.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, variant := range []string{"inspect", "server-use", "all"} {
		grants := []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
		if variant != "inspect" {
			grants = append(grants, authz.Grant{Action: "server.use", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		}
		if variant == "all" {
			grants = append(grants, authz.Grant{Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		}
		role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: variant, Pages: []string{"page.servers", "page.vault"}, RequiresSelectedBinding: true, Grants: grants})
		if e != nil {
			t.Fatal(e)
		}
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: variant, DisplayName: variant, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: server.ID}, {Kind: "credential", ID: cred}}}}}})
		if e != nil {
			t.Fatal(e)
		}
		login, e := accounts.Login(ctx, variant, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, variant+"-pinned-password")
		if e != nil {
			t.Fatal(e)
		}
		actor := tenancy.WithActor(ctx, login.Actor, team)
		operations := []func(context.Context) error{
			func(c context.Context) error {
				_, e := pin.ExecLimited(c, snap, []string{"uname"}, ExecutionLimits{})
				return e
			},
			func(c context.Context) error {
				_, e := pin.ExecPinned(c, snap, []string{"uname"}, ExecutionLimits{})
				return e
			},
			func(c context.Context) error {
				return pin.UploadPinned(c, snap, strings.NewReader("fixture"), "/tmp/fixture")
			},
			func(c context.Context) error {
				_, e := secret.ExecSecretPinned(c, snap, "SHA256:"+strings.Repeat("A", 43), []string{"uname"}, nil)
				return e
			},
			func(c context.Context) error {
				return secret.PullSecretPinned(c, snap, "SHA256:"+strings.Repeat("A", 43), "registry.invalid/test", nil)
			},
		}
		for index, op := range operations {
			before := d.calls
			e := op(actor)
			if variant == "all" {
				if e != nil || d.calls != before+1 {
					t.Fatal(variant, index, e, d.calls)
				}
			} else if !errors.Is(e, tenancy.ErrForbidden) || d.calls != before {
				t.Fatal("unauthorized transport", variant, index, e, d.calls)
			}
		}
		containerID := strings.Repeat("a", 64)
		issuer := svc.(DockerToolCleanupIssuer)
		boundCleanup, cleanupErr := issuer.OpenDockerToolCleanup(actor, snap, snap.ExecutionHostKey, containerID)
		if variant != "all" && (!errors.Is(cleanupErr, tenancy.ErrForbidden) || boundCleanup != nil) {
			t.Fatal("unauthorized cleanup issuance", cleanupErr)
		}
		if variant == "all" {
			if cleanupErr != nil || boundCleanup == nil {
				t.Fatal("authorized cleanup issuance", cleanupErr)
			}
			if _, e = db.Exec(`UPDATE servers SET `+store.Identifier(store.DialectOf(db), "user")+`='root' WHERE id=?`, server.ID); e != nil {
				t.Fatal(e)
			}
			rootSnap, e := pin.Capture(actor, server.ID)
			if e != nil {
				t.Fatal(e)
			}
			before := d.calls
			if _, e = pin.ExecPinned(actor, rootSnap, []string{"uname"}, ExecutionLimits{}); !errors.Is(e, tenancy.ErrForbidden) || d.calls != before {
				t.Fatal("root execution", e, d.calls)
			}
			if _, e = db.Exec(`UPDATE servers SET `+store.Identifier(store.DialectOf(db), "user")+`='fixture' WHERE id=?`, server.ID); e != nil {
				t.Fatal(e)
			}
			if e = accounts.Logout(ctx, login.Token); e != nil {
				t.Fatal(e)
			}
			before = d.calls
			for _, attempt := range [][]string{
				{"exec", containerID, "true"}, {"rm", "-f", containerID},
				{"rm", strings.Repeat("b", 64)}, {"inspect", containerID},
			} {
				if _, err := boundCleanup.Docker(actor, attempt, nil); !errors.Is(err, ErrTargetChanged) || d.calls != before {
					t.Fatalf("unauthorized cleanup command dispatched: %v", attempt)
				}
			}
			for _, allowed := range [][]string{
				{"info", "--format", "{{json .}}"},
				{"inspect", "--type", "container", "--format", "{{json .}}", containerID},
				{"stop", "--time", "2", containerID}, {"rm", containerID},
			} {
				if _, err := boundCleanup.Docker(actor, allowed, nil); err != nil {
					t.Fatal("bound cleanup after logout", err)
				}
			}
			if d.calls != before+4 {
				t.Fatal("unexpected cleanup call count")
			}
			if _, err := boundCleanup.Docker(actor, []string{"rm", containerID}, []byte("irrelevant")); !errors.Is(err, ErrTargetChanged) || d.calls != before+4 {
				t.Fatal("cleanup accepted input")
			}
			expired := *boundCleanup
			expired.expires = time.Now().Add(-time.Second)
			if _, err := expired.Docker(actor, []string{"rm", containerID}, nil); !errors.Is(err, ErrTargetChanged) || d.calls != before+4 {
				t.Fatal("expired cleanup reached SSH")
			}
			var rawBinding string
			if err := db.QueryRow("SELECT execution_binding_json FROM servers WHERE id=?", server.ID).Scan(&rawBinding); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE servers SET execution_binding_json=NULL WHERE id=?", server.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := boundCleanup.Docker(actor, []string{"rm", containerID}, nil); !errors.Is(err, ErrTargetChanged) || d.calls != before+4 {
				t.Fatal("changed assignment received cleanup", err)
			}
			if _, err := db.Exec("UPDATE servers SET execution_binding_json=? WHERE id=?", rawBinding, server.ID); err != nil {
				t.Fatal(err)
			}
			var original []byte
			if err := db.QueryRow("SELECT ciphertext FROM credentials WHERE id=?", cred).Scan(&original); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE credentials SET ciphertext=? WHERE id=?", []byte("test-rotated"), cred); err != nil {
				t.Fatal(err)
			}
			if _, err := boundCleanup.Docker(actor, []string{"rm", containerID}, nil); !errors.Is(err, ErrTargetChanged) || d.calls != before+4 {
				t.Fatal("changed credential reached SSH", err)
			}
			if _, err := db.Exec("UPDATE credentials SET ciphertext=? WHERE id=?", original, cred); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE servers SET host='192.0.2.10' WHERE id=?", server.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := boundCleanup.Docker(actor, []string{"rm", containerID}, nil); !errors.Is(err, ErrTargetChanged) || d.calls != before+4 {
				t.Fatal("changed host received cleanup", err)
			}
			for index, op := range operations {
				before := d.calls
				if e = op(actor); !errors.Is(e, tenancy.ErrForbidden) || d.calls != before {
					t.Fatal("revoked transport", index, e, d.calls)
				}
			}
		}
	}
}
