package oauth

import (
	"errors"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

func TestReviewCompletedOAuthUseRetriesBeforeNextMutation(t *testing.T) {
	for _, operation := range []string{"reconnect", "edit", "delete", "repeat-release"} {
		t.Run(operation, func(t *testing.T) {
			storetest.SkipIfMySQL(t)
			f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
			_, done := f.acquire(t)
			if _, e := f.db.Exec("CREATE TRIGGER fail_oauth_release BEFORE DELETE ON oauth_credential_uses BEGIN SELECT RAISE(ABORT,'synthetic cleanup SQL failure'); END"); e != nil {
				t.Fatal(e)
			}
			done()
			var n int
			if e := f.db.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?", f.id).Scan(&n); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			if _, e := f.db.Exec("DROP TRIGGER fail_oauth_release"); e != nil {
				t.Fatal(e)
			}
			var e error
			switch operation {
			case "reconnect":
				_, e = f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, f.id, 1, false)
			case "edit":
				_, e = f.managed.Update(f.id, vault.UpdateInput{Secret: strp("new-local-token"), DetachOAuth: true})
			case "delete":
				e = f.managed.Delete(f.id)
			case "repeat-release":
				done()
			}
			if e != nil {
				t.Fatalf("known returned OAuth use blocked %s after DB recovery: %v", operation, e)
			}
			if e = f.db.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?", f.id).Scan(&n); e != nil || n != 0 {
				t.Fatal("completed reference was not released", n, e)
			}
			if f.refreshes.Load() != 0 || f.exchanges.Load() != 1 {
				t.Fatal("bookkeeping cleanup replayed a provider exchange")
			}
		})
	}
}
func TestReviewOAuthRetryKeepsForeignOwnerAtSameID(t *testing.T) {
	storetest.SkipIfMySQL(t)
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	_, done := f.acquire(t)
	if _, e := f.db.Exec("CREATE TRIGGER fail_old_owner_release BEFORE DELETE ON oauth_credential_uses BEGIN SELECT RAISE(ABORT,'synthetic cleanup SQL failure'); END"); e != nil {
		t.Fatal(e)
	}
	done()
	if _, e := f.db.Exec("UPDATE oauth_credential_uses SET owner='new-foreign-owner' WHERE credential_id=?", f.id); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec("DROP TRIGGER fail_old_owner_release"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.managed.Update(f.id, vault.UpdateInput{Secret: strp("new-local-token"), DetachOAuth: true}); !errors.Is(e, vault.ErrOAuthBusy) {
		t.Fatal("foreign reference cleared by old completion", e)
	}
	var n int
	if e := f.db.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=? AND owner='new-foreign-owner'", f.id).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}

func TestReviewUnreleasedOAuthBookkeepingStillBlocksInstallation(t *testing.T) {
	storetest.SkipIfMySQL(t)
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	_, done := f.acquire(t)
	if _, e := f.db.Exec("CREATE TRIGGER fail_install_oauth_release BEFORE DELETE ON oauth_credential_uses BEGIN SELECT RAISE(ABORT,'synthetic cleanup SQL failure'); END"); e != nil {
		t.Fatal(e)
	}
	done()
	if e := maintenance.Fence(ctx(), f.db, "test-install"); !errors.Is(e, maintenance.ErrBlocked) {
		t.Error("installation could discard unresolved local completion evidence", e)
		_ = maintenance.Unfence(ctx(), f.db, "test-install")
	}
	if _, e := f.db.Exec("DROP TRIGGER fail_install_oauth_release"); e != nil {
		t.Fatal(e)
	}
	if e := maintenance.Fence(ctx(), f.db, "test-install"); e != nil {
		t.Fatal("recovered bookkeeping did not release installation", e)
	}
}
