package authlogin

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const (
	jwksMaxAge          = 6 * time.Hour
	jwksRefreshCooldown = time.Minute
	jwksMaxKeys         = 64
)

type scopedJWK struct {
	key    jose.JSONWebKey
	issuer string
}

// jwksCache is shared across all tenants of a provider, so arbitrary tid/kid
// values cannot create an unbounded map of tenants or cached keys.
type jwksCache struct {
	client    *http.Client
	endpoint  string
	microsoft bool
	now       func() time.Time

	mu          sync.Mutex
	keys        []scopedJWK
	expiresAt   time.Time
	lastAttempt time.Time
	inflight    chan struct{}
	lastError   error
}

func newJWKSCache(client *http.Client, endpoint string, microsoft bool) *jwksCache {
	return &jwksCache{client: client, endpoint: endpoint, microsoft: microsoft, now: time.Now}
}

func (c *jwksCache) load(ctx context.Context, force bool) ([]scopedJWK, error) {
	c.mu.Lock()
	now := c.now()
	if !force && len(c.keys) > 0 && now.Before(c.expiresAt) {
		keys := c.keys
		c.mu.Unlock()
		return keys, nil
	}
	if c.inflight != nil {
		done := c.inflight
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ErrResultUncertain
		case <-done:
			c.mu.Lock()
			keys, err := c.keys, c.lastError
			c.mu.Unlock()
			return keys, err
		}
	}
	if !c.lastAttempt.IsZero() && now.Before(c.lastAttempt.Add(jwksRefreshCooldown)) {
		c.mu.Unlock()
		return nil, ErrIdentityInvalid
	}
	c.lastAttempt = now
	c.inflight = make(chan struct{})
	done := c.inflight
	c.mu.Unlock()

	keys, err := c.fetch(ctx)
	c.mu.Lock()
	if err == nil {
		c.keys = keys
		c.expiresAt = c.now().Add(jwksMaxAge)
	}
	c.lastError = err
	c.inflight = nil
	close(done)
	c.mu.Unlock()
	return keys, err
}

func (c *jwksCache) fetch(ctx context.Context) ([]scopedJWK, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, ErrResultUncertain
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrResultUncertain
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ErrResultUncertain
	}
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if json.Unmarshal(body, &document) != nil || len(document.Keys) == 0 || len(document.Keys) > jwksMaxKeys {
		return nil, ErrIdentityInvalid
	}
	keys := make([]scopedJWK, 0, len(document.Keys))
	for _, raw := range document.Keys {
		var key jose.JSONWebKey
		var scope struct {
			Issuer string `json:"issuer"`
		}
		if json.Unmarshal(raw, &key) != nil || json.Unmarshal(raw, &scope) != nil ||
			!key.IsPublic() || !key.Valid() || key.KeyID == "" || len(key.KeyID) > 256 ||
			(key.Use != "" && key.Use != "sig") || (key.Algorithm != "" && key.Algorithm != "RS256") {
			continue
		}
		rsaKey, ok := key.Key.(*rsa.PublicKey)
		if !ok || rsaKey.N.BitLen() < 2048 {
			continue
		}
		if c.microsoft && !validMicrosoftKeyIssuer(scope.Issuer) {
			continue
		}
		keys = append(keys, scopedJWK{key: key, issuer: scope.Issuer})
	}
	if len(keys) == 0 {
		return nil, ErrIdentityInvalid
	}
	return keys, nil
}

func validMicrosoftKeyIssuer(issuer string) bool {
	if issuer == microsoftIssuerTemplate {
		return true
	}
	if !strings.HasPrefix(issuer, microsoftIssuerPrefix) || !strings.HasSuffix(issuer, "/v2.0") {
		return false
	}
	tenant := strings.TrimSuffix(strings.TrimPrefix(issuer, microsoftIssuerPrefix), "/v2.0")
	return tenantGUID.MatchString(tenant)
}

type scopedKeySet struct {
	cache   *jwksCache
	issuer  string
	failure error
}

func (s *scopedKeySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(jws.Signatures) != 1 || jws.Signatures[0].Header.KeyID == "" {
		return nil, ErrIdentityInvalid
	}
	keys, err := s.cache.load(ctx, false)
	if err != nil {
		s.failure = err
		return nil, err
	}
	if payload, ok := s.verify(jws, keys); ok {
		return payload, nil
	}
	// Unknown kid/signature may mean rotation, but refresh is globally bounded
	// and coalesced. Neither JWT URLs nor tenant-specific hosts are followed.
	keys, err = s.cache.load(ctx, true)
	if err != nil {
		s.failure = err
		return nil, err
	}
	if payload, ok := s.verify(jws, keys); ok {
		return payload, nil
	}
	return nil, ErrIdentityInvalid
}

func (s *scopedKeySet) verify(jws *jose.JSONWebSignature, keys []scopedJWK) ([]byte, bool) {
	kid := jws.Signatures[0].Header.KeyID
	for _, scoped := range keys {
		if scoped.key.KeyID != kid {
			continue
		}
		if s.cache.microsoft {
			issuer := scoped.issuer
			if issuer == microsoftIssuerTemplate {
				tenant := strings.TrimSuffix(strings.TrimPrefix(s.issuer, microsoftIssuerPrefix), "/v2.0")
				if !tenantGUID.MatchString(tenant) {
					continue
				}
				issuer = microsoftIssuerPrefix + tenant + "/v2.0"
			}
			if issuer != s.issuer {
				continue
			}
		}
		if payload, err := jws.Verify(scoped.key.Key); err == nil {
			return payload, true
		}
	}
	return nil, false
}
