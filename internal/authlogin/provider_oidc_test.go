package authlogin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

var signingKeysOnce sync.Once
var signingKeyOne, signingKeyTwo *rsa.PrivateKey

func providerSigningKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	signingKeysOnce.Do(func() {
		var err error
		signingKeyOne, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		signingKeyTwo, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
	})
	return signingKeyOne, signingKeyTwo
}

func signedProviderToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func providerJWK(t *testing.T, key *rsa.PrivateKey, kid, issuer string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Use: "sig", Algorithm: "RS256"})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if issuer != "" {
		value["issuer"] = issuer
	}
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func providerJWKS(t *testing.T, keys ...json.RawMessage) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func oidcClaims(now time.Time, issuer string) map[string]any {
	return map[string]any{"iss": issuer, "sub": "immutable-subject", "aud": "test-client", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": testNonce, "name": "Test Account", "picture": "https://untrusted.example/avatar"}
}

func oidcTestAdapter(t *testing.T, provider, token, jwks string, now time.Time) (*platformAdapter, *atomic.Int32) {
	t.Helper()
	var keyCalls atomic.Int32
	transport := providerRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			raw, _ := json.Marshal(map[string]string{"access_token": "test-canary-token", "id_token": token, "token_type": "Bearer"})
			return providerResponse(200, string(raw)), nil
		}
		if req.URL.String() != googleJWKS && req.URL.String() != microsoftJWKS {
			t.Error("unexpected OIDC outbound URL")
			return nil, errors.New("unexpected")
		}
		keyCalls.Add(1)
		return providerResponse(200, jwks), nil
	})
	adapter := NewDefaultAdapters(&http.Client{Transport: transport})[provider].(*platformAdapter)
	adapter.now = func() time.Time { return now }
	adapter.keys.now = adapter.now
	return adapter, &keyCalls
}

func oidcExchange(a *platformAdapter, c AppConfig) (Identity, error) {
	return a.Exchange(context.Background(), c, ExchangeParams{Code: "test-code", Verifier: testVerifier, NonceHash: nonceHash(testNonce)})
}

func TestGoogleOIDCIdentityAndLegacyIssuer(t *testing.T) {
	key, _ := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	for _, issuer := range []string{googleIssuer, "accounts.google.com"} {
		claims := oidcClaims(now, issuer)
		adapter, calls := oidcTestAdapter(t, ProviderGoogle, signedProviderToken(t, key, "one", claims), providerJWKS(t, providerJWK(t, key, "one", "")), now)
		identity, err := oidcExchange(adapter, adapterConfig(ProviderGoogle))
		if err != nil {
			t.Fatal(err)
		}
		if identity.Issuer != googleIssuer || identity.Subject != "immutable-subject" || identity.DisplayLabel != "Test Account" || calls.Load() != 1 {
			t.Fatalf("bad identity %+v, key requests %d", identity, calls.Load())
		}
	}
}

