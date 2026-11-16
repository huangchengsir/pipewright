package oauth

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"sync/atomic"
	"testing"
	"time"
)

func tenantLeaseActor(t *testing.T, f *lifecycleFixture) (context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	if err := auth.NewService(f.db, nil).Bootstrap("admin", "admin-oauth-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.NewRepository(f.db).ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	f.db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(f.db)
	login, err := accounts.Login(ctx, "admin", "admin-oauth-password")
	if err != nil {
		t.Fatal(err)
	}
	return tenancy.WithActor(ctx, login.Actor, team), func() {
		if e := accounts.Logout(ctx, login.Token); e != nil {
			t.Error(e)
		}
	}
}
func TestManagedOAuthRefreshRechecksRevokedSessionBeforeReturningToken(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	actor, revoke := tenantLeaseActor(t, f)
	f.blockRefresh = make(chan struct{})
	f.now.Add(61000)
	done := make(chan error, 1)
	go func() {
		a, release, e := vault.AcquireGitAuth(actor, f.managed, f.id)
		if release != nil {
			release()
		}
		if a.Token != "" {
			done <- errors.New("revoked caller received token")
			return
		}
		done <- e
	}()
	select {
	case <-f.refreshEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh not reached")
	}
	revoke()
	close(f.blockRefresh)
	select {
	case e := <-done:
		if !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked refresh stalled")
	}
	var n int
	if e := f.db.QueryRow(`SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?`, f.id).Scan(&n); e != nil || n != 0 {
		t.Fatal("revoked refresh leaked hold", n, e)
	}
	if f.binding(t).State != vault.OAuthActive {
		t.Fatal("returned exchange outcome lost")
	}
}

type revokeAtUseVault struct {
	vault.Vault
	calls  atomic.Int32
	revoke func()
}

func (v *revokeAtUseVault) AuthorizeUse(ctx context.Context, id string) error {
	if v.calls.Add(1) == 3 {
		v.revoke()
	}
	return vault.AuthorizeUse(ctx, v.Vault, id)
}
func TestManagedOAuthReleasesAcquiredHoldWhenReturnCheckFails(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	actor, revoke := tenantLeaseActor(t, f)
	decorated := &revokeAtUseVault{Vault: f.raw, revoke: revoke}
	managed := ManagedVault(f.s, decorated).(*managedVault)
	// Direct method has entry, loop, and post-acquisition checks. Revoke exactly
	// after TryAcquire committed its use row, before the token can be returned.
	a, release, e := managed.AcquireGitAuth(actor, f.id)
	if release != nil {
		release()
	}
	if !errors.Is(e, tenancy.ErrForbidden) || a.Token != "" {
		t.Fatal("return check failed", e)
	}
	var n int
	if e = f.db.QueryRow(`SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?`, f.id).Scan(&n); e != nil || n != 0 {
		t.Fatal("acquired hold not released", n, e)
	}
}
