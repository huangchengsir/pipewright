package authlogin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/oauth2"
)

type providerDefinition struct {
	name, issuer, authURL, tokenURL, userURL string
	scopes                                   []string
}

var providerDefinitions = map[string]providerDefinition{
	ProviderGitHub:    {name: ProviderGitHub, issuer: "https://github.com", authURL: "https://github.com/login/oauth/authorize", tokenURL: "https://github.com/login/oauth/access_token", userURL: "https://api.github.com/user"},
	ProviderGitLab:    {name: ProviderGitLab, issuer: "https://gitlab.com", authURL: "https://gitlab.com/oauth/authorize", tokenURL: "https://gitlab.com/oauth/token", userURL: "https://gitlab.com/api/v4/user", scopes: []string{"read_user"}},
	ProviderGitee:     {name: ProviderGitee, issuer: "https://gitee.com", authURL: "https://gitee.com/oauth/authorize", tokenURL: "https://gitee.com/oauth/token", userURL: "https://gitee.com/api/v5/user", scopes: []string{"user_info"}},
	ProviderGoogle:    {name: ProviderGoogle, issuer: googleIssuer, authURL: "https://accounts.google.com/o/oauth2/v2/auth", tokenURL: "https://oauth2.googleapis.com/token", scopes: []string{"openid", "profile"}},
	ProviderMicrosoft: {name: ProviderMicrosoft, authURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", tokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token", scopes: []string{"openid", "profile"}},
}

type platformAdapter struct {
	def    providerDefinition
	client *http.Client
	now    func() time.Time
	keys   *jwksCache
}

// NewDefaultAdapters creates the five fixed public-cloud adapters without I/O.
func NewDefaultAdapters(client *http.Client) map[string]Adapter {
	result := make(map[string]Adapter, len(providerDefinitions))
	safeClient := providerHTTP(client)
	for name, def := range providerDefinitions {
		a := &platformAdapter{def: def, client: safeClient, now: time.Now}
		switch name {
		case ProviderGoogle:
			a.keys = newJWKSCache(safeClient, googleJWKS, false)
		case ProviderMicrosoft:
			a.keys = newJWKSCache(safeClient, microsoftJWKS, true)
		}
		result[name] = a
	}
	return result
}

func (a *platformAdapter) config(c AppConfig) (*oauth2.Config, error) {
	if c.Provider != a.def.name {
		return nil, ErrProviderUnavailable
	}
	if err := ValidateAppConfig(c); err != nil {
		return nil, err
	}
	endpoint := oauth2.Endpoint{AuthURL: a.def.authURL, TokenURL: a.def.tokenURL, AuthStyle: oauth2.AuthStyleInParams}
	if c.Provider == ProviderMicrosoft {
		tenant, err := microsoftTenant(c.MicrosoftTenant)
		if err != nil {
			return nil, err
		}
		endpoint.AuthURL = "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/authorize"
		endpoint.TokenURL = "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token"
	}
	return &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURI, Endpoint: endpoint, Scopes: a.def.scopes}, nil
}

func usesPKCE(c AppConfig) bool { return c.Provider != ProviderGitee || c.GiteeMode == GiteeModeS256 }
func usesOIDC(c AppConfig) bool {
	return c.Provider == ProviderGoogle || c.Provider == ProviderMicrosoft
}

func (a *platformAdapter) Authorize(ctx context.Context, c AppConfig, p AuthorizeParams) (string, error) {
	if ctx.Err() != nil {
		return "", ErrProviderUnavailable
	}
	config, err := a.config(c)
	if err != nil {
		return "", err
	}
	if !validText(p.State, 512) || (usesPKCE(c) && !validVerifier(p.Verifier)) ||
		(usesOIDC(c) && !validText(p.Nonce, 512)) {
		return "", ErrProviderUnavailable
	}
	options := []oauth2.AuthCodeOption{}
	if usesPKCE(c) {
		options = append(options, oauth2.S256ChallengeOption(p.Verifier))
	}
	if usesOIDC(c) {
		options = append(options, oauth2.SetAuthURLParam("nonce", p.Nonce), oauth2.SetAuthURLParam("response_mode", "query"))
	}
	return config.AuthCodeURL(p.State, options...), nil
}

