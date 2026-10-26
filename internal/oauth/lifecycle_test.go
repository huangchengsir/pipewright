package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type lifecycleFixture struct {
	s                      *service
	raw                    vault.Vault
	managed                vault.Vault
	db                     *sql.DB
	id, provider, redirect string
	now                    atomic.Int64
	refreshes              atomic.Int32
	exchanges              atomic.Int32
	mode                   atomic.Value
	account                atomic.Value
	tokenBody              string
	blockRefresh           chan struct{}
	refreshEntered         chan struct{}
	once                   sync.Once
}

func newLifecycleFixture(t *testing.T, provider, body string) *lifecycleFixture {
	t.Helper()
	f := &lifecycleFixture{provider: provider, redirect: "https://pipewright.test/api/oauth/" + provider + "/callback", tokenBody: body, refreshEntered: make(chan struct{})}
	f.now.Store(time.Now().UnixMilli())
	f.mode.Store("")
	f.account.Store("9007199254740993")
	transport := lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		text := `{"id":` + f.account.Load().(string) + `,"login":"alice","username":"alice"}`
		status := 200
		if r.Method == http.MethodPost {
			formBytes, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(formBytes))
			if values.Get("grant_type") == "refresh_token" {
				f.refreshes.Add(1)
				f.once.Do(func() { close(f.refreshEntered) })
				if f.blockRefresh != nil {
					select {
					case <-f.blockRefresh:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				}
				switch f.mode.Load().(string) {
				case "eof":
					return nil, io.ErrUnexpectedEOF
				case "invalid":
					text = `{"error":"invalid_grant","error_description":"synthetic-secret-must-not-escape"}`
					status = 400
				case "malformed":
					text = `{"access_token":"fixture-access-2","expires_in":false}`
				case "third":
					text = `{"access_token":"fixture-access-3","refresh_token":"fixture-refresh-3","expires_in":90,"token_type":"bearer"}`
				default:
					text = `{"access_token":"fixture-access-2","refresh_token":"fixture-refresh-2","expires_in":90,"token_type":"bearer"}`
				}
			} else {
				f.exchanges.Add(1)
				if f.mode.Load().(string) == "authorize_eof" {
					return nil, io.ErrUnexpectedEOF
				}
				text = f.tokenBody
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(text)), Header: http.Header{}}, nil
	})
	svc, v, db, _ := newService(t, &http.Client{Transport: transport})
	f.s = svc.(*service)
	f.raw = v
	f.managed = ManagedVault(svc, v)
	f.db = db
	f.s.lifecycle.now = func() time.Time { return time.UnixMilli(f.now.Load()).UTC() }
	f.s.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	base := ""
	if provider == ProviderGitea || provider == ProviderCustom {
		base = "http://127.0.0.1/fixture"
	}
	_, err := f.s.SaveApp(ctx(), SaveAppInput{Provider: provider, ClientID: "fixture-client", ClientSecret: strp("fixture-app-secret"), BaseURL: base, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.id = f.authorize(t, "", 0, false)
	return f
}
func (f *lifecycleFixture) authorize(t *testing.T, id string, revision int64, adopt bool) string {
	t.Helper()
	raw, err := f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, id, revision, adopt)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	newID, _, err := f.s.CompleteAuthorization(ctx(), "session", f.provider, u.Query().Get("state"), "fixture-code", f.redirect)
	if err != nil {
		t.Fatal(err)
	}
	return newID
}
func (f *lifecycleFixture) acquire(t *testing.T) (vault.GitAuth, func()) {
	t.Helper()
	auth, done, err := vault.AcquireGitAuth(ctx(), f.managed, f.id)
	if err != nil {
		t.Fatal(err)
	}
	return auth, done
}
func (f *lifecycleFixture) binding(t *testing.T) *vault.OAuthBinding {
	t.Helper()
	b, e := f.s.lifecycle.repo.Binding(ctx(), f.id)
	if e != nil || b == nil {
		t.Fatalf("missing binding: %v", e)
	}
	return b
}

const rotatingFixture = `{"access_token":"fixture-access-1","refresh_token":"fixture-refresh-1","expires_in":90,"token_type":"bearer"}`

