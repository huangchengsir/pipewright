package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type Lifecycle interface {
	BeginAuthorization(context.Context, string, string, string, string, int64, bool) (string, error)
	CompleteAuthorization(context.Context, string, string, string, string, string) (string, string, error)
}

type authorizationState struct {
	Provider, AppRevision, RedirectURI string
	Target                             vault.OAuthTarget
}
type credentialGate struct {
	mu       sync.Mutex
	changed  chan struct{}
	inFlight map[string]bool
}
type lifecycle struct {
	repo  *vault.OAuthRepository
	owner string
	mu    sync.Mutex
	gates map[string]*credentialGate
	now   func() time.Time
}

func (s *service) initLifecycle() {
	s.lifecycle = &lifecycle{repo: vault.NewOAuthRepository(s.db, s.vault), owner: uuid.NewString(), gates: map[string]*credentialGate{}, now: time.Now}
}
func (l *lifecycle) gate(id string) *credentialGate {
	l.mu.Lock()
	defer l.mu.Unlock()
	g := l.gates[id]
	if g == nil {
		g = &credentialGate{changed: make(chan struct{}), inFlight: map[string]bool{}}
		l.gates[id] = g
	}
	return g
}
func (g *credentialGate) signal() { close(g.changed); g.changed = make(chan struct{}) }

func issuerOf(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(u.Path, "/oauth/token")
}

func (s *service) appRevision(ctx context.Context, provider string) (string, error) {
	_, client, secret, base, enabled, _, err := s.loadApp(ctx, provider)
	if err != nil {
		return "", err
	}
	if !enabled || client == "" || len(secret) == 0 {
		return "", ErrAppNotConfigured
	}
	return vault.OAuthAppRevision(client, base, secret, enabled), nil
}

func (s *service) BeginAuthorization(ctx context.Context, session, provider, redirect, id string, revision int64, adopt bool) (string, error) {
	provider, err := normalizeProvider(provider)
	if err != nil {
		return "", err
	}
	appRev, err := s.appRevision(ctx, provider)
	if err != nil {
		return "", err
	}
	target := vault.OAuthTarget{}
	if id != "" {
		target, err = s.lifecycle.repo.Target(ctx, id, adopt)
		if err != nil {
			return "", err
		}
		if target.Revision != revision {
			return "", vault.ErrConflict
		}
		if b, e := s.lifecycle.repo.Binding(ctx, id); e != nil {
			return "", e
		} else if b != nil && b.Provider != provider {
			return "", vault.ErrOAuthIdentity
		}
	}
	state, err := s.states.issueBound(session, authorizationState{Provider: provider, AppRevision: appRev, RedirectURI: redirect, Target: target})
	if err != nil {
		return "", err
	}
	return s.AuthorizeURL(ctx, provider, state, redirect)
}

func (s *service) CompleteAuthorization(ctx context.Context, session, provider, state, code, redirect string) (string, string, error) {
	release, err := maintenance.Enter(ctx, s.db, "oauth_callback")
	if err != nil {
		return "", "", err
	}
	defer release()
	bound, ok := s.states.consumeBound(state, session)
	if !ok {
		return "", "", ErrInvalidState
	}
	if bound.Provider != provider || bound.RedirectURI != redirect {
		return "", "", vault.ErrOAuthIdentity
	}
	rev, err := s.appRevision(ctx, provider)
	if err != nil || rev != bound.AppRevision {
		return "", "", vault.ErrOAuthIdentity
	}
	if code == "" {
		return "", "", ErrExchangeFailed
	}
	preserveClaim := false
	if bound.Target.CredentialID != "" {
		g := s.lifecycle.gate(bound.Target.CredentialID)
		g.mu.Lock()
		if g.inFlight[bound.Target.PreviousAuthorizationID] || g.inFlight[bound.Target.PreviousRefreshID] {
			g.mu.Unlock()
			return "", "", vault.ErrOAuthBusy
		}
		target, e := s.lifecycle.repo.ClaimAuthorization(ctx, bound.Target, s.lifecycle.owner)
		if e != nil {
			g.mu.Unlock()
			return "", "", e
		}
		bound.Target = target
		g.inFlight[target.ClaimID] = true
		g.mu.Unlock()
		defer func() {
			if !preserveClaim {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = s.lifecycle.repo.ClearAuthorization(cleanup, target)
				cancel()
			}
			g.mu.Lock()
			delete(g.inFlight, target.ClaimID)
			g.signal()
			g.mu.Unlock()
		}()
	}
	result, err := s.Exchange(ctx, provider, code, redirect)
	if err != nil {
		preserveClaim = true
		var exchange *tokenError
		state := vault.OAuthUncertain
		if errors.As(err, &exchange) && exchange.invalidGrant {
			state = vault.OAuthReauthorize
		}
		if code == "" {
			return "", "", ErrExchangeFailed
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.lifecycle.repo.AuthorizationFailure(cleanup, bound.Target, state)
		if state == vault.OAuthReauthorize {
			return "", "", vault.ErrOAuthReauthorize
		}
		return "", "", vault.ErrOAuthUncertain
	}
	if result.Binding.AppRevision != bound.AppRevision {
		return "", "", vault.ErrOAuthIdentity
	}
	save, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSave()
	id, err := s.lifecycle.repo.Authorize(save, result.Binding, result.Tokens, bound.Target)
	if err != nil {
		if errors.Is(err, vault.ErrOAuthIdentity) || errors.Is(err, vault.ErrConflict) || errors.Is(err, vault.ErrOAuthBusy) || errors.Is(err, vault.ErrNotFound) {
			return "", "", err
		}
		preserveClaim = true
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.lifecycle.repo.AuthorizationFailure(cleanup, bound.Target, vault.OAuthUncertain)
		return "", "", vault.ErrOAuthUncertain
	}
	return id, result.Login, nil
}

type tokenError struct{ invalidGrant bool }

func (e *tokenError) Error() string        { return "oauth: token exchange unavailable" }
func (e *tokenError) Is(target error) bool { return target == ErrExchangeFailed }

func expiry(raw json.RawMessage, started time.Time) (*time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var number json.Number
	// Providers use either JSON integers or decimal strings. Reject float/exponent,
	// negative, boolean and overflowing values rather than guessing an expiry.
	text := string(raw)
	if len(text) > 2 && text[0] == '"' {
		var str string
		if json.Unmarshal(raw, &str) != nil {
			return nil, ErrExchangeFailed
		}
		text = str
	}
	number = json.Number(text)
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return nil, ErrExchangeFailed
		}
	}
	n, e := strconv.ParseInt(number.String(), 10, 64)
	if e != nil || n <= 0 || n > 10*365*24*60*60 {
		return nil, ErrExchangeFailed
	}
	t := started.Add(time.Duration(n) * time.Second)
	return &t, nil
}