func (a *platformAdapter) Exchange(ctx context.Context, c AppConfig, p ExchangeParams) (Identity, error) {
	config, err := a.config(c)
	if err != nil {
		return Identity{}, err
	}
	if !validText(p.Code, 8192) || (usesPKCE(c) && !validVerifier(p.Verifier)) ||
		(usesOIDC(c) && !validNonceHash(p.NonceHash)) {
		return Identity{}, ErrIdentityInvalid
	}
	ctx, cancel := requestContext(ctx)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, a.client)
	options := []oauth2.AuthCodeOption{}
	if usesPKCE(c) {
		options = append(options, oauth2.VerifierOption(p.Verifier))
	}
	// Endpoint.AuthStyle is fixed: never probe another authentication style or
	// retry this code after a failure. The flow service separately claims it once.
	token, err := config.Exchange(ctx, p.Code, options...)
	if err != nil {
		if _, ok := err.(*oauth2.RetrieveError); ok {
			return Identity{}, ErrProviderRejected
		}
		return Identity{}, ErrResultUncertain
	}
	if usesOIDC(c) {
		raw, ok := token.Extra("id_token").(string)
		if !ok || raw == "" {
			return Identity{}, ErrIdentityInvalid
		}
		return a.verifyIDToken(ctx, c, p, raw)
	}
	if token.AccessToken == "" || len(token.AccessToken) > 16384 ||
		strings.ContainsAny(token.AccessToken, "\r\n") ||
		!strings.EqualFold(token.Type(), "Bearer") {
		return Identity{}, ErrIdentityInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.def.userURL, nil)
	if err != nil {
		return Identity{}, ErrProviderUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")
	if c.Provider == ProviderGitHub {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return Identity{}, ErrResultUncertain
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Identity{}, ErrProviderRejected
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Identity{}, ErrProviderRejected
	}
	var user struct {
		ID       json.RawMessage `json:"id"`
		Login    string          `json:"login"`
		Username string          `json:"username"`
		Name     string          `json:"name"`
	}
	if json.Unmarshal(body, &user) != nil {
		return Identity{}, ErrIdentityInvalid
	}
	subject, err := numericSubject(user.ID)
	if err != nil {
		return Identity{}, err
	}
	label := user.Login
	if c.Provider == ProviderGitLab {
		label = user.Username
	}
	if label == "" {
		label = user.Name
	}
	return Identity{Provider: c.Provider, Issuer: a.def.issuer, Subject: subject, DisplayLabel: displayLabel(label, c.Provider)}, nil
}

var decimalSubject = regexp.MustCompile(`^[1-9][0-9]{0,39}$`)

func numericSubject(raw json.RawMessage) (string, error) {
	value := string(raw)
	if len(value) > 0 && value[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return "", ErrIdentityInvalid
		}
	}
	if !decimalSubject.MatchString(value) {
		return "", ErrIdentityInvalid
	}
	return value, nil
}

func displayLabel(value, fallback string) string {
	clean := func(text string) string {
		if !utf8.ValidString(text) {
			return ""
		}
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, text))
	}
	value = clean(value)
	if value == "" {
		value = clean(fallback)
	}
	// Storage/core contracts use UTF-8 bytes; truncate only at complete rune boundaries.
	end, count := 0, 0
	for offset, r := range value {
		if count == 200 || offset+utf8.RuneLen(r) > 512 {
			break
		}
		end = offset + utf8.RuneLen(r)
		count++
	}
	return value[:end]
}

// Keep net/url in this package's compilation unit for exact, fixed path validation.
func validMicrosoftTokenPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[2] != "oauth2" || parts[3] != "v2.0" || parts[4] != "token" {
		return false
	}
	tenant, err := microsoftTenant(parts[1])
	return err == nil && tenant == parts[1] && url.PathEscape(tenant) == tenant
}
