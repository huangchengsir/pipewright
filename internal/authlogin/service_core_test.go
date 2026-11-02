package authlogin

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coreClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *coreClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *coreClock) add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type coreAdapter struct {
	calls            atomic.Int32
	identity         Identity
	err              error
	entered, release chan struct{}
}

func (a *coreAdapter) Authorize(_ context.Context, _ AppConfig, p AuthorizeParams) (string, error) {
	return "https://provider.invalid/authorize?state=" + url.QueryEscape(p.State), nil
}
func (a *coreAdapter) Exchange(ctx context.Context, _ AppConfig, _ ExchangeParams) (Identity, error) {
	a.calls.Add(1)
	if a.entered != nil {
		a.entered <- struct{}{}
		select {
		case <-a.release:
		case <-ctx.Done():
			return Identity{}, ErrResultUncertain
		}
	}
	return a.identity, a.err
}

type coreFixture struct {
	s       *Service
	a       *auth.Service
	db      *store.Store
	session *auth.Session
	adapter *coreAdapter
	clock   *coreClock
}

func coreSetup(t *testing.T) *coreFixture {
	t.Helper()
	db := storetest.Open(t)
	clock := &coreClock{now: time.Now().UTC().Truncate(time.Second)}
	a := auth.NewService(db.DB, clock)
	if e := a.Bootstrap("admin", "fixture-password"); e != nil {
		t.Fatal(e)
	}
	session, e := a.Login("admin", "fixture-password")
	if e != nil {
		t.Fatal(e)
	}
	var key [32]byte
	copy(key[:], []byte("only-local-test-master-key-value!"))
	v := vault.New(db.DB, &key)
	adapter := &coreAdapter{identity: Identity{Provider: ProviderGitHub, Issuer: "https://github.com", Subject: "90071992547409931234", DisplayLabel: "local fixture"}}
	s := New(db.DB, v, a, Config{PublicURL: "https://pipewright.example", Now: clock.Now, Adapters: map[string]Adapter{ProviderGitHub: adapter}})
	return &coreFixture{s, a, db, session, adapter, clock}
}
func (f *coreFixture) save(t *testing.T) Mutation {
	t.Helper()
	secret := "canary-local-login-secret"
	out, e := f.s.SaveApp(context.Background(), f.session.Token, ProviderGitHub, AppInput{ExpectedVersion: 1, ClientID: "dedicated-login-app", ClientSecret: &secret})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func stateOf(t *testing.T, start StartResult) string {
	t.Helper()
	u, e := url.Parse(start.AuthorizeURL)
	if e != nil {
		t.Fatal(e)
	}
	return u.Query().Get("state")
}
func (f *coreFixture) bind(t *testing.T) Mutation {
	t.Helper()
	out := f.save(t)
	start, e := f.s.Start(context.Background(), ProviderGitHub, "bind", f.session.Token, "/settings/account", out.Provider.ConfigVersion)
	if e != nil {
		t.Fatal(e)
	}
	complete, e := f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, f.session.Token, "fixture-code", false)
	if e != nil || complete.Session != nil || complete.BindingFlow != start.FlowID {
		t.Fatalf("candidate: %+v %v", complete, e)
	}
	out, e = f.s.Confirm(context.Background(), f.session.Token, ProviderGitHub, start.FlowID, out.Provider.ConfigVersion)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func (f *coreFixture) login(t *testing.T) *auth.Session {
	t.Helper()
	start, e := f.s.Start(context.Background(), ProviderGitHub, "login", "", "/runs?status=failed", 0)
	if e != nil {
		t.Fatal(e)
	}
	out, e := f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "", "fixture-code", false)
	if e != nil {
		t.Fatal(e)
	}
	return out.Session
}
func TestCoreDefaultVisibilityCASPersistenceAndPrivacy(t *testing.T) {
	f := coreSetup(t)
	ctx := context.Background()
	public, e := f.s.PublicProviders(ctx)
	if e != nil || len(public) != 5 {
		t.Fatal(e)
	}
	for _, p := range public {
		if !p.Visible || p.Available || p.Reason != "not_configured" {
			t.Fatalf("default %+v", p)
		}
	}
	f.save(t)
	settings, e := f.s.Settings(ctx, f.session.Token)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(settings)
	if strings.Contains(string(body), "canary-local") || strings.Contains(string(body), "secret_cipher") {
		t.Fatal("secret exposed")
	}
	choices := map[string]bool{"github": false, "gitlab": true, "gitee": false, "google": true, "microsoft": false}
	next, e := f.s.SaveVisibility(ctx, f.session.Token, settings.VisibilityVersion, choices)
	if e != nil {
		t.Fatal(e)
	}
	if next.VisibilityVersion != settings.VisibilityVersion+1 {
		t.Fatal("revision")
	}
	if _, e = f.s.SaveVisibility(ctx, f.session.Token, settings.VisibilityVersion, choices); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale %v", e)
	}
	after, e := New(f.db.DB, f.s.vault, f.a, Config{PublicURL: f.s.origin}).Settings(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range after.Providers {
		if p.Visible != choices[p.Provider] {
			t.Fatal("visibility not durable")
		}
	}
	if f.adapter.calls.Load() != 0 {
		t.Fatal("settings caused external request")
	}
}
func TestCoreBindingThenLoginAndExactIdentity(t *testing.T) {
	f := coreSetup(t)
	bound := f.bind(t)
	if !bound.Provider.Enabled || bound.Provider.Binding == nil {
		t.Fatal("not bound")
	}
	sess := f.login(t)
	if sess.AuthSource != ProviderGitHub || sess.PasswordVerifiedAt != nil || sess.AbsoluteExpiresAt == nil || sess.ExpiresAt.Sub(sess.CreatedAt) != 24*time.Hour {
		t.Fatalf("source session %+v", sess)
	}
	if _, e := f.a.Verify(sess.Token); e != nil {
		t.Fatal(e)
	}
	f.adapter.identity.Subject = "90071992547409931235"
	start, e := f.s.Start(context.Background(), ProviderGitHub, "login", "", "/", 0)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "", "other-code", false)
	if !errors.Is(e, ErrIdentityDenied) {
		t.Fatalf("stranger %v", e)
	}
	var cipher []byte
	var status string
	if e = f.db.DB.QueryRow(`SELECT verifier_cipher,status FROM login_flows WHERE flow_id=?`, start.FlowID).Scan(&cipher, &status); e != nil || len(cipher) != 0 || status != "failed" {
		t.Fatalf("failed cleanup %s %v", status, e)
	}
}
func TestCoreSingleExchangeReplayAndBrowserBinding(t *testing.T) {
	f := coreSetup(t)
	f.bind(t)
	start, e := f.s.Start(context.Background(), ProviderGitHub, "login", "", "/", 0)
	if e != nil {
		t.Fatal(e)
	}
	state := stateOf(t, start)
	base := f.adapter.calls.Load()
	if _, e = f.s.Complete(context.Background(), ProviderGitHub, state, strings.Repeat("0", 64), "", "code", false); !errors.Is(e, ErrFlowInvalid) {
		t.Fatalf("browser %v", e)
	}
	if f.adapter.calls.Load() != base {
		t.Fatal("bad browser exchanged")
	}
	var wg sync.WaitGroup
	var success atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := f.s.Complete(context.Background(), ProviderGitHub, state, start.BrowserNonce, "", "code", false); e == nil {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 || f.adapter.calls.Load() != base+1 {
		t.Fatalf("single-use success=%d calls=%d", success.Load(), f.adapter.calls.Load()-base)
	}
	if _, e = f.s.Complete(context.Background(), ProviderGitHub, state, start.BrowserNonce, "", "code", false); !errors.Is(e, ErrFlowConsumed) {
		t.Fatalf("replay %v", e)
	}
}
func TestCoreUncertainExchangeNeverRetries(t *testing.T) {
	f := coreSetup(t)
	f.bind(t)
	f.adapter.err = ErrResultUncertain
	base := f.adapter.calls.Load()
	start, _ := f.s.Start(context.Background(), ProviderGitHub, "login", "", "/", 0)
	if _, e := f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "", "code", false); !errors.Is(e, ErrResultUncertain) {
		t.Fatal(e)
	}
	if _, e := f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "", "code", false); !errors.Is(e, ErrFlowConsumed) {
		t.Fatal(e)
	}
	if f.adapter.calls.Load() != base+1 {
		t.Fatal("exchange retried")
	}
}
func TestCoreGenerationChangeDuringExchangeCannotCreateSession(t *testing.T) {
	f := coreSetup(t)
	bound := f.bind(t)
	f.adapter.entered = make(chan struct{}, 1)
	f.adapter.release = make(chan struct{})
	start, _ := f.s.Start(context.Background(), ProviderGitHub, "login", "", "/", 0)
	done := make(chan error, 1)
	go func() {
		_, e := f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "", "code", false)
		done <- e
	}()
	<-f.adapter.entered
	if _, e := f.s.SetEnabled(context.Background(), f.session.Token, ProviderGitHub, bound.Provider.ConfigVersion, false); e != nil {
		t.Fatal(e)
	}
	close(f.adapter.release)
	if e := <-done; e == nil {
		t.Fatal("stale callback succeeded")
	}
	var n int
	f.db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_source='github'`).Scan(&n)
	if n != 0 {
		t.Fatal("stale session persisted")
	}
}
func TestCoreVisibilityDoesNotRevokeButDisablePhysicallyDeletes(t *testing.T) {
	f := coreSetup(t)
	bound := f.bind(t)
	sess := f.login(t)
	ctx, release := f.a.Lease(context.Background(), sess)
	defer release()
	values := map[string]bool{}
	for _, p := range Providers {
		values[p] = false
	}
	if _, e := f.s.SaveVisibility(context.Background(), f.session.Token, 1, values); e != nil {
		t.Fatal(e)
	}
	if _, e := f.a.Verify(sess.Token); e != nil {
		t.Fatal("hidden revoked session")
	}
	if _, e := f.s.SetEnabled(context.Background(), f.session.Token, ProviderGitHub, bound.Provider.ConfigVersion, false); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("revocation broadcast missing")
	}
	var n int
	f.db.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token=?`, sess.Token).Scan(&n)
	if n != 0 {
		t.Fatal("source not physically deleted")
	}
	if _, e := f.a.Verify(f.session.Token); e != nil {
		t.Fatal("password session revoked")
	}
}
func TestCorePasswordProofSessionBindingCandidateExpiryAndConfigRace(t *testing.T) {
	f := coreSetup(t)
	out := f.save(t)
	start, e := f.s.Start(context.Background(), ProviderGitHub, "bind", f.session.Token, "/settings/account", out.Provider.ConfigVersion)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "wrong-session", "code", false); !errors.Is(e, ErrFlowInvalid) {
		t.Fatal(e)
	}
	if _, e = f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, f.session.Token, "code", false); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Pending(context.Background(), "wrong-session", start.FlowID); !errors.Is(e, ErrFlowInvalid) {
		t.Fatal(e)
	}
	f.clock.add(5 * time.Minute)
	if _, e = f.s.Confirm(context.Background(), f.session.Token, ProviderGitHub, start.FlowID, out.Provider.ConfigVersion); !errors.Is(e, ErrReauthRequired) {
		t.Fatalf("recent password %v", e)
	}
	if _, e = f.a.Reauthenticate(f.session.Token, "fixture-password"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Confirm(context.Background(), f.session.Token, ProviderGitHub, start.FlowID, out.Provider.ConfigVersion); !errors.Is(e, ErrFlowExpired) {
		t.Fatalf("expired %v", e)
	}
}
func TestCoreAbsoluteSessionCapAndPasswordSliding(t *testing.T) {
	f := coreSetup(t)
	f.bind(t)
	external := f.login(t)
	f.clock.add(23 * time.Hour)
	refreshed, e := f.a.Verify(external.Token)
	if e != nil || !refreshed.ExpiresAt.Equal(external.ExpiresAt) {
		t.Fatalf("absolute cap %v", e)
	}
	native, e := f.a.Verify(f.session.Token)
	if e != nil || native.ExpiresAt.Sub(f.clock.Now()) != 7*24*time.Hour {
		t.Fatalf("native sliding %v", e)
	}
	f.clock.add(time.Hour)
	if _, e = f.a.Verify(external.Token); !errors.Is(e, auth.ErrSessionNotFound) {
		t.Fatalf("24h boundary %v", e)
	}
}
func TestCorePasswordChangeInvalidatesPendingAndPreservesCurrent(t *testing.T) {
	f := coreSetup(t)
	f.bind(t)
	external := f.login(t)
	start, _ := f.s.Start(context.Background(), ProviderGitHub, "login", "", "/", 0)
	if e := f.a.ChangePassword("fixture-password", "new-fixture-password", f.session.Token); e != nil {
		t.Fatal(e)
	}
	if _, e := f.a.Verify(f.session.Token); e != nil {
		t.Fatal(e)
	}
	if _, e := f.a.Verify(external.Token); !errors.Is(e, auth.ErrSessionNotFound) {
		t.Fatal("other source retained")
	}
	base := f.adapter.calls.Load()
	if _, e := f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, "", "code", false); e == nil {
		t.Fatal("old pending accepted")
	}
	if f.adapter.calls.Load() != base {
		t.Fatal("old generation exchanged")
	}
}
func TestCoreSecretRotationAndApplicationChange(t *testing.T) {
	f := coreSetup(t)
	bound := f.bind(t)
	session := f.login(t)
	secret := "rotated-local-secret"
	rotated, e := f.s.SaveApp(context.Background(), f.session.Token, ProviderGitHub, AppInput{ExpectedVersion: bound.Provider.ConfigVersion, ClientID: bound.Provider.ClientID, ClientSecret: &secret})
	if e != nil {
		t.Fatal(e)
	}
	if rotated.Provider.Binding == nil {
		t.Fatal("rotation removed same-app binding")
	}
	if _, e = f.a.Verify(session.Token); !errors.Is(e, auth.ErrSessionNotFound) {
		t.Fatal("rotation retained old session")
	}
	if _, e = f.s.SaveApp(context.Background(), f.session.Token, ProviderGitHub, AppInput{ExpectedVersion: rotated.Provider.ConfigVersion, ClientID: "different-app"}); !errors.Is(e, ErrBadRequest) {
		t.Fatalf("changed client without secret %v", e)
	}
	changed, e := f.s.SaveApp(context.Background(), f.session.Token, ProviderGitHub, AppInput{ExpectedVersion: rotated.Provider.ConfigVersion, ClientID: "different-app", ClientSecret: &secret})
	if e != nil || changed.Provider.Binding != nil || changed.Provider.Enabled {
		t.Fatalf("new app %+v %v", changed, e)
	}
}
func TestCoreOriginAndReturnTargetValidation(t *testing.T) {
	for _, raw := range []string{"https://app.example/path", "https://user@app.example", "http://app.example", "http://127.0.0.1", "https://app.example?x=1", "https://app.example/#fragment"} {
		if _, e := PublicOrigin(raw, false); e == nil {
			t.Errorf("origin accepted %q", raw)
		}
	}
	if _, e := PublicOrigin("http://127.0.0.1:18090", true); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"//evil.example", "https://evil.example", "/\\evil", "/%2f%2fevil", "/%252f%252fevil", "/\nlocation", "/%00evil"} {
		if _, e := SafeReturnTo(raw); e == nil {
			t.Errorf("target accepted %q", raw)
		}
	}
	if got, e := SafeReturnTo("/runs?status=failed"); e != nil || got != "/runs?status=failed" {
		t.Fatal(e)
	}
}

