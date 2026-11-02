package authlogin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type providerRoundTripper func(*http.Request) (*http.Response, error)

func (f providerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func providerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}
func adapterConfig(provider string) AppConfig {
	return AppConfig{Provider: provider, ClientID: "test-client", ClientSecret: "test-canary-secret", RedirectURI: "https://pipewright.example/api/auth/platform/" + provider + "/callback", GiteeMode: GiteeModeConfidentialCode}
}

const testVerifier = "test-verifier-abcdefghijklmnopqrstuvwxyz-0123456789"
const testNonce = "test-nonce-abcdefghijklmnopqrstuvwxyz-0123456789"

func nonceHash(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

func TestAdaptersAuthorizeWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	adapters := NewDefaultAdapters(&http.Client{Transport: providerRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected network")
	})})
	if len(adapters) != 5 {
		t.Fatalf("adapters = %d", len(adapters))
	}
	scopes := map[string]string{ProviderGitHub: "", ProviderGitLab: "read_user", ProviderGitee: "user_info", ProviderGoogle: "openid profile", ProviderMicrosoft: "openid profile"}
	for name, adapter := range adapters {
		t.Run(name, func(t *testing.T) {
			c := adapterConfig(name)
			raw, err := adapter.Authorize(context.Background(), c, AuthorizeParams{State: "test-state", Verifier: testVerifier, Nonce: testNonce})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			query := u.Query()
			if u.Scheme != "https" || query.Get("state") != "test-state" || query.Get("client_id") != c.ClientID || query.Get("redirect_uri") != c.RedirectURI || query.Get("response_type") != "code" || query.Get("scope") != scopes[name] {
				t.Fatalf("incorrect authorization parameters for %s", name)
			}
			if query.Has("client_secret") || query.Has("access_type") || query.Has("offline_access") {
				t.Fatal("unnecessary credential/access parameter")
			}
			if usesPKCE(c) {
				hash := sha256.Sum256([]byte(testVerifier))
				if query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(hash[:]) || query.Get("code_challenge_method") != "S256" {
					t.Fatal("missing S256 binding")
				}
			} else if query.Has("code_challenge") {
				t.Fatal("Gitee capability was inferred")
			}
			if usesOIDC(c) && (query.Get("nonce") != testNonce || query.Get("response_mode") != "query") {
				t.Fatal("missing OIDC nonce/query mode")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("initialization/authorize made %d external requests", calls.Load())
	}
}

func TestAdapterConfigurationRejectsUnsupportedInput(t *testing.T) {
	cases := []struct {
		name   string
		change func(*AppConfig)
	}{
		{"missing-client", func(c *AppConfig) { c.ClientID = "" }},
		{"missing-secret", func(c *AppConfig) { c.ClientSecret = "" }},
		{"remote-http", func(c *AppConfig) { c.RedirectURI = "http://pipewright.example/callback" }},
		{"callback-userinfo", func(c *AppConfig) { c.RedirectURI = "https://user:pass@pipewright.example/callback" }},
		{"callback-query", func(c *AppConfig) { c.RedirectURI += "?code=bad" }},
		{"callback-fragment", func(c *AppConfig) { c.RedirectURI += "#" }},
		{"wrong-provider", func(c *AppConfig) { c.Provider = "custom" }},
		{"missing-gitee-capability", func(c *AppConfig) { c.Provider = ProviderGitee; c.GiteeMode = "" }},
		{"unknown-gitee-capability", func(c *AppConfig) { c.Provider = ProviderGitee; c.GiteeMode = "auto" }},
		{"tenant-url", func(c *AppConfig) { c.Provider = ProviderMicrosoft; c.MicrosoftTenant = "https://untrusted.example" }},
		{"tenant-path", func(c *AppConfig) { c.Provider = ProviderMicrosoft; c.MicrosoftTenant = "common/../../other" }},
		{"secret-control", func(c *AppConfig) { c.ClientSecret += "\n" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := adapterConfig(ProviderGitHub)
			tc.change(&c)
			if !errors.Is(ValidateAppConfig(c), ErrProviderUnavailable) {
				t.Fatal("configuration accepted")
			}
		})
	}
	for _, callback := range []string{"http://127.0.0.1:18090/api/auth/platform/github/callback", "http://[::1]:18090/callback"} {
		c := adapterConfig(ProviderGitHub)
		c.RedirectURI = callback
		if err := ValidateAppConfig(c); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitProviderExchangeUsesExactIdentityAndMinimalRequest(t *testing.T) {
	for _, name := range []string{ProviderGitHub, ProviderGitLab, ProviderGitee} {
		for _, mode := range []string{GiteeModeConfidentialCode, GiteeModeS256} {
			if name != ProviderGitee && mode != GiteeModeConfidentialCode {
				continue
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				var tokenCalls, userCalls int
				transport := providerRoundTripper(func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPost {
						tokenCalls++
						if req.GetBody != nil || !req.Close {
							t.Fatal("token request remains replayable")
						}
						if err := req.ParseForm(); err != nil {
							t.Fatal(err)
						}
						if req.Form.Get("grant_type") != "authorization_code" || req.Form.Get("client_id") != "test-client" || req.Form.Get("client_secret") != "test-canary-secret" || req.Form.Get("code") != "test-code" || req.Header.Get("Authorization") != "" {
							t.Fatal("incorrect confidential code exchange")
						}
						if req.Form.Get("code_verifier") != "" != (name != ProviderGitee || mode == GiteeModeS256) {
							t.Fatal("wrong PKCE capability")
						}
						return providerResponse(200, `{"access_token":"test-canary-token","token_type":"bearer","refresh_token":"must-not-retain"}`), nil
					}
					userCalls++
					if req.URL.RawQuery != "" || req.Header.Get("Authorization") != "Bearer test-canary-token" {
						t.Fatal("token leaked to URL")
					}
					return providerResponse(200, `{"id":900719925474099312345,"login":"old-name\n","username":"gitlab-name","avatar_url":"https://untrusted.example/avatar"}`), nil
				})
				adapter := NewDefaultAdapters(&http.Client{Transport: transport})[name]
				c := adapterConfig(name)
				c.GiteeMode = mode
				identity, err := adapter.Exchange(context.Background(), c, ExchangeParams{Code: "test-code", Verifier: testVerifier})
				if err != nil {
					t.Fatal(err)
				}
				if identity.Subject != "900719925474099312345" || identity.Issuer != providerDefinitions[name].issuer || identity.Provider != name || tokenCalls != 1 || userCalls != 1 {
					t.Fatalf("wrong identity or request counts: %+v %d/%d", identity, tokenCalls, userCalls)
				}
				if strings.ContainsAny(identity.DisplayLabel, "\r\n") {
					t.Fatal("unsafe display label")
				}
			})
		}
	}
}

func TestProviderNumericSubjectsNeverUseFloat(t *testing.T) {
	valid := []string{`1`, `9007199254740993`, `"900719925474099312345"`}
	for _, raw := range valid {
		result, err := numericSubject(json.RawMessage(raw))
		if err != nil || result != strings.Trim(raw, "\"") {
			t.Errorf("lost exact ID %s", raw)
		}
	}
	for _, raw := range []string{`0`, `-1`, `1.0`, `1e3`, `null`, `""`, `"01"`, `" 12"`, `{}`, `true`, `"x"`, strings.Repeat("9", 41)} {
		if _, err := numericSubject(json.RawMessage(raw)); !errors.Is(err, ErrIdentityInvalid) {
			t.Errorf("invalid ID accepted: %s", raw)
		}
	}
}

func TestTokenFailureNeverRetriesOrLeaksUpstream(t *testing.T) {
	for _, scenario := range []string{"401", "500", "redirect", "network", "malformed", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			var calls int
			transport := providerRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				switch scenario {
				case "401":
					return providerResponse(401, `{"error":"invalid_client","error_description":"test-canary-secret test-code"}`), nil
				case "500":
					return providerResponse(500, "test-canary-secret"), nil
				case "network":
					return nil, errors.New("uncertain test-code test-canary-secret")
				case "redirect":
					resp := providerResponse(307, "test-canary-secret")
					resp.Header.Set("Location", "https://untrusted.example/steal")
					return resp, nil
				case "malformed":
					return providerResponse(200, `{"access_token":`), nil
				default:
					resp := providerResponse(200, strings.Repeat("x", maxProviderBody+1))
					resp.ContentLength = -1
					return resp, nil
				}
			})
			adapter := NewDefaultAdapters(&http.Client{Transport: transport})[ProviderGitHub]
			_, err := adapter.Exchange(context.Background(), adapterConfig(ProviderGitHub), ExchangeParams{Code: "test-code", Verifier: testVerifier})
			if err == nil || calls != 1 {
				t.Fatalf("exchange err=%v calls=%d", err, calls)
			}
			for _, forbidden := range []string{"test-code", "test-canary-secret", "untrusted.example"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatal("upstream data leaked")
				}
			}
		})
	}
}