func TestManagedOAuthRefreshWaitsForActualGenerationUsers(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	old, releaseOld := f.acquire(t)
	if old.Token != "fixture-access-1" {
		t.Fatal("initial token mismatch")
	}
	f.now.Add(int64(61 * time.Second / time.Millisecond))
	type result struct {
		auth vault.GitAuth
		done func()
		err  error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			c, cancel := context.WithTimeout(ctx(), 10*time.Second)
			defer cancel()
			a, d, e := vault.AcquireGitAuth(c, f.managed, f.id)
			results <- result{a, d, e}
		}()
	}
	select {
	case <-f.refreshEntered:
		t.Fatal("refresh invalidated an in-use generation")
	case <-time.After(100 * time.Millisecond):
	}
	releaseOld()
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatal(r.err)
			}
			if r.auth.Token != "fixture-access-2" {
				t.Fatal("new users did not share refreshed generation")
			}
			r.done()
			r.done()
		case <-time.After(12 * time.Second):
			t.Fatal("waiting users did not finish")
		}
	}
	if f.refreshes.Load() != 1 {
		t.Fatalf("refresh dispatch count=%d", f.refreshes.Load())
	}
	b := f.binding(t)
	if b.Revision != 2 || b.CredentialID != f.id {
		t.Fatal("ID/generation changed incorrectly")
	}
	plain, e := f.raw.OpenSecret(b.RefreshCiphertext)
	if e != nil || string(plain) != "fixture-refresh-2" {
		t.Fatal("rotating pair not saved together")
	}
	var n int
	if e = f.db.QueryRow("SELECT COUNT(*) FROM oauth_credential_uses WHERE credential_id=?", f.id).Scan(&n); e != nil || n != 0 {
		t.Fatalf("generation references leaked: %d %v", n, e)
	}
}

func TestManagedOAuthFiveProvidersUseOnlyDeclaredCapabilities(t *testing.T) {
	for _, provider := range []string{ProviderGitee, ProviderGitHub, ProviderGitLab, ProviderCustom, ProviderGitea} {
		t.Run(provider, func(t *testing.T) {
			f := newLifecycleFixture(t, provider, `{"access_token":"fixture-static-access","token_type":"bearer"}`)
			f.now.Add(int64(365 * 24 * time.Hour / time.Millisecond))
			a, done := f.acquire(t)
			done()
			if a.Token != "fixture-static-access" || f.refreshes.Load() != 0 {
				t.Fatal("undeclared expiry triggered a refresh")
			}
			b := f.binding(t)
			if b.ExpiresAt != nil || len(b.RefreshCiphertext) != 0 {
				t.Fatal("inferred missing capability")
			}
		})
	}
}

func TestManagedOAuthUncertainAndInvalidGrantNeverAutoRetry(t *testing.T) {
	for _, mode := range []string{"eof", "malformed", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			f := newLifecycleFixture(t, ProviderGitLab, rotatingFixture)
			f.mode.Store(mode)
			f.now.Add(61000)
			_, _, err := vault.AcquireGitAuth(ctx(), f.managed, f.id)
			want := vault.ErrOAuthUncertain
			if mode == "invalid" {
				want = vault.ErrOAuthReauthorize
			}
			if !errors.Is(err, want) {
				t.Fatalf("wrong failure category: %v", err)
			}
			_, _, again := vault.AcquireGitAuth(ctx(), f.managed, f.id)
			if !errors.Is(again, want) {
				t.Fatalf("second read category: %v", again)
			}
			if f.refreshes.Load() != 1 {
				t.Fatal("uncertain/failed rotating grant was resent")
			}
			token, e := f.raw.Reveal(f.id)
			if e != nil || token != "fixture-access-1" {
				t.Fatal("failed exchange altered existing access token")
			}
			list, e := f.managed.List()
			if e != nil {
				t.Fatal(e)
			}
			encoded, _ := json.Marshal(list)
			if strings.Contains(string(encoded), "fixture-refresh") || strings.Contains(string(encoded), "synthetic-secret") {
				t.Fatal("credential metadata exposed a secret")
			}
		})
	}
}