func parseTokenResponse(body []byte, started time.Time) (vault.OAuthTokens, error) {
	var parsed struct {
		AccessToken    string          `json:"access_token"`
		RefreshToken   string          `json:"refresh_token"`
		Expires        json.RawMessage `json:"expires_in"`
		RefreshExpires json.RawMessage `json:"refresh_token_expires_in"`
		TokenType      string          `json:"token_type"`
		Error          string          `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return vault.OAuthTokens{}, ErrExchangeFailed
	}
	if parsed.Error != "" {
		return vault.OAuthTokens{}, &tokenError{invalidGrant: parsed.Error == "invalid_grant"}
	}
	if parsed.AccessToken == "" || len(parsed.AccessToken) > 32768 || len(parsed.RefreshToken) > 32768 || strings.ContainsAny(parsed.AccessToken+parsed.RefreshToken, "\r\n\x00") || (parsed.TokenType != "" && !strings.EqualFold(parsed.TokenType, "bearer")) {
		return vault.OAuthTokens{}, ErrExchangeFailed
	}
	expires, e := expiry(parsed.Expires, started)
	if e != nil {
		return vault.OAuthTokens{}, e
	}
	refreshExpires, e := expiry(parsed.RefreshExpires, started)
	if e != nil {
		return vault.OAuthTokens{}, e
	}
	var after *time.Time
	if expires != nil {
		lead := expires.Sub(started) / 2
		if lead > 30*time.Second {
			lead = 30 * time.Second
		}
		at := expires.Add(-lead)
		after = &at
	}
	return vault.OAuthTokens{AccessToken: parsed.AccessToken, RefreshToken: parsed.RefreshToken, ExpiresAt: expires, RefreshExpiresAt: refreshExpires, RefreshAfter: after}, nil
}

func (s *service) requestTokens(ctx context.Context, p Provider, form url.Values) (vault.OAuthTokens, error) {
	started := s.lifecycle.now().UTC()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if e != nil {
		return vault.OAuthTokens{}, ErrExchangeFailed
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, e := s.request(req)
	if e != nil {
		return vault.OAuthTokens{}, ErrExchangeFailed
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, (1<<16)+1))
	if e != nil || len(body) > 1<<16 {
		return vault.OAuthTokens{}, ErrExchangeFailed
	}
	tokens, e := parseTokenResponse(body, started)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var te *tokenError
		if errors.As(e, &te) && te.invalidGrant {
			return vault.OAuthTokens{}, te
		}
		return vault.OAuthTokens{}, ErrExchangeFailed
	}
	return tokens, e
}

func (s *service) refresh(ctx context.Context, b *vault.OAuthBinding) error {
	release, err := maintenance.Enter(ctx, s.db, "oauth_refresh")
	if err != nil {
		return err
	}
	defer release()
	work, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider, client, sealed, base, enabled, _, err := s.loadApp(work, b.Provider)
	if err != nil || !enabled || vault.OAuthAppRevision(client, base, sealed, enabled) != b.AppRevision {
		return vault.ErrOAuthIdentity
	}
	endpoints, err := resolveProvider(provider, base)
	if err != nil || issuerOf(endpoints.TokenURL) != b.Issuer {
		return vault.ErrOAuthIdentity
	}
	refresh, err := s.vault.OpenSecret(b.RefreshCiphertext)
	if err != nil {
		return err
	}
	secret, err := s.openSecret(sealed)
	if err != nil {
		return err
	}
	form := url.Values{"client_id": {client}, "client_secret": {secret}, "grant_type": {"refresh_token"}, "refresh_token": {string(refresh)}, "redirect_uri": {b.RedirectURI}}
	tokens, err := s.requestTokens(work, endpoints, form)
	if err != nil {
		state := vault.OAuthUncertain
		var te *tokenError
		if errors.As(err, &te) && te.invalidGrant {
			state = vault.OAuthReauthorize
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.lifecycle.repo.FinishFailure(cleanup, b, state)
		if state == vault.OAuthReauthorize {
			return vault.ErrOAuthReauthorize
		}
		return vault.ErrOAuthUncertain
	}
	// Do not bind persistence to a browser cancellation after a rotating provider
	// has accepted the exchange. This one bounded local commit is never retried.
	save, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSave()
	if err = s.lifecycle.repo.CompleteRefresh(save, b, tokens); err != nil {
		_ = s.lifecycle.repo.FinishFailure(save, b, vault.OAuthUncertain)
		return vault.ErrOAuthUncertain
	}
	return nil
}
