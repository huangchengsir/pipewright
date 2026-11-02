// Package authlogin separates platform sign-in from repository OAuth credentials.
package authlogin

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode"
)

const (
	ProviderGitHub    = "github"
	ProviderGitLab    = "gitlab"
	ProviderGitee     = "gitee"
	ProviderGoogle    = "google"
	ProviderMicrosoft = "microsoft"

	GiteeModeConfidentialCode = "confidential_code"
	GiteeModeS256             = "s256"
)

// These errors deliberately omit upstream response bodies and request parameters.
var (
	ErrProviderUnavailable = errors.New("authlogin: provider unavailable")
	ErrProviderRejected    = errors.New("authlogin: provider rejected authorization")
	ErrIdentityInvalid     = errors.New("authlogin: invalid provider identity")
	ErrResultUncertain     = errors.New("authlogin: authorization result uncertain")
)

// AppConfig is trusted application configuration read and decrypted by the service.
// Provider endpoints are intentionally not configurable.
type AppConfig struct {
	Provider        string
	ClientID        string
	ClientSecret    string
	RedirectURI     string
	MicrosoftTenant string
	GiteeMode       string
}

type AuthorizeParams struct{ State, Verifier, Nonce string }
type ExchangeParams struct{ Code, Verifier, NonceHash string }

// Identity is the only output of an exchange. Tokens are not retained or returned.
// Subject and Issuer are case-sensitive, immutable authentication keys.
type Identity struct{ Provider, Issuer, Subject, DisplayLabel string }

type Adapter interface {
	Authorize(context.Context, AppConfig, AuthorizeParams) (string, error)
	Exchange(context.Context, AppConfig, ExchangeParams) (Identity, error)
}

// ValidateAppConfig performs only local validation; it never contacts a provider.
// The service must additionally enforce its configured public origin/callback.
func ValidateAppConfig(c AppConfig) error {
	if !validText(c.ClientID, 2048) || !validText(c.ClientSecret, 8192) {
		return ErrProviderUnavailable
	}
	u, err := url.Parse(c.RedirectURI)
	if err != nil || u.User != nil || u.Hostname() == "" || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || strings.Contains(c.RedirectURI, "#") ||
		!validText(c.RedirectURI, 4096) {
		return ErrProviderUnavailable
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return ErrProviderUnavailable
	}
	switch c.Provider {
	case ProviderGitHub, ProviderGitLab, ProviderGoogle:
	case ProviderGitee:
		if c.GiteeMode != GiteeModeConfidentialCode && c.GiteeMode != GiteeModeS256 {
			return ErrProviderUnavailable
		}
	case ProviderMicrosoft:
		if _, err := microsoftTenant(c.MicrosoftTenant); err != nil {
			return err
		}
	default:
		return ErrProviderUnavailable
	}
	return nil
}

func validText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
			return false
		}
	}
	return true
}
