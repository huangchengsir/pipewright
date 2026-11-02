package httpapi

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authlogin"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"testing"
)

func TestPlatformReviewCanonicalPublicOrigin(t *testing.T) {
	for _, origin := range []string{"https://PUBLIC.example", "https://public.example:443"} {
		t.Run(origin, func(t *testing.T) {
			f := platformSetup(t)
			f.seedBound(t)
			if parsed, err := authlogin.PublicOrigin(origin, false); err != nil {
				t.Fatalf("config rejected: %v", err)
			} else {
				t.Logf("accepted configured origin=%s", parsed)
			}
			s := authlogin.New(f.store.DB, vault.New(f.store.DB, testMasterKey()), f.auth, authlogin.Config{PublicURL: origin, Adapters: map[string]authlogin.Adapter{"github": f.adapter}})
			f.handler = New(testWebFSAuth(), f.auth, WithPlatformLogin(s))
			w := f.request(t, "POST", "/api/auth/platform/github/start", `{}`, false, false, "https://public.example")
			if w.Code != http.StatusOK {
				t.Fatalf("same browser origin should start sign-in; status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
func TestPlatformReviewCurrentRevokeAudit(t *testing.T) {
	f := platformSetup(t)
	w := f.request(t, "DELETE", "/api/account/sessions/"+auth.SessionID(f.session.Token), "", true, true, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke=%d %s", w.Code, w.Body.String())
	}
	var n int
	if err := f.store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action='session_revoke'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("successful current-session revoke should retain audit; rows=%d", n)
	}
}
func TestPlatformReviewPasswordChangeLockoutHTTP(t *testing.T) {
	f := platformSetup(t)
	for i := 0; i < auth.LockoutThreshold; i++ {
		f.request(t, "POST", "/api/account/password", `{"currentPassword":"wrong","newPassword":"new-valid-password"}`, true, true, "")
	}
	w := f.request(t, "POST", "/api/account/password", `{"currentPassword":"testpass","newPassword":"new-valid-password"}`, true, true, "")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("missing lockout retry hint")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out password change should be 429; status=%d body=%s", w.Code, w.Body.String())
	}
}