func TestManagedOAuthExactAccountAndBoundReauthorization(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	name, scope := "retained name", "project:retained"
	if _, e := f.raw.Update(f.id, vault.UpdateInput{Name: &name, Scope: &scope}); e != nil {
		t.Fatal(e)
	}
	for _, scenario := range []string{"session", "provider", "account", "replay"} {
		t.Run(scenario, func(t *testing.T) {
			b := f.binding(t)
			raw, e := f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, f.id, b.Revision, false)
			if e != nil {
				t.Fatal(e)
			}
			u, _ := url.Parse(raw)
			state := u.Query().Get("state")
			session, provider := "session", f.provider
			switch scenario {
			case "session":
				session = "different"
			case "provider":
				provider = ProviderGitHub
			case "account":
				f.account.Store("9007199254740992")
				defer f.account.Store("9007199254740993")
			case "replay":
				if _, _, e = f.s.CompleteAuthorization(ctx(), session, provider, state, "code", f.redirect); e != nil {
					t.Fatal(e)
				}
			}
			before, _ := f.raw.Reveal(f.id)
			_, _, e = f.s.CompleteAuthorization(ctx(), session, provider, state, "code", f.redirect)
			if !errors.Is(e, vault.ErrOAuthIdentity) {
				t.Fatalf("unbound/cross-account authorization accepted: %v", e)
			}
			after, _ := f.raw.Reveal(f.id)
			if before != after {
				t.Fatal("rejected authorization changed token")
			}
		})
	}
	b := f.binding(t)
	if id := f.authorize(t, f.id, b.Revision, false); id != f.id {
		t.Fatal("reconnect created another ID")
	}
	list, e := f.raw.List()
	if e != nil || len(list) != 1 || list[0].Name != name || list[0].Scope != scope {
		t.Fatal("reconnect changed metadata or duplicated the credential")
	}
}

