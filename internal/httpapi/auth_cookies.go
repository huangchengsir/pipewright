package httpapi

import (
	"github.com/huangchengsir/pipewright/internal/auth"
	"net/http"
	"time"
)

type authCookiePolicy struct{ Secure bool }

func cookieSecure(r *http.Request, policies []authCookiePolicy) bool {
	if len(policies) > 0 && policies[0].Secure {
		return true
	}
	return r.TLS != nil
}
func setAuthCookies(w http.ResponseWriter, r *http.Request, s *auth.Session, policy authCookiePolicy) {
	for _, c := range []*http.Cookie{
		{Name: cookieSession, Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: cookieSecure(r, []authCookiePolicy{policy}), Expires: s.ExpiresAt},
		{Name: cookieCsrf, Value: s.CSRFToken, Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, Secure: cookieSecure(r, []authCookiePolicy{policy}), Expires: s.ExpiresAt},
	} {
		http.SetCookie(w, c)
	}
}
func clearAuthCookies(w http.ResponseWriter, r *http.Request, policy authCookiePolicy) {
	for _, name := range []string{cookieSession, cookieCsrf} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, Expires: time.Unix(0, 0), HttpOnly: name == cookieSession, Secure: cookieSecure(r, []authCookiePolicy{policy}), SameSite: http.SameSiteLaxMode})
	}
}
