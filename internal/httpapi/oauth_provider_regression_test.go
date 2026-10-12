package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/gitauth"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestIssue188GiteaCallbackAndRepositoryCredential(t *testing.T) {
	for _, prefix := range []string{"", "/gitea"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			const token = "synthetic-gitea-token"
			tokenCalls, userCalls, repoCalls := 0, 0, 0
			var redirectURI string
			mux := http.NewServeMux()
			mux.HandleFunc(prefix+"/login/oauth/authorize", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "synthetic authorization page") })
			mux.HandleFunc(prefix+"/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
				tokenCalls++
				if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "synthetic-secret" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "valid-code" || r.Form.Get("redirect_uri") != redirectURI {
					t.Error("incorrect token request")
				}
				io.WriteString(w, `{"access_token":"`+token+`","token_type":"bearer"}`)
			})
			mux.HandleFunc(prefix+"/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
				userCalls++
				if r.Header.Get("Authorization") != "Bearer "+token || r.URL.RawQuery != "" {
					t.Error("user authentication must be header-only")
				}
				io.WriteString(w, `{"login":"alice","id":7}`)
			})
			mux.HandleFunc(prefix+"/api/v1/user/repos", func(w http.ResponseWriter, r *http.Request) {
				repoCalls++
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("wrong repository bearer token")
				}
				io.WriteString(w, `[{"full_name":"alice/private-repo","private":true}]`)
			})
			provider := httptest.NewServer(mux)
			defer provider.Close()
			api, client, csrf, v := setupOAuthServer(t, provider.Client())
			redirectURI = api.URL + "/api/oauth/gitea/callback"
			payload, _ := json.Marshal(map[string]any{"clientId": "cid", "clientSecret": "synthetic-secret", "baseUrl": provider.URL + prefix + "/", "enabled": true})
			saved := doJSON(t, client, http.MethodPut, api.URL+"/api/oauth/apps/gitea", csrf, string(payload))
			body, _ := io.ReadAll(saved.Body)
			saved.Body.Close()
			if saved.StatusCode != 200 || strings.Contains(string(body), "synthetic-secret") {
				t.Fatalf("save status=%d", saved.StatusCode)
			}
			authorized, err := client.Get(api.URL + "/api/oauth/gitea/authorize")
			if err != nil {
				t.Fatal(err)
			}
			authorized.Body.Close()
			location, _ := url.Parse(authorized.Header.Get("Location"))
			if authorized.StatusCode != 302 || location.Path != prefix+"/login/oauth/authorize" || location.Query().Get("scope") != "read:user read:repository" || location.Query().Get("redirect_uri") != redirectURI {
				t.Fatal("wrong authorization destination or parameters")
			}
			page, err := provider.Client().Get(location.String())
			if err != nil {
				t.Fatal(err)
			}
			page.Body.Close()
			if page.StatusCode != 200 {
				t.Fatalf("authorize route=%d", page.StatusCode)
			}
			callback := api.URL + "/api/oauth/gitea/callback?code=valid-code&state=" + url.QueryEscape(location.Query().Get("state"))
			completed, err := client.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			completed.Body.Close()
			if completed.StatusCode != 302 || completed.Header.Get("Location") != "/settings/vault?connected=gitea&account=alice" {
				t.Fatalf("callback: status=%d location=%s", completed.StatusCode, completed.Header.Get("Location"))
			}
			creds, err := v.List()
			if err != nil || len(creds) != 1 || creds[0].Name != "gitea:alice" || creds[0].Type != vault.TypeGitToken {
				t.Fatal("missing Gitea credential")
			}
			stored, err := v.Reveal(creds[0].ID)
			if err != nil || stored != token {
				t.Fatal("vault token mismatch")
			}
			// Production repository cloning consumes this same vault token via BasicAuth.
			auth := gitauth.BasicAuth(provider.URL+prefix+"/alice/private-repo.git", "", stored)
			if auth.Username == "" || auth.Password != token {
				t.Fatal("repository credential incompatible")
			}
			req, _ := http.NewRequest(http.MethodGet, provider.URL+prefix+"/api/v1/user/repos", nil)
			req.Header.Set("Authorization", "Bearer "+stored)
			repos, err := provider.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			repos.Body.Close()
			if repos.StatusCode != 200 || repoCalls != 1 {
				t.Fatal("repository token control failed")
			}
			replay, err := client.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			replay.Body.Close()
			if !strings.Contains(replay.Header.Get("Location"), "invalid_state") || tokenCalls != 1 || userCalls != 1 {
				t.Fatal("callback state was reusable")
			}
		})
	}
}