func TestCoreInvalidSecretAndChangedTenantRequireNewCredentials(t *testing.T) {
	f := coreSetup(t)
	ctx := context.Background()
	for _, bad := range []string{" secret", "secret ", "secret\nvalue", strings.Repeat("a", 8193)} {
		if _, e := f.s.SaveApp(ctx, f.session.Token, ProviderGitHub, AppInput{ExpectedVersion: 1, ClientID: "app", ClientSecret: &bad}); !errors.Is(e, ErrBadRequest) {
			t.Fatalf("invalid secret accepted: %v", e)
		}
	}
	secret := "valid-local-secret"
	first, e := f.s.SaveApp(ctx, f.session.Token, ProviderMicrosoft, AppInput{ExpectedVersion: 1, ClientID: "app", ClientSecret: &secret, MicrosoftTenant: "common"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.SaveApp(ctx, f.session.Token, ProviderMicrosoft, AppInput{ExpectedVersion: first.Provider.ConfigVersion, ClientID: "app", MicrosoftTenant: "organizations"}); !errors.Is(e, ErrBadRequest) {
		t.Fatalf("changed tenant without new secret: %v", e)
	}
}
func TestCoreExpiredCancelledAndCrossProviderFlowsDoNotExchange(t *testing.T) {
	f := coreSetup(t)
	f.bind(t)
	ctx := context.Background()
	base := f.adapter.calls.Load()
	cancelled, _ := f.s.Start(ctx, ProviderGitHub, "login", "", "/", 0)
	if _, e := f.s.Complete(ctx, ProviderGitHub, stateOf(t, cancelled), cancelled.BrowserNonce, "", "", true); !errors.Is(e, ErrProviderRejected) {
		t.Fatal(e)
	}
	if _, e := f.s.Complete(ctx, ProviderGitHub, stateOf(t, cancelled), cancelled.BrowserNonce, "", "code", false); !errors.Is(e, ErrFlowConsumed) {
		t.Fatal(e)
	}
	expired, _ := f.s.Start(ctx, ProviderGitHub, "login", "", "/", 0)
	if _, e := f.s.Complete(ctx, ProviderGitLab, stateOf(t, expired), expired.BrowserNonce, "", "code", false); !errors.Is(e, ErrFlowInvalid) {
		t.Fatal(e)
	}
	f.clock.add(10 * time.Minute)
	if _, e := f.s.Complete(ctx, ProviderGitHub, stateOf(t, expired), expired.BrowserNonce, "", "code", false); !errors.Is(e, ErrFlowExpired) {
		t.Fatal(e)
	}
	if f.adapter.calls.Load() != base {
		t.Fatal("cancelled/expired/wrong provider exchanged")
	}
}
func TestCoreLeaseRechecksDBWhenRevocationIsOutsideProcess(t *testing.T) {
	f := coreSetup(t)
	f.bind(t)
	sess := f.login(t)
	ctx, release := f.a.Lease(context.Background(), sess)
	defer release()
	if _, e := f.db.DB.Exec(`DELETE FROM sessions WHERE token=?`, sess.Token); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ctx.Done():
	case <-time.After(25 * time.Second):
		t.Fatal("lease exceeded thirty-second revocation bound")
	}
}