func TestManagedOAuthExplicitLegacyAdoptionAndSecretDetach(t *testing.T) {
	f := newLifecycleFixture(t, ProviderCustom, rotatingFixture)
	old, e := f.raw.Create(vault.CreateInput{Name: "not-a-provider-name", Type: vault.TypeGitToken, Secret: "fixture-old"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, old.ID, 0, false); !errors.Is(e, vault.ErrOAuthIdentity) {
		t.Fatal("legacy identity inferred without confirmation")
	}
	if id := f.authorize(t, old.ID, 0, true); id != old.ID {
		t.Fatal("adoption replaced ID")
	}
	secret := "new-user-supplied-secret"
	if _, e = f.raw.Update(old.ID, vault.UpdateInput{Secret: &secret}); !errors.Is(e, vault.ErrOAuthDetachRequired) {
		t.Fatal("secret rotation silently retained OAuth")
	}
	if _, e = f.raw.Update(old.ID, vault.UpdateInput{Secret: &secret, DetachOAuth: true}); e != nil {
		t.Fatal(e)
	}
	b, e := f.s.lifecycle.repo.Binding(ctx(), old.ID)
	if e != nil || b != nil {
		t.Fatal("detaching left refresh material")
	}
}

func TestManagedOAuthLateResponsesCannotResurrectOrOverwrite(t *testing.T) {
	for _, action := range []string{"delete", "detach", "application"} {
		t.Run(action, func(t *testing.T) {
			f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
			f.blockRefresh = make(chan struct{})
			f.now.Add(61000)
			result := make(chan error, 1)
			go func() {
				_, done, e := vault.AcquireGitAuth(ctx(), f.managed, f.id)
				if done != nil {
					done()
				}
				result <- e
			}()
			select {
			case <-f.refreshEntered:
			case <-time.After(10 * time.Second):
				t.Fatal("refresh did not begin")
			}
			switch action {
			case "delete":
				if e := f.raw.Delete(f.id); e != nil {
					t.Fatal(e)
				}
			case "detach":
				secret := "fixture-manual-secret"
				if _, e := f.raw.Update(f.id, vault.UpdateInput{Secret: &secret, DetachOAuth: true}); e != nil {
					t.Fatal(e)
				}
			case "application":
				if _, e := f.s.SaveApp(ctx(), SaveAppInput{Provider: f.provider, ClientID: "changed-client", ClientSecret: strp("changed-app-secret"), BaseURL: "http://127.0.0.1/other", Enabled: true}); e != nil {
					t.Fatal(e)
				}
			}
			close(f.blockRefresh)
			select {
			case e := <-result:
				if !errors.Is(e, vault.ErrOAuthUncertain) {
					t.Fatalf("late response category: %v", e)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("late response did not finish")
			}
			if action == "delete" {
				var n int
				_ = f.db.QueryRow("SELECT COUNT(*) FROM credentials WHERE id=?", f.id).Scan(&n)
				if n != 0 {
					t.Fatal("late response resurrected credential")
				}
			}
			if action == "detach" {
				got, e := f.raw.Reveal(f.id)
				if e != nil || got != "fixture-manual-secret" {
					t.Fatal("late response overwrote manual secret")
				}
			}
		})
	}
}

func TestManagedOAuthApplicationChangeDoesNotSendOldRefresh(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	f.now.Add(61000)
	if _, e := f.s.SaveApp(ctx(), SaveAppInput{Provider: f.provider, ClientID: "changed", ClientSecret: strp("fixture-new-app"), BaseURL: "http://127.0.0.1/another", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := vault.AcquireGitAuth(ctx(), f.managed, f.id); !errors.Is(e, vault.ErrOAuthIdentity) {
		t.Fatal("changed app accepted old binding")
	}
	if f.refreshes.Load() != 0 {
		t.Fatal("old refresh sent after application identity changed")
	}
}

func TestManagedOAuthStorageFailureKeepsPairAndUnknownClaim(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitLab, rotatingFixture)
	statement := `CREATE TRIGGER oauth_fixture_fail BEFORE UPDATE OF ciphertext ON credentials BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`
	if store.DialectOf(f.db) == store.MySQL {
		statement = `CREATE TRIGGER oauth_fixture_fail BEFORE UPDATE ON credentials FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='fixture write failure'`
	}
	if _, e := f.db.Exec(statement); e != nil {
		t.Fatal(e)
	}
	defer func() { _, _ = f.db.Exec("DROP TRIGGER oauth_fixture_fail") }()
	f.now.Add(61000)
	if _, _, e := vault.AcquireGitAuth(ctx(), f.managed, f.id); !errors.Is(e, vault.ErrOAuthUncertain) {
		t.Fatalf("failed save category: %v", e)
	}
	b := f.binding(t)
	token, e := f.raw.Reveal(f.id)
	refresh, re := f.raw.OpenSecret(b.RefreshCiphertext)
	if e != nil || re != nil || token != "fixture-access-1" || string(refresh) != "fixture-refresh-1" || b.State != vault.OAuthUncertain {
		t.Fatal("storage failure left a torn token pair or clear status")
	}
	if _, _, e = vault.AcquireGitAuth(ctx(), f.managed, f.id); !errors.Is(e, vault.ErrOAuthUncertain) || f.refreshes.Load() != 1 {
		t.Fatal("failed save repeated refresh")
	}
}

func TestManagedOAuthMetadataAndMaintenanceNeverBypassUseLifetime(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitHub, rotatingFixture)
	_, done := f.acquire(t)
	if e := maintenance.Fence(ctx(), f.db, "fixture-install"); !errors.Is(e, maintenance.ErrBlocked) {
		t.Fatal("install admitted while Git call held its lease")
	}
	done()
	f.now.Add(61000)
	for i := 0; i < 2; i++ {
		if _, e := f.managed.List(); e != nil {
			t.Fatal(e)
		}
		if _, e := f.raw.Reveal(f.id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := f.raw.GetGitAuth(f.id); !errors.Is(e, vault.ErrGitLeaseRequired) {
		t.Fatal("unscoped read bypassed managed use")
	}
	if f.refreshes.Load() != 0 {
		t.Fatal("metadata/reveal caused network refresh")
	}
	if e := maintenance.Fence(ctx(), f.db, "fixture-install"); e != nil {
		t.Fatal(e)
	}
	defer maintenance.Unfence(ctx(), f.db, "fixture-install")
	if _, _, e := vault.AcquireGitAuth(ctx(), f.managed, f.id); e == nil {
		t.Fatal("Git operation bypassed install fence")
	}
	if _, e := f.s.Exchange(ctx(), f.provider, "code", f.redirect); e == nil {
		t.Fatal("OAuth exchange bypassed install fence")
	}
}

func TestManagedOAuthForeignClaimsAreNeverReclaimedByTime(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	f.now.Add(61000)
	acquired, e := f.s.lifecycle.repo.TryAcquire(ctx(), f.id, "different-process", time.UnixMilli(f.now.Load()))
	if e != nil || acquired.Action != "refresh" {
		t.Fatal("fixture claim failed", e)
	}
	f.now.Add(int64(365 * 24 * time.Hour / time.Millisecond))
	if _, _, e = vault.AcquireGitAuth(ctx(), f.managed, f.id); !errors.Is(e, vault.ErrOAuthUncertain) {
		t.Fatal("unknown owner was reclaimed")
	}
	if f.refreshes.Load() != 0 || f.binding(t).ClaimOwner != "different-process" {
		t.Fatal("foreign claim changed")
	}
	list, e := f.managed.List()
	if e != nil || list[0].OAuth.Status != vault.OAuthUncertain {
		t.Fatal("unresolved claim not shown as uncertain")
	}
}

func TestOAuthTokenResponseStrictExpiryAndExactIdentity(t *testing.T) {
	for _, value := range []string{"false", "-1", "0", "1.5", "1e3", "9223372036854775807"} {
		if _, e := parseTokenResponse([]byte(`{"access_token":"fixture","expires_in":`+value+`}`), time.Now()); e == nil {
			t.Fatalf("invalid expiry accepted: %s", value)
		}
	}
	for _, value := range []string{"90", `"90"`} {
		p, e := parseTokenResponse([]byte(`{"access_token":"fixture","expires_in":`+value+`}`), time.Unix(1, 0))
		if e != nil || p.RefreshAfter.Sub(time.Unix(1, 0)) != 60*time.Second {
			t.Fatal("declared refresh lead incorrect", e)
		}
	}
	for _, id := range []string{"9007199254740992", "9007199254740993"} {
		got, e := vault.ParseAccountID(json.Number(id))
		if e != nil || got != id {
			t.Fatal("immutable account identity rounded")
		}
	}
	if _, e := vault.ParseAccountID(float64(9007199254740992)); e == nil {
		t.Fatal("floating point account identity accepted")
	}
}

func TestManagedOAuthMissingExpiryOrRefreshDoesNotInventCapability(t *testing.T) {
	for _, body := range []string{`{"access_token":"fixture","refresh_token":"fixture-refresh"}`, `{"access_token":"fixture","expires_in":90}`} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			f := newLifecycleFixture(t, ProviderGitea, body)
			f.now.Add(91000)
			_, done, e := vault.AcquireGitAuth(ctx(), f.managed, f.id)
			if done != nil {
				done()
			}
			if strings.Contains(body, "expires_in") && !errors.Is(e, vault.ErrOAuthReauthorize) {
				t.Fatalf("expired no-refresh credential category: %v", e)
			}
			if !strings.Contains(body, "expires_in") && e != nil {
				t.Fatal(e)
			}
			if f.refreshes.Load() != 0 {
				t.Fatal("missing capability caused extra request")
			}
		})
	}
}

func TestManagedOAuthReauthorizationChecksUsersAgainBeforeExchange(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
			id, revision, adopt := f.id, int64(1), false
			if legacy {
				c, e := f.raw.Create(vault.CreateInput{Name: "older", Type: vault.TypeGitToken, Secret: "fixture-older"})
				if e != nil {
					t.Fatal(e)
				}
				id, revision, adopt = c.ID, 0, true
			}
			raw, e := f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, id, revision, adopt)
			if e != nil {
				t.Fatal(e)
			}
			u, _ := url.Parse(raw)
			_, release, e := vault.AcquireGitAuth(ctx(), f.managed, id)
			if e != nil {
				t.Fatal(e)
			}
			before := f.exchanges.Load()
			_, _, e = f.s.CompleteAuthorization(ctx(), "session", f.provider, u.Query().Get("state"), "code", f.redirect)
			if !errors.Is(e, vault.ErrOAuthBusy) {
				t.Fatalf("callback accepted a Git user admitted during browser authorization: %v", e)
			}
			if f.exchanges.Load() != before {
				t.Fatal("provider exchanged before the Git user ended")
			}
			release()
			f.authorize(t, id, revision, adopt)
		})
	}
}