func TestOIDCRejectsClaimsAndSignatureAttacks(t *testing.T) {
	key, wrongKey := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	tests := []struct {
		name           string
		mutate         func(map[string]any)
		wrongSignature bool
	}{
		{"bad-signature", func(map[string]any) {}, true},
		{"issuer", func(c map[string]any) { c["iss"] = "https://untrusted.example" }, false},
		{"audience", func(c map[string]any) { c["aud"] = "another-client" }, false},
		{"missing-audience", func(c map[string]any) { delete(c, "aud") }, false},
		{"azp", func(c map[string]any) { c["azp"] = "another-client" }, false},
		{"multiple-audiences-without-azp", func(c map[string]any) { c["aud"] = []string{"test-client", "another-client"} }, false},
		{"nonce", func(c map[string]any) { c["nonce"] = "different" }, false},
		{"missing-nonce", func(c map[string]any) { delete(c, "nonce") }, false},
		{"expired", func(c map[string]any) { c["exp"] = now.Add(-time.Second).Unix() }, false},
		{"expiry-exact-boundary", func(c map[string]any) { c["exp"] = now.Unix() }, false},
		{"missing-expiry", func(c map[string]any) { delete(c, "exp") }, false},
		{"missing-iat", func(c map[string]any) { delete(c, "iat") }, false},
		{"future-iat", func(c map[string]any) { c["iat"] = now.Add(oidcClockSkew + time.Second).Unix() }, false},
		{"expiry-before-iat", func(c map[string]any) { c["iat"] = now.Add(time.Minute).Unix(); c["exp"] = now.Add(time.Second).Unix() }, false},
		{"future-nbf", func(c map[string]any) { c["nbf"] = now.Add(oidcClockSkew + time.Second).Unix() }, false},
		{"missing-subject", func(c map[string]any) { delete(c, "sub") }, false},
		{"control-subject", func(c map[string]any) { c["sub"] = "subject\n" }, false},
		{"non-ascii-subject", func(c map[string]any) { c["sub"] = "身份" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := oidcClaims(now, googleIssuer)
			tc.mutate(claims)
			signer := key
			if tc.wrongSignature {
				signer = wrongKey
			}
			adapter, _ := oidcTestAdapter(t, ProviderGoogle, signedProviderToken(t, signer, "one", claims), providerJWKS(t, providerJWK(t, key, "one", "")), now)
			if identity, err := oidcExchange(adapter, adapterConfig(ProviderGoogle)); !errors.Is(err, ErrIdentityInvalid) || identity != (Identity{}) {
				t.Fatalf("accepted invalid token: %+v %v", identity, err)
			}
		})
	}
	t.Run("multiple-audiences-with-correct-azp", func(t *testing.T) {
		claims := oidcClaims(now, googleIssuer)
		claims["aud"] = []string{"test-client", "another-client"}
		claims["azp"] = "test-client"
		adapter, _ := oidcTestAdapter(t, ProviderGoogle, signedProviderToken(t, key, "one", claims), providerJWKS(t, providerJWK(t, key, "one", "")), now)
		if _, err := oidcExchange(adapter, adapterConfig(ProviderGoogle)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOIDCRejectsUnsignedAndHMACAlgorithms(t *testing.T) {
	key, _ := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	body, _ := json.Marshal(oidcClaims(now, googleIssuer))
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(body) + "."
	hmacSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte("01234567890123456789012345678901")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hmacJWS, err := hmacSigner.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	hmacToken, err := hmacJWS.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{unsigned, hmacToken, "malformed", signedProviderToken(t, key, "", oidcClaims(now, googleIssuer))} {
		adapter, calls := oidcTestAdapter(t, ProviderGoogle, token, providerJWKS(t, providerJWK(t, key, "one", "")), now)
		if _, err := oidcExchange(adapter, adapterConfig(ProviderGoogle)); !errors.Is(err, ErrIdentityInvalid) {
			t.Fatal("invalid algorithm accepted")
		}
		if calls.Load() != 0 {
			t.Fatal("invalid header triggered JWKS network")
		}
	}
}

const testTenantOne = "11111111-1111-4111-8111-111111111111"
const testTenantTwo = "22222222-2222-4222-8222-222222222222"

func microsoftClaims(now time.Time, tenant string) map[string]any {
	claims := oidcClaims(now, microsoftIssuerPrefix+tenant+"/v2.0")
	claims["tid"] = tenant
	claims["ver"] = "2.0"
	return claims
}

func TestMicrosoftIssuerKeyScopesAndAccountModes(t *testing.T) {
	key, _ := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	tests := []struct {
		name, tokenTenant, configuredTenant, keyIssuer string
		valid                                          bool
	}{
		{"org-one-common-template", testTenantOne, "common", microsoftIssuerTemplate, true},
		{"org-two-common-template", testTenantTwo, "common", microsoftIssuerTemplate, true},
		{"personal-common-template", microsoftConsumerTenant, "common", microsoftIssuerTemplate, true},
		{"personal-exact-key", microsoftConsumerTenant, "consumers", microsoftIssuerPrefix + microsoftConsumerTenant + "/v2.0", true},
		{"org-exact-key", testTenantOne, testTenantOne, microsoftIssuerPrefix + testTenantOne + "/v2.0", true},
		{"organization-mode", testTenantOne, "organizations", microsoftIssuerTemplate, true},
		{"personal-in-org-mode", microsoftConsumerTenant, "organizations", microsoftIssuerTemplate, false},
		{"org-in-personal-mode", testTenantOne, "consumers", microsoftIssuerTemplate, false},
		{"wrong-restricted-tenant", testTenantTwo, testTenantOne, microsoftIssuerTemplate, false},
		{"consumer-key-cannot-sign-org", testTenantOne, "common", microsoftIssuerPrefix + microsoftConsumerTenant + "/v2.0", false},
		{"other-org-key-cannot-sign-org", testTenantOne, "common", microsoftIssuerPrefix + testTenantTwo + "/v2.0", false},
		{"missing-key-issuer", testTenantOne, "common", "", false},
		{"wrong-key-issuer-host", testTenantOne, "common", "https://untrusted.example/" + testTenantOne + "/v2.0", false},
	}
	var orgIdentities []Identity
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := signedProviderToken(t, key, "one", microsoftClaims(now, tc.tokenTenant))
			adapter, _ := oidcTestAdapter(t, ProviderMicrosoft, raw, providerJWKS(t, providerJWK(t, key, "one", tc.keyIssuer)), now)
			config := adapterConfig(ProviderMicrosoft)
			config.MicrosoftTenant = tc.configuredTenant
			identity, err := oidcExchange(adapter, config)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && err == nil {
				t.Fatal("tenant/key scope accepted")
			}
			if tc.valid && (identity.Issuer != microsoftIssuerPrefix+tc.tokenTenant+"/v2.0" || identity.Subject != "immutable-subject") {
				t.Fatalf("wrong identity %+v", identity)
			}
			if tc.name == "org-one-common-template" || tc.name == "org-two-common-template" {
				orgIdentities = append(orgIdentities, identity)
			}
		})
	}
	if len(orgIdentities) != 2 || orgIdentities[0].Issuer == orgIdentities[1].Issuer || orgIdentities[0].Subject != orgIdentities[1].Subject {
		t.Fatal("same sub was not separated by issuer")
	}
}

func TestMicrosoftRejectsInvalidRoutingClaimsBeforeKeys(t *testing.T) {
	key, _ := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	for _, name := range []string{"tid-url", "tid-missing", "issuer-mismatch", "version"} {
		t.Run(name, func(t *testing.T) {
			claims := microsoftClaims(now, testTenantOne)
			switch name {
			case "tid-url":
				claims["tid"] = "https://untrusted.example"
			case "tid-missing":
				delete(claims, "tid")
			case "issuer-mismatch":
				claims["iss"] = microsoftIssuerPrefix + testTenantTwo + "/v2.0"
			default:
				claims["ver"] = "1.0"
			}
			adapter, calls := oidcTestAdapter(t, ProviderMicrosoft, signedProviderToken(t, key, "one", claims), providerJWKS(t, providerJWK(t, key, "one", microsoftIssuerTemplate)), now)
			if _, err := oidcExchange(adapter, adapterConfig(ProviderMicrosoft)); err == nil || calls.Load() != 0 {
				t.Fatal("invalid routing claims reached key network")
			}
		})
	}
}

func TestJWKSBoundedRotationConcurrencyAndFailure(t *testing.T) {
	keyOne, keyTwo := providerSigningKeys(t)
	var clock atomic.Int64
	now := time.Unix(1800000000, 0)
	clock.Store(now.Unix())
	var rotated atomic.Bool
	var fail atomic.Bool
	var keyCalls atomic.Int32
	claims := oidcClaims(now, googleIssuer)
	// Extend token lifetime only in this local cache-boundary fixture.
	claims["exp"] = now.Add(24 * time.Hour).Unix()
	tokenOne := signedProviderToken(t, keyOne, "one", claims)
	tokenTwo := signedProviderToken(t, keyTwo, "two", claims)
	keysOne := providerJWKS(t, providerJWK(t, keyOne, "one", ""))
	keysTwo := providerJWKS(t, providerJWK(t, keyTwo, "two", ""))
	transport := providerRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			token := tokenOne
			if rotated.Load() {
				token = tokenTwo
			}
			body, _ := json.Marshal(map[string]string{"access_token": "test-canary-token", "id_token": token})
			return providerResponse(200, string(body)), nil
		}
		keyCalls.Add(1)
		if fail.Load() {
			return providerResponse(503, "temporarily unavailable"), nil
		}
		keys := keysOne
		if rotated.Load() {
			keys = keysTwo
		}
		// Permit concurrent callers to observe the same inflight refresh.
		time.Sleep(10 * time.Millisecond)
		return providerResponse(200, keys), nil
	})
	adapter := NewDefaultAdapters(&http.Client{Transport: transport})[ProviderGoogle].(*platformAdapter)
	adapter.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	adapter.keys.now = adapter.now
	config := adapterConfig(ProviderGoogle)
	concurrentExchange := func() {
		t.Helper()
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := oidcExchange(adapter, config); err != nil {
					t.Errorf("concurrent exchange failed: %v", err)
				}
			}()
		}
		wg.Wait()
	}
	concurrentExchange()
	if keyCalls.Load() != 1 {
		t.Fatalf("cold fetches = %d", keyCalls.Load())
	}
	rotated.Store(true)
	for range 5 {
		if _, err := oidcExchange(adapter, config); err == nil {
			t.Fatal("unknown kid passed without updated keys")
		}
	}
	if keyCalls.Load() != 1 {
		t.Fatal("unknown kid bypassed cooldown")
	}
	clock.Add(int64(jwksRefreshCooldown/time.Second) + 1)
	concurrentExchange()
	if keyCalls.Load() != 2 {
		t.Fatalf("rotation fetches = %d", keyCalls.Load())
	}
	fail.Store(true)
	clock.Add(int64(jwksMaxAge/time.Second) + 1)
	if _, err := oidcExchange(adapter, config); err == nil {
		t.Fatal("expired key cache failed open")
	}
	if keyCalls.Load() != 3 {
		t.Fatalf("stale fetches = %d", keyCalls.Load())
	}
	if _, err := oidcExchange(adapter, config); err == nil {
		t.Fatal("failed refresh accepted cached key")
	}
	if keyCalls.Load() != 3 {
		t.Fatal("failed fetch had no cooldown")
	}
}

