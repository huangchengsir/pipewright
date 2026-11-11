package vault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// Exercise the domain queries as well as data copying: an OAuth lookup must
// parse even when the credential has no binding (DM reserves LOGIN).
func TestCredentialListOAuthPortability(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		v := New(st.DB, testKey())
		plain, e := v.Create(CreateInput{Name: "ordinary 中文😀", Type: TypeGitToken, Secret: "plain-synthetic-secret"})
		if e != nil {
			t.Fatal(e)
		}
		list, e := v.List()
		if e != nil {
			t.Fatal(e)
		}
		if len(list) != 1 || list[0].ID != plain.ID || list[0].OAuth != nil || list[0].LastUsedAt != nil {
			t.Fatal("ordinary list view changed")
		}
		name := "renamed"
		if _, e = v.Update(plain.ID, UpdateInput{Name: &name}); e != nil {
			t.Fatal(e)
		}
		sealed, e := v.SealSecret([]byte("app-synthetic-secret"))
		if e != nil {
			t.Fatal(e)
		}
		stamp := time.Now().UTC().Format(time.RFC3339)
		_, e = st.DB.ExecContext(ctx, "INSERT INTO oauth_apps(provider,client_id,client_secret_ciphertext,base_url,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", "github", "fixture-client", sealed, "", 1, stamp, stamp)
		if e != nil {
			t.Fatal(e)
		}
		repo := NewOAuthRepository(st.DB, v)
		binding := OAuthBinding{Provider: "github", Issuer: "https://identity.invalid", ClientID: "fixture-client", RedirectURI: "https://callback.invalid", AppRevision: OAuthAppRevision("fixture-client", "", sealed, true), AccountID: "fixture-account", Login: "login 中文😀"}
		id, e := repo.Authorize(ctx, binding, OAuthTokens{AccessToken: "access-synthetic-secret", RefreshToken: "refresh-synthetic-secret"}, OAuthTarget{})
		if e != nil {
			t.Fatal(e)
		}
		current, e := repo.Binding(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if current == nil || current.Login != binding.Login || current.ExpiresAt != nil {
			t.Fatal("OAuth binding metadata changed")
		}
		refresh, e := v.OpenSecret(current.RefreshCiphertext)
		if e != nil {
			t.Fatal(e)
		}
		if string(refresh) != "refresh-synthetic-secret" {
			t.Fatal("refresh token changed")
		}
		clear(refresh)
		access, e := v.Reveal(id)
		if e != nil {
			t.Fatal(e)
		}
		if access != "access-synthetic-secret" {
			t.Fatal("access token changed")
		}
		list, e = v.List()
		if e != nil {
			t.Fatal(e)
		}
		if len(list) != 2 {
			t.Fatal("list lost credentials")
		}
		found := false
		for _, c := range list {
			if c.ID == id {
				found = true
				if c.OAuth == nil || !c.OAuth.Managed || c.OAuth.Account != binding.Login || c.OAuth.Status != OAuthActive || !c.OAuth.Refreshable || c.LastUsedAt != nil {
					t.Fatal("OAuth public view changed")
				}
			}
		}
		if !found {
			t.Fatal("OAuth credential absent")
		}
		raw, e := json.Marshal(list)
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{"plain-synthetic-secret", "app-synthetic-secret", "access-synthetic-secret", "refresh-synthetic-secret"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("list exposes secret")
			}
		}
		target, e := repo.Target(ctx, id, false)
		if e != nil {
			t.Fatal(e)
		}
		target, e = repo.ClaimAuthorization(ctx, target, "fixture-owner")
		if e != nil {
			t.Fatal(e)
		}
		binding.Login = "login updated"
		expires := time.Now().Add(time.Hour).UTC()
		_, e = repo.Authorize(ctx, binding, OAuthTokens{AccessToken: "rotated-synthetic-secret", ExpiresAt: &expires}, target)
		if e != nil {
			t.Fatal(e)
		}
		info, e := OAuthView(ctx, st.DB, id)
		if e != nil {
			t.Fatal(e)
		}
		if info == nil || info.Account != binding.Login || info.Revision != 2 || info.Refreshable || info.ExpiresAt == nil {
			t.Fatal("reauthorization view changed")
		}
	})
}
