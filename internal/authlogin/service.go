package authlogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"golang.org/x/net/idna"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	ErrNotConfigured    = errors.New("platform_not_configured")
	ErrDisabled         = errors.New("platform_disabled")
	ErrConflict         = errors.New("platform_config_conflict")
	ErrReauthRequired   = errors.New("platform_reauth_required")
	ErrFlowInvalid      = errors.New("platform_flow_invalid")
	ErrFlowExpired      = errors.New("platform_flow_expired")
	ErrFlowConsumed     = errors.New("platform_flow_consumed")
	ErrIdentityDenied   = errors.New("platform_identity_denied")
	ErrCandidateInvalid = errors.New("platform_candidate_invalid")
	ErrBadRequest       = errors.New("bad_request")
	ErrRateLimited      = errors.New("platform_rate_limited")
)
var Providers = []string{ProviderGitHub, ProviderGitLab, ProviderGitee, ProviderGoogle, ProviderMicrosoft}

func ValidProvider(p string) bool {
	for _, v := range Providers {
		if p == v {
			return true
		}
	}
	return false
}

type Sealer interface {
	SealSecret([]byte) ([]byte, error)
	OpenSecret([]byte) ([]byte, error)
}
type Config struct {
	PublicURL         string
	AllowHTTPLoopback bool
	Adapters          map[string]Adapter
	Now               func() time.Time
}
type Service struct {
	db       *sql.DB
	vault    Sealer
	auth     *auth.Service
	origin   string
	secure   bool
	adapters map[string]Adapter
	now      func() time.Time
}

