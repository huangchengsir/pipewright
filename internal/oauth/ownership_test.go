package oauth

import (
	"errors"
	"strings"
	"testing"
	"time"

	secretmask "github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestManagedOAuthMaskerPreservesOptionalReaderAndMasksTokenPair(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	f.now.Add(61000)
	if _, ok := f.managed.(vault.MaskSecretRegistrar); !ok {
		t.Fatal("managed vault hid the local bounded secret registrar")
	}
	m := secretmask.NewMasker()
	if err := vault.RegisterMaskSecrets(ctx(), f.managed, m); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-access-1", "fixture-refresh-1"} {
		if strings.Contains(m.Scrub("echo "+secret), secret) {
			t.Fatal("OAuth token pair was not masked")
		}
	}
	var used any
	if err := f.db.QueryRow("SELECT last_used_at FROM credentials WHERE id=?", f.id).Scan(&used); err != nil || used != nil {
		t.Fatal("secret registration marked a credential as used", err)
	}
	if f.refreshes.Load() != 0 || f.exchanges.Load() != 1 || f.binding(t).Revision != 1 {
		t.Fatal("local secret registration performed a provider exchange")
	}
	if _, done, err := vault.AcquireGitAuth(ctx(), f.managed, f.id); err != nil {
		t.Fatal(err)
	} else {
		done()
	}
	m = secretmask.NewMasker()
	if err := vault.RegisterMaskSecrets(ctx(), f.managed, m); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-access-2", "fixture-refresh-2"} {
		if strings.Contains(m.Scrub(secret), secret) {
			t.Fatal("rotated OAuth token pair was not masked")
		}
	}
}

func TestManagedOAuthMaskerRejectsCorruptAndOversizedRefresh(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	if _, err := f.db.Exec("UPDATE oauth_credentials SET refresh_ciphertext=? WHERE credential_id=?", []byte{0}, f.id); err != nil {
		t.Fatal(err)
	}
	if err := vault.RegisterMaskSecrets(ctx(), f.managed, secretmask.NewMasker()); !errors.Is(err, vault.ErrDecrypt) {
		t.Fatal("corrupt refresh token was silently omitted from the secret catalog", err)
	}
	if store.DialectOf(f.db) == store.SQLite {
		if _, err := f.db.Exec("UPDATE oauth_credentials SET refresh_ciphertext=? WHERE credential_id=?", make([]byte, 65537), f.id); err != nil {
			t.Fatal(err)
		}
		if err := vault.RegisterMaskSecrets(ctx(), f.managed, secretmask.NewMasker()); err == nil {
			t.Fatal("oversized refresh token bypassed the bounded reader")
		}
	}
}

func TestManagedOAuthForeignUsesAreNeverClearedByExplicitReconnect(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	acquisition, e := f.s.lifecycle.repo.TryAcquire(ctx(), f.id, "other-process", time.UnixMilli(f.now.Load()))
	if e != nil || acquisition.Action != "use" {
		t.Fatal("fixture use not admitted", e)
	}
	f.now.Add(int64(365 * 24 * time.Hour / time.Millisecond))
	if _, e = f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, f.id, 1, false); !errors.Is(e, vault.ErrOAuthBusy) {
		t.Fatal("unknown user's age was treated as proof of exit", e)
	}
	if e = f.s.lifecycle.repo.Release(ctx(), acquisition.UseID, "wrong-owner"); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = f.db.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE id=?", acquisition.UseID).Scan(&n); e != nil || n != 1 {
		t.Fatal("foreign reference was removed", e)
	}
	if e = f.s.lifecycle.repo.Release(ctx(), acquisition.UseID, "other-process"); e != nil {
		t.Fatal(e)
	}
}

func TestManagedOAuthTokenPairChangesInvalidateSecretCatalogOnlyOnCommit(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitLab, rotatingFixture)
	read := func() int64 {
		t.Helper()
		var n int64
		if e := f.db.QueryRow("SELECT value FROM config_control_epochs WHERE name='secret_catalog'").Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := read()
	_, done := f.acquire(t)
	done()
	if read() != before {
		t.Fatal("reading a generation changed secret catalog")
	}
	f.now.Add(61000)
	_, done = f.acquire(t)
	done()
	if read() != before+1 {
		t.Fatal("atomic token rotation did not invalidate old secret snapshots once")
	}
	if _, e := f.raw.List(); e != nil {
		t.Fatal(e)
	}
	if _, e := f.raw.Reveal(f.id); e != nil {
		t.Fatal(e)
	}
	if read() != before+1 {
		t.Fatal("local metadata/reveal mutated secret epoch")
	}
}