func TestJWKSDoesNotFollowTokenProvidedURLs(t *testing.T) {
	key, _ := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	claims := oidcClaims(now, googleIssuer)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "one").WithHeader("jku", "https://untrusted.example/jwks").WithHeader("x5u", "https://untrusted.example/cert"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(claims)
	signed, err := signer.Sign(body)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	adapter, calls := oidcTestAdapter(t, ProviderGoogle, token, providerJWKS(t, providerJWK(t, key, "one", "")), now)
	if _, err := oidcExchange(adapter, adapterConfig(ProviderGoogle)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("token URL altered fixed JWKS request")
	}
}

func TestJWKSRejectsInvalidDocumentAndPrivateKeys(t *testing.T) {
	key, _ := providerSigningKeys(t)
	now := time.Unix(1800000000, 0)
	token := signedProviderToken(t, key, "one", oidcClaims(now, googleIssuer))
	private, err := json.Marshal(jose.JSONWebKey{Key: key, KeyID: "one", Algorithm: "RS256"})
	if err != nil {
		t.Fatal(err)
	}
	tooMany := make([]json.RawMessage, jwksMaxKeys+1)
	for i := range tooMany {
		tooMany[i] = providerJWK(t, key, "one", "")
	}
	for _, document := range []string{`{"keys":[]}`, `{"keys":null}`, "not-json", providerJWKS(t, private), providerJWKS(t, tooMany...)} {
		adapter, _ := oidcTestAdapter(t, ProviderGoogle, token, document, now)
		if _, err := oidcExchange(adapter, adapterConfig(ProviderGoogle)); err == nil {
			t.Fatal("invalid JWKS accepted")
		}
	}
}