func New(db *sql.DB, v Sealer, a *auth.Service, c Config) *Service {
	now := c.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	origin, _ := PublicOrigin(c.PublicURL, c.AllowHTTPLoopback)
	adapters := c.Adapters
	if adapters == nil {
		adapters = NewDefaultAdapters(nil)
	}
	return &Service{db: db, vault: v, auth: a, origin: origin, secure: strings.HasPrefix(origin, "https://"), adapters: adapters, now: now}
}
func PublicOrigin(raw string, allowHTTP bool) (string, error) {
	if raw == "" {
		return "", ErrNotConfigured
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\\\r\n\t") {
		return "", ErrBadRequest
	}
	loop := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loop = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http" && loop) {
		return "", ErrBadRequest
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(strings.TrimSuffix(host, ".")); ip != nil {
		// IPv4-mapped IPv6 has differing URL serializers; reject this unsupported form.
		if strings.Contains(host, ":") && ip.To4() != nil {
			return "", ErrBadRequest
		}
		host = ip.String()
	} else {
		host, e = idna.Lookup.ToASCII(host)
		if e != nil || host == "" || strings.ContainsAny(host, ":%") {
			return "", ErrBadRequest
		}
		// WHATWG browsers interpret numeric final labels as IPv4, rather than DNS names.
		numericHost := strings.TrimSuffix(host, ".")
		last := numericHost[strings.LastIndex(numericHost, ".")+1:]
		if _, err := strconv.ParseUint(last, 10, 64); err == nil {
			return "", ErrBadRequest
		}
		if strings.HasPrefix(last, "0x") {
			if _, err := strconv.ParseUint(last[2:], 16, 64); err == nil {
				return "", ErrBadRequest
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrBadRequest
		}
		if (u.Scheme == "https" && n == 443) || (u.Scheme == "http" && n == 80) {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}
func (s *Service) PublicOrigin() string { return s.origin }
func (s *Service) SecureCookies() bool  { return s.secure }
func (s *Service) CallbackURL(p string) string {
	if s.origin == "" || !ValidProvider(p) {
		return ""
	}
	return s.origin + "/api/auth/platform/" + p + "/callback"
}
func SafeReturnTo(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if len(raw) > 2048 {
		return "", ErrBadRequest
	}
	u, e := url.Parse(raw)
	if e != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" || strings.ContainsRune(raw, '\\') {
		return "", ErrBadRequest
	}
	for _, c := range raw {
		if unicode.IsControl(c) || c == ' ' {
			return "", ErrBadRequest
		}
	}
	// Query/fragment values are data, not redirect authorities. Do not recursively decode them.
	check := u.EscapedPath()
	for i := 0; i < 5; i++ {
		if !strings.HasPrefix(check, "/") || strings.HasPrefix(check, "//") || strings.ContainsRune(check, '\\') {
			return "", ErrBadRequest
		}
		for _, c := range check {
			if unicode.IsControl(c) {
				return "", ErrBadRequest
			}
		}
		decoded, e := url.PathUnescape(check)
		if e != nil {
			return "", ErrBadRequest
		}
		if decoded == check {
			break
		}
		check = decoded
	}
	if strings.Contains(check, "%") || strings.HasPrefix(check, "//") || strings.ContainsRune(check, '\\') {
		return "", ErrBadRequest
	}
	return raw, nil
}

type Binding struct {
	DisplayLabel string `json:"displayLabel"`
	Issuer       string `json:"issuer"`
	Subject      string `json:"subject"`
	BoundAt      string `json:"boundAt"`
}
type ProviderSettings struct {
	Provider        string   `json:"provider"`
	Visible         bool     `json:"visible"`
	Enabled         bool     `json:"enabled"`
	Configured      bool     `json:"configured"`
	HasSecret       bool     `json:"hasSecret"`
	ClientID        string   `json:"clientId"`
	MicrosoftTenant string   `json:"microsoftTenant"`
	GiteeMode       string   `json:"giteeMode"`
	CallbackURL     string   `json:"callbackURL"`
	ConfigVersion   int64    `json:"configVersion"`
	Binding         *Binding `json:"binding,omitempty"`
}
type Snapshot struct {
	VisibilityVersion    int64              `json:"visibilityVersion"`
	ReauthenticatedUntil string             `json:"reauthenticatedUntil"`
	Providers            []ProviderSettings `json:"providers"`
}
type PublicProvider struct {
	Provider  string `json:"provider"`
	Visible   bool   `json:"visible"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}
type Mutation struct {
	Provider       ProviderSettings `json:"provider"`
	SessionRevoked bool             `json:"sessionRevoked"`
}
type AppInput struct {
	ExpectedVersion int64   `json:"expectedVersion"`
	ClientID        string  `json:"clientId"`
	ClientSecret    *string `json:"clientSecret"`
	MicrosoftTenant string  `json:"microsoftTenant"`
	GiteeMode       string  `json:"giteeMode"`
}
type row struct {
	ProviderSettings
	cipher                         []byte
	generation, identityGeneration int64
}
type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const rowQuery = `SELECT p.provider,p.visible,p.enabled,p.client_id,p.secret_cipher,p.microsoft_tenant,p.gitee_mode,p.config_version,p.auth_generation,i.issuer,i.subject,i.display_label,i.identity_generation,i.bound_at FROM login_provider_settings p LEFT JOIN login_identities i ON i.provider=p.provider WHERE p.provider=?`

func (s *Service) read(ctx context.Context, q querier, p string) (row, error) {
	var r row
	var issuer, subject, label, bound sql.NullString
	var ig sql.NullInt64
	e := q.QueryRowContext(ctx, rowQuery, p).Scan(&r.Provider, &r.Visible, &r.Enabled, &r.ClientID, &r.cipher, &r.MicrosoftTenant, &r.GiteeMode, &r.ConfigVersion, &r.generation, &issuer, &subject, &label, &ig, &bound)
	if e != nil {
		return r, e
	}
	r.identityGeneration = ig.Int64
	if issuer.Valid {
		r.Binding = &Binding{label.String, issuer.String, subject.String, bound.String}
	}
	r.HasSecret = len(r.cipher) > 0
	r.CallbackURL = s.CallbackURL(p)
	app := AppConfig{Provider: p, ClientID: r.ClientID, ClientSecret: "configured", RedirectURI: r.CallbackURL, MicrosoftTenant: r.MicrosoftTenant, GiteeMode: r.GiteeMode}
	r.Configured = r.HasSecret && s.vault != nil && ValidateAppConfig(app) == nil
	if r.Configured {
		plain, e := s.vault.OpenSecret(r.cipher)
		r.Configured = e == nil && len(plain) > 0
	}
	return r, nil
}
func (s *Service) Settings(ctx context.Context, token string) (Snapshot, error) {
	out := Snapshot{Providers: make([]ProviderSettings, 0, 5)}
	if e := s.db.QueryRowContext(ctx, `SELECT visibility_revision FROM login_settings WHERE id=1`).Scan(&out.VisibilityVersion); e != nil {
		return out, e
	}
	for _, p := range Providers {
		r, e := s.read(ctx, s.db, p)
		if e != nil {
			return out, e
		}
		out.Providers = append(out.Providers, r.ProviderSettings)
	}
	var verified sql.NullString
	if token != "" && s.db.QueryRowContext(ctx, `SELECT password_verified_at FROM sessions WHERE token=?`, token).Scan(&verified) == nil && verified.Valid {
		t, e := time.Parse(time.RFC3339, verified.String)
		if e == nil && s.now().Before(t.Add(auth.RecentPasswordWindow)) {
			out.ReauthenticatedUntil = t.Add(auth.RecentPasswordWindow).UTC().Format(time.RFC3339)
		}
	}
	return out, nil
}
func (s *Service) PublicProviders(ctx context.Context) ([]PublicProvider, error) {
	out := make([]PublicProvider, 0, 5)
	for _, p := range Providers {
		r, e := s.read(ctx, s.db, p)
		if e != nil {
			return nil, e
		}
		v := PublicProvider{Provider: p, Visible: r.Visible, Available: r.Configured && r.Enabled && r.Binding != nil}
		if !r.Configured {
			v.Reason = "not_configured"
		} else if !v.Available {
			v.Reason = "temporarily_unavailable"
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) SaveVisibility(ctx context.Context, token string, version int64, values map[string]bool) (Snapshot, error) {
	if len(values) != 5 {
		return Snapshot{}, ErrBadRequest
	}
	for _, p := range Providers {
		if _, ok := values[p]; !ok {
			return Snapshot{}, ErrBadRequest
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Snapshot{}, e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE login_settings SET visibility_revision=visibility_revision+1 WHERE id=1 AND visibility_revision=?`, version)
	if e != nil {
		return Snapshot{}, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	for _, p := range Providers {
		if _, e = tx.ExecContext(ctx, `UPDATE login_provider_settings SET visible=? WHERE provider=?`, values[p], p); e != nil {
			return Snapshot{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return Snapshot{}, e
	}
	return s.Settings(ctx, token)
}

// Locking the single administrator serializes security mutations and final callback commits.
func (s *Service) lock(ctx context.Context, tx *sql.Tx, token string, recent bool) (int64, error) {
	if _, e := tx.ExecContext(ctx, `UPDATE admin_user SET security_generation=security_generation WHERE id=1`); e != nil {
		return 0, e
	}
	var generation int64
	if e := tx.QueryRowContext(ctx, `SELECT security_generation FROM admin_user WHERE id=1`).Scan(&generation); e != nil {
		return 0, e
	}
	if token != "" {
		var verified sql.NullString
		var expires string
		var absolute sql.NullString
		if e := tx.QueryRowContext(ctx, `SELECT password_verified_at,expires_at,absolute_expires_at FROM sessions WHERE token=?`, token).Scan(&verified, &expires, &absolute); e != nil {
			return 0, auth.ErrSessionNotFound
		}
		expiry, e := time.Parse(time.RFC3339, expires)
		if e != nil || !s.now().Before(expiry) {
			return 0, auth.ErrSessionNotFound
		}
		if absolute.Valid {
			t, e := time.Parse(time.RFC3339, absolute.String)
			if e != nil || !s.now().Before(t) {
				return 0, auth.ErrSessionNotFound
			}
		}
		if recent {
			t, e := time.Parse(time.RFC3339, verified.String)
			if e != nil || t.After(s.now()) || !s.now().Before(t.Add(auth.RecentPasswordWindow)) {
				return 0, ErrReauthRequired
			}
		}
	}
	return generation, nil
}
func (s *Service) invalidate(ctx context.Context, tx *sql.Tx, p string) error {
	if _, e := tx.ExecContext(ctx, `DELETE FROM sessions WHERE auth_source=?`, p); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE login_flows SET status='invalidated',verifier_cipher=NULL,candidate_cipher=NULL WHERE provider=? AND status IN ('created','processing','candidate')`, p)
	return e
}
func (s *Service) mutation(ctx context.Context, token, p string, version int64, change func(*sql.Tx, row) error) (Mutation, error) {
	if !ValidProvider(p) {
		return Mutation{}, ErrBadRequest
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Mutation{}, e
	}
	defer tx.Rollback()
	if _, e = s.lock(ctx, tx, token, true); e != nil {
		return Mutation{}, e
	}
	old, e := s.read(ctx, tx, p)
	if e != nil {
		return Mutation{}, e
	}
	if old.ConfigVersion != version {
		return Mutation{}, ErrConflict
	}
	var source string
	if e = tx.QueryRowContext(ctx, `SELECT auth_source FROM sessions WHERE token=?`, token).Scan(&source); e != nil {
		return Mutation{}, e
	}
	if e = change(tx, old); e != nil {
		return Mutation{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE login_provider_settings SET config_version=config_version+1,auth_generation=auth_generation+1 WHERE provider=? AND config_version=?`, p, version); e != nil {
		return Mutation{}, e
	}
	if e = s.invalidate(ctx, tx, p); e != nil {
		return Mutation{}, e
	}
	after, e := s.read(ctx, tx, p)
	if e != nil {
		return Mutation{}, e
	}
	if e = tx.Commit(); e != nil {
		return Mutation{}, e
	}
	if s.auth != nil {
		s.auth.NotifyRevokedSource(p)
	}
	return Mutation{after.ProviderSettings, source == p}, nil
}
func (s *Service) SaveApp(ctx context.Context, token, p string, in AppInput) (Mutation, error) {
	return s.mutation(ctx, token, p, in.ExpectedVersion, func(tx *sql.Tx, old row) error {
		tenant := in.MicrosoftTenant
		if p == ProviderMicrosoft && tenant == "" {
			tenant = "common"
		}
		if p != ProviderMicrosoft {
			tenant = "common"
		}
		mode := in.GiteeMode
		if p != ProviderGitee {
			mode = ""
		}
		changed := old.ClientID != in.ClientID || old.MicrosoftTenant != tenant || old.GiteeMode != mode

		validateURL := s.CallbackURL(p)
		if validateURL == "" {
			validateURL = "https://invalid.local/api/auth/platform/" + p + "/callback"
		}
		provided := in.ClientSecret != nil && *in.ClientSecret != ""
		if changed && !provided {
			return ErrBadRequest
		}
		validationSecret := "configured"
		if provided {
			validationSecret = *in.ClientSecret
		}
		if ValidateAppConfig(AppConfig{Provider: p, ClientID: in.ClientID, ClientSecret: validationSecret, RedirectURI: validateURL, MicrosoftTenant: tenant, GiteeMode: mode}) != nil {
			return ErrBadRequest
		}
		cipher := old.cipher
		if provided {
			if s.vault == nil {
				return ErrNotConfigured
			}
			var e error
			cipher, e = s.vault.SealSecret([]byte(*in.ClientSecret))
			if e != nil {
				return ErrNotConfigured
			}
		}
		if len(cipher) == 0 {
			return ErrBadRequest
		}

		if changed {
			if _, e := tx.ExecContext(ctx, `DELETE FROM login_identities WHERE provider=?`, p); e != nil {
				return e
			}
			if _, e := tx.ExecContext(ctx, `UPDATE login_provider_settings SET enabled=0 WHERE provider=?`, p); e != nil {
				return e
			}
		}
		_, e := tx.ExecContext(ctx, `UPDATE login_provider_settings SET client_id=?,secret_cipher=?,microsoft_tenant=?,gitee_mode=? WHERE provider=?`, in.ClientID, cipher, tenant, mode, p)
		return e
	})
}
func (s *Service) SetEnabled(ctx context.Context, token, p string, version int64, enabled bool) (Mutation, error) {
	return s.mutation(ctx, token, p, version, func(tx *sql.Tx, r row) error {
		if enabled && (!r.Configured || r.Binding == nil) {
			return ErrNotConfigured
		}
		_, e := tx.ExecContext(ctx, `UPDATE login_provider_settings SET enabled=? WHERE provider=?`, enabled, p)
		return e
	})
}
func (s *Service) Unbind(ctx context.Context, token, p string, version int64) (Mutation, error) {
	return s.mutation(ctx, token, p, version, func(tx *sql.Tx, _ row) error {
		if _, e := tx.ExecContext(ctx, `DELETE FROM login_identities WHERE provider=?`, p); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE login_provider_settings SET enabled=0 WHERE provider=?`, p)
		return e
	})
}
func (s *Service) app(r row) (AppConfig, error) {
	if !r.Configured {
		return AppConfig{}, ErrNotConfigured
	}
	secret, e := s.vault.OpenSecret(r.cipher)
	if e != nil {
		return AppConfig{}, ErrNotConfigured
	}
	app := AppConfig{Provider: r.Provider, ClientID: r.ClientID, ClientSecret: string(secret), RedirectURI: r.CallbackURL, MicrosoftTenant: r.MicrosoftTenant, GiteeMode: r.GiteeMode}
	if ValidateAppConfig(app) != nil {
		return AppConfig{}, ErrNotConfigured
	}
	return app, nil
}
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func digest(value string) string { s := sha256.Sum256([]byte(value)); return hex.EncodeToString(s[:]) }
func (s *Service) seal(value any) ([]byte, error) {
	if s.vault == nil {
		return nil, ErrNotConfigured
	}
	b, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	return s.vault.SealSecret(b)
}
func (s *Service) open(cipher []byte, value any) error {
	if s.vault == nil {
		return ErrNotConfigured
	}
	b, e := s.vault.OpenSecret(cipher)
	if e != nil {
		return ErrFlowInvalid
	}
	if json.Unmarshal(b, value) != nil {
		return ErrFlowInvalid
	}
	return nil
}
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now().UTC()
	_, e := s.db.ExecContext(ctx, `UPDATE login_flows SET status='expired',verifier_cipher=NULL,candidate_cipher=NULL WHERE status IN ('created','processing','candidate') AND (expires_at<=? OR (status='candidate' AND candidate_expires_at<=?))`, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, `DELETE FROM login_flows WHERE expires_at<?`, now.Add(-24*time.Hour).Format(time.RFC3339))
	return e
}
