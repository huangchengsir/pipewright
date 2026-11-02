package authlogin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

const (
	googleIssuer            = "https://accounts.google.com"
	googleJWKS              = "https://www.googleapis.com/oauth2/v3/certs"
	microsoftIssuerPrefix   = "https://login.microsoftonline.com/"
	microsoftIssuerTemplate = microsoftIssuerPrefix + "{tenantid}/v2.0"
	microsoftJWKS           = "https://login.microsoftonline.com/common/discovery/v2.0/keys"
	microsoftConsumerTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
	oidcClockSkew           = 2 * time.Minute
)

var tenantGUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func microsoftTenant(value string) (string, error) {
	if value == "" {
		return "common", nil
	}
	if value != strings.TrimSpace(value) {
		return "", ErrProviderUnavailable
	}
	value = strings.ToLower(value)
	switch value {
	case "common", "organizations", "consumers":
		return value, nil
	default:
		if tenantGUID.MatchString(value) {
			return value, nil
		}
	}
	return "", ErrProviderUnavailable
}

func validNonceHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func nonceMatches(nonce, expectedHash string) bool {
	if !validText(nonce, 512) {
		return false
	}
	expected, err := hex.DecodeString(expectedHash)
	if err != nil || len(expected) != sha256.Size {
		return false
	}
	actual := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare(actual[:], expected) == 1
}

func validOIDCSubject(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// microsoftIssuer selects a bounded validation context; unverified claims never
// grant access and never select a network host. Full verification follows.
func microsoftIssuer(c AppConfig, raw string) (string, error) {
	jws, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(jws.Signatures) != 1 {
		return "", ErrIdentityInvalid
	}
	var claims struct {
		Tenant  string `json:"tid"`
		Issuer  string `json:"iss"`
		Version string `json:"ver"`
	}
	if json.Unmarshal(jws.UnsafePayloadWithoutVerification(), &claims) != nil ||
		!tenantGUID.MatchString(claims.Tenant) || claims.Version != "2.0" {
		return "", ErrIdentityInvalid
	}
	tenant, err := microsoftTenant(c.MicrosoftTenant)
	if err != nil {
		return "", err
	}
	switch tenant {
	case "common":
	case "organizations":
		if claims.Tenant == microsoftConsumerTenant {
			return "", ErrIdentityInvalid
		}
	case "consumers":
		if claims.Tenant != microsoftConsumerTenant {
			return "", ErrIdentityInvalid
		}
	default:
		if claims.Tenant != tenant {
			return "", ErrIdentityInvalid
		}
	}
	expected := microsoftIssuerPrefix + claims.Tenant + "/v2.0"
	if claims.Issuer != expected {
		return "", ErrIdentityInvalid
	}
	return expected, nil
}

func (a *platformAdapter) verifyIDToken(ctx context.Context, c AppConfig, p ExchangeParams, raw string) (Identity, error) {
	if len(raw) > 65536 || strings.Count(raw, ".") != 2 {
		return Identity{}, ErrIdentityInvalid
	}
	issuer := googleIssuer
	if c.Provider == ProviderMicrosoft {
		var err error
		issuer, err = microsoftIssuer(c, raw)
		if err != nil {
			return Identity{}, err
		}
	}
	keys := &scopedKeySet{cache: a.keys, issuer: issuer}
	verifier := oidc.NewVerifier(issuer, keys, &oidc.Config{
		ClientID:             c.ClientID,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  a.now,
	})
	token, err := verifier.Verify(ctx, raw)
	if err != nil {
		if keys.failure == ErrResultUncertain {
			return Identity{}, ErrResultUncertain
		}
		return Identity{}, ErrIdentityInvalid
	}
	now := a.now()
	if !validOIDCSubject(token.Subject) || !nonceMatches(token.Nonce, p.NonceHash) ||
		token.IssuedAt.IsZero() || token.IssuedAt.After(now.Add(oidcClockSkew)) ||
		!token.Expiry.After(now) || token.Expiry.Before(token.IssuedAt) {
		return Identity{}, ErrIdentityInvalid
	}
	// go-oidc checks audience membership but intentionally does not validate azp
	// or iat. Keep these checks explicit rather than assuming library coverage.
	var claims struct {
		AuthorizedParty string `json:"azp"`
		Name            string `json:"name"`
		NotBefore       *int64 `json:"nbf"`
	}
	if token.Claims(&claims) != nil ||
		(len(token.Audience) > 1 && claims.AuthorizedParty == "") ||
		(claims.AuthorizedParty != "" && claims.AuthorizedParty != c.ClientID) ||
		(claims.NotBefore != nil && time.Unix(*claims.NotBefore, 0).After(now.Add(oidcClockSkew))) {
		return Identity{}, ErrIdentityInvalid
	}
	// Google explicitly permits its historical schemeless issuer. NewVerifier
	// only allows that exception for Google; persist the canonical trusted value.
	return Identity{Provider: c.Provider, Issuer: issuer, Subject: token.Subject, DisplayLabel: displayLabel(claims.Name, c.Provider)}, nil
}