func TestManagedOAuthReauthorizationLostResponsePersistsUncertainty(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	b := f.binding(t)
	raw, e := f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, f.id, b.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(raw)
	f.mode.Store("authorize_eof")
	_, _, e = f.s.CompleteAuthorization(ctx(), "session", f.provider, u.Query().Get("state"), "code", f.redirect)
	if !errors.Is(e, vault.ErrOAuthUncertain) {
		t.Fatal("lost authorization response not uncertain", e)
	}
	var state string
	if e = f.db.QueryRow("SELECT state FROM oauth_authorizations WHERE credential_id=?", f.id).Scan(&state); e != nil || state != vault.OAuthUncertain {
		t.Fatal("uncertainty was not durable", e)
	}
	_, _, e = vault.AcquireGitAuth(ctx(), f.managed, f.id)
	if !errors.Is(e, vault.ErrOAuthUncertain) {
		t.Fatal("unknown callback result reused old credential", e)
	}
	if f.exchanges.Load() != 2 || f.refreshes.Load() != 0 {
		t.Fatal("unknown authorization was resent")
	}
	f.mode.Store("")
	f.authorize(t, f.id, b.Revision, false)
}

func TestManagedOAuthExplicitFreshAuthorizationRecoversUnknownClaims(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
			id, revision, adopt := f.id, int64(1), false
			if legacy {
				c, e := f.raw.Create(vault.CreateInput{Name: "legacy", Type: vault.TypeGitToken, Secret: "fixture-legacy"})
				if e != nil {
					t.Fatal(e)
				}
				id, revision, adopt = c.ID, 0, true
			}
			snapshot, e := f.s.lifecycle.repo.Target(ctx(), id, adopt)
			if e != nil {
				t.Fatal(e)
			}
			old, e := f.s.lifecycle.repo.ClaimAuthorization(ctx(), snapshot, "unknown-process")
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = vault.AcquireGitAuth(ctx(), f.managed, id); !errors.Is(e, vault.ErrOAuthUncertain) {
				t.Fatal("unknown claim automatically reclaimed")
			}
			f.authorize(t, id, revision, adopt)
			newer, e := f.s.lifecycle.repo.Binding(ctx(), id)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.lifecycle.repo.Authorize(ctx(), *newer, vault.OAuthTokens{AccessToken: "fixture-late"}, old); !errors.Is(e, vault.ErrConflict) {
				t.Fatal("late unknown callback overwrote fresh authorization", e)
			}
			value, e := f.raw.Reveal(id)
			if e != nil || value != "fixture-access-1" {
				t.Fatal("late old claim altered current credential")
			}
		})
	}
}