func TestGiteePKCEModeDoesNotFallback(t *testing.T) {
	for _, mode := range []string{GiteeModeS256, GiteeModeConfidentialCode} {
		t.Run(mode, func(t *testing.T) {
			var calls int
			transport := providerRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if mode == GiteeModeS256 && req.Form.Get("code_verifier") == "" {
					t.Fatal("PKCE removed after failure")
				}
				return providerResponse(400, `{"error":"invalid_grant"}`), nil
			})
			config := adapterConfig(ProviderGitee)
			config.GiteeMode = mode
			_, err := NewDefaultAdapters(&http.Client{Transport: transport})[ProviderGitee].Exchange(context.Background(), config, ExchangeParams{Code: "test-code", Verifier: testVerifier})
			if err == nil || calls != 1 {
				t.Fatalf("mode fallback: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestUserInfoAndTransportLimits(t *testing.T) {
	for _, scenario := range []string{"bad-id", "missing-id", "userinfo-redirect", "userinfo-error", "userinfo-large"} {
		t.Run(scenario, func(t *testing.T) {
			var calls int
			transport := providerRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method == http.MethodPost {
					return providerResponse(200, `{"access_token":"test-canary-token"}`), nil
				}
				switch scenario {
				case "bad-id":
					return providerResponse(200, `{"id":1.25,"login":"name"}`), nil
				case "missing-id":
					return providerResponse(200, `{"login":"same-email-is-not-identity"}`), nil
				case "userinfo-redirect":
					resp := providerResponse(302, "test-canary-token")
					resp.Header.Set("Location", "https://untrusted.example")
					return resp, nil
				case "userinfo-error":
					return providerResponse(500, "test-canary-token"), nil
				default:
					resp := providerResponse(200, strings.Repeat("x", maxProviderBody+1))
					resp.ContentLength = -1
					return resp, nil
				}
			})
			_, err := NewDefaultAdapters(&http.Client{Transport: transport})[ProviderGitHub].Exchange(context.Background(), adapterConfig(ProviderGitHub), ExchangeParams{Code: "test-code", Verifier: testVerifier})
			if err == nil || calls != 2 || strings.Contains(err.Error(), "test-canary-token") {
				t.Fatalf("userinfo failure: err=%v calls=%d", err, calls)
			}
		})
	}
	client := providerHTTP(&http.Client{Transport: providerRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe request reached network")
		return nil, nil
	})})
	for _, raw := range []string{"http://api.github.com/user", "https://api.github.com:443/user", "https://api.github.com/user?access_token=secret", "https://untrusted.example/user", "https://login.microsoftonline.com/evil/discovery/v2.0/keys"} {
		req, _ := http.NewRequest(http.MethodGet, raw, nil)
		if _, err := client.Do(req); err == nil {
			t.Fatalf("unsafe URL accepted %s", raw)
		}
	}
}

func TestInvalidExchangeInputsDoNotSendRequests(t *testing.T) {
	var calls int
	adapters := NewDefaultAdapters(&http.Client{Transport: providerRoundTripper(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })})
	for _, name := range []string{ProviderGitHub, ProviderGoogle} {
		params := []ExchangeParams{
			{Code: "", Verifier: testVerifier, NonceHash: nonceHash(testNonce)},
			{Code: "code", Verifier: "bad", NonceHash: nonceHash(testNonce)},
		}
		if name == ProviderGoogle {
			params = append(params, ExchangeParams{Code: "code", Verifier: testVerifier, NonceHash: "bad"})
		}
		for _, p := range params {
			if _, err := adapters[name].Exchange(context.Background(), adapterConfig(name), p); err == nil {
				t.Fatal("invalid params accepted")
			}
		}
	}
	if calls != 0 {
		t.Fatalf("bad inputs sent %d requests", calls)
	}
}