func TestManagedOAuthKnownInFlightRefreshBlocksReauthorization(t *testing.T) {
	f := newLifecycleFixture(t, ProviderGitea, rotatingFixture)
	f.blockRefresh = make(chan struct{})
	f.now.Add(61000)
	finished := make(chan error, 1)
	go func() {
		_, done, e := vault.AcquireGitAuth(ctx(), f.managed, f.id)
		if done != nil {
			done()
		}
		finished <- e
	}()
	select {
	case <-f.refreshEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("refresh not started")
	}
	b := f.binding(t)
	raw, e := f.s.BeginAuthorization(ctx(), "session", f.provider, f.redirect, f.id, b.Revision, false)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(raw)
	before := f.exchanges.Load()
	if _, _, e = f.s.CompleteAuthorization(ctx(), "session", f.provider, u.Query().Get("state"), "code", f.redirect); !errors.Is(e, vault.ErrOAuthBusy) {
		t.Fatal("live local exchange was replaced", e)
	}
	if f.exchanges.Load() != before {
		t.Fatal("reauthorization sent during live refresh")
	}
	close(f.blockRefresh)
	if e = <-finished; e != nil {
		t.Fatal(e)
	}
}

func TestManagedOAuthGitUsernamesFollowProviderProtocol(t *testing.T) {
	for _, provider := range []string{ProviderGitee, ProviderGitHub, ProviderGitLab, ProviderCustom, ProviderGitea} {
		t.Run(provider, func(t *testing.T) {
			f := newLifecycleFixture(t, provider, rotatingFixture)
			auth, done := f.acquire(t)
			done()
			want := "alice"
			if provider == ProviderGitLab || provider == ProviderCustom {
				want = "oauth2"
			}
			if auth.Username != want {
				t.Fatalf("Git username=%q want %q", auth.Username, want)
			}
		})
	}
}
