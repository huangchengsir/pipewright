package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authlogin"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func WithPlatformLogin(s *authlogin.Service) Option { return func(o *options) { o.platformLogin = s } }
func platformHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
func platformJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		writeError(w, 400, "bad_request", "请求体格式错误")
		return false
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		writeError(w, 400, "bad_request", "请求体格式错误")
		return false
	}
	return true
}
func platformError(w http.ResponseWriter, e error) {
	status, code := 500, "internal"
	switch {
	case errors.Is(e, auth.ErrSessionNotFound):
		status, code = 401, "unauthorized"
	case errors.Is(e, auth.ErrInvalidCurrentPassword):
		status, code = 422, "invalid_current_password"
	case errors.Is(e, authlogin.ErrNotConfigured):
		status, code = 409, "platform_not_configured"
	case errors.Is(e, authlogin.ErrDisabled):
		status, code = 409, "platform_disabled"
	case errors.Is(e, authlogin.ErrConflict):
		status, code = 409, "platform_config_conflict"
	case errors.Is(e, authlogin.ErrReauthRequired):
		status, code = 403, "platform_reauth_required"
	case errors.Is(e, authlogin.ErrFlowInvalid):
		status, code = 400, "platform_flow_invalid"
	case errors.Is(e, authlogin.ErrFlowExpired):
		status, code = 410, "platform_flow_expired"
	case errors.Is(e, authlogin.ErrFlowConsumed):
		status, code = 409, "platform_flow_consumed"
	case errors.Is(e, authlogin.ErrIdentityDenied), errors.Is(e, authlogin.ErrIdentityInvalid):
		status, code = 403, "platform_identity_denied"
	case errors.Is(e, authlogin.ErrCandidateInvalid):
		status, code = 409, "platform_candidate_invalid"
	case errors.Is(e, authlogin.ErrProviderRejected):
		status, code = 400, "platform_provider_rejected"
	case errors.Is(e, authlogin.ErrResultUncertain):
		status, code = 502, "platform_result_uncertain"
	case errors.Is(e, authlogin.ErrProviderUnavailable):
		status, code = 502, "platform_provider_unavailable"
	case errors.Is(e, authlogin.ErrRateLimited):
		status, code = 429, "platform_rate_limited"
	case errors.Is(e, authlogin.ErrBadRequest):
		status, code = 400, "bad_request"
	}
	var locked *auth.ErrLockedOut
	if errors.As(e, &locked) {
		status, code = 429, "locked_out"
	}
	writeError(w, status, code, "登录设置操作未完成，请重试")
}
func platformService(w http.ResponseWriter, s *authlogin.Service) bool {
	platformHeaders(w)
	if s == nil {
		writeError(w, 503, "platform_unavailable", "第三方登录暂不可用")
		return false
	}
	return true
}
func platformOrigin(w http.ResponseWriter, r *http.Request, s *authlogin.Service) bool {
	origin := r.Header.Get("Origin")
	if s.PublicOrigin() == "" {
		platformError(w, authlogin.ErrNotConfigured)
		return false
	}
	if origin != s.PublicOrigin() || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, 403, "platform_origin_invalid", "请求来源无效")
		return false
	}
	return true
}
func flowCookieName(s *authlogin.Service, id string) string {
	prefix := "pipewright_flow_"
	if s.SecureCookies() {
		prefix = "__Host-" + prefix
	}
	return prefix + id
}
func setFlowCookie(w http.ResponseWriter, s *authlogin.Service, start authlogin.StartResult) {
	expiry, _ := time.Parse(time.RFC3339, start.ExpiresAt)
	http.SetCookie(w, &http.Cookie{Name: flowCookieName(s, start.FlowID), Value: start.BrowserNonce, Path: "/", HttpOnly: true, Secure: s.SecureCookies(), SameSite: http.SameSiteLaxMode, Expires: expiry, MaxAge: 600})
}
func clearFlowCookie(w http.ResponseWriter, s *authlogin.Service, id string) {
	http.SetCookie(w, &http.Cookie{Name: flowCookieName(s, id), Path: "/", HttpOnly: true, Secure: s.SecureCookies(), SameSite: http.SameSiteLaxMode, Expires: time.Unix(0, 0), MaxAge: -1})
}
func flowCookieCapacity(r *http.Request) bool {
	n := 0
	for _, c := range r.Cookies() {
		if strings.HasPrefix(c.Name, "pipewright_flow_") || strings.HasPrefix(c.Name, "__Host-pipewright_flow_") {
			n++
		}
	}
	return n < 8
}

type startRate struct {
	mu      sync.Mutex
	entries map[string]rateWindow
}
type rateWindow struct {
	at time.Time
	n  int
}

func (l *startRate) allow(r *http.Request) bool {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = make(map[string]rateWindow)
	}
	if len(l.entries) > 1024 {
		for key, v := range l.entries {
			if now.Sub(v.at) >= time.Minute {
				delete(l.entries, key)
			}
		}
		if len(l.entries) > 1024 {
			return false
		}
	}
	v := l.entries[host]
	if now.Sub(v.at) >= time.Minute {
		v = rateWindow{at: now}
	}
	v.n++
	l.entries[host] = v
	return v.n <= 20
}
func mountPlatformPublic(r chi.Router, s *authlogin.Service) {
	limiter := &startRate{}
	r.Get("/api/auth/platform/providers", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		providers, e := s.PublicProviders(r.Context())
		if e != nil {
			platformError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"providers": providers})
	})
	r.Post("/api/auth/platform/{provider}/start", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) || !platformOrigin(w, r, s) {
			return
		}
		if !limiter.allow(r) || !flowCookieCapacity(r) {
			platformError(w, authlogin.ErrRateLimited)
			return
		}
		var in struct {
			ReturnTo string `json:"returnTo"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		start, e := s.Start(r.Context(), chi.URLParam(r, "provider"), "login", "", in.ReturnTo, 0)
		if e != nil {
			platformError(w, e)
			return
		}
		setFlowCookie(w, s, start)
		writeJSON(w, 200, start)
	})
	r.Get("/api/auth/platform/{provider}/callback", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		q := r.URL.Query()
		state := q.Get("state")
		id := authlogin.FlowIDFromState(state)
		outcome := "invalid"
		var complete authlogin.Completion
		var err error = authlogin.ErrFlowInvalid
		if id != "" && len(q["state"]) == 1 && len(q["code"]) <= 1 && len(q["error"]) <= 1 && !(q.Get("code") != "" && q.Get("error") != "") {
			if cookie, e := r.Cookie(flowCookieName(s, id)); e == nil {
				complete, err = s.Complete(r.Context(), chi.URLParam(r, "provider"), state, cookie.Value, currentSessionToken(r), q.Get("code"), q.Get("error") != "")
				clearFlowCookie(w, s, id)
			}
		}
		if err == nil {
			if complete.Session != nil {
				setAuthCookies(w, r, complete.Session, authCookiePolicy{Secure: s.SecureCookies()})
				http.Redirect(w, r, "/login?platform_result=success&returnTo="+url.QueryEscape(complete.ReturnTo), http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/settings/account?platform_binding="+url.QueryEscape(complete.BindingFlow), http.StatusSeeOther)
			return
		}
		switch {
		case errors.Is(err, authlogin.ErrProviderRejected):
			outcome = "unavailable"
			if q.Get("error") == "access_denied" {
				outcome = "cancelled"
			}
		case errors.Is(err, authlogin.ErrFlowExpired):
			outcome = "expired"
		case errors.Is(err, authlogin.ErrIdentityDenied), errors.Is(err, authlogin.ErrIdentityInvalid):
			outcome = "denied"
		case errors.Is(err, authlogin.ErrResultUncertain):
			outcome = "uncertain"
		case errors.Is(err, authlogin.ErrProviderUnavailable), errors.Is(err, authlogin.ErrNotConfigured):
			outcome = "unavailable"
		}
		http.Redirect(w, r, "/login?platform_result="+outcome, http.StatusSeeOther)
	})
}
func platformAudit(r *http.Request, aud audit.Recorder, action, p string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	recordAudit(ctx, aud, audit.Entry{Actor: auditActor, Action: action, TargetType: audit.TargetAccount, TargetID: p, Detail: map[string]any{"provider": p}, IP: clientIP(r)})
}
func mountPlatformAccount(r chi.Router, s *authlogin.Service, a auth.Authenticator, aud audit.Recorder) {
	r.Post("/account/reauth", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		svc, ok := a.(interface {
			Reauthenticate(string, string) (time.Time, error)
		})
		if !ok {
			writeError(w, 503, "platform_unavailable", "第三方登录暂不可用")
			return
		}
		var in struct {
			CurrentPassword string `json:"currentPassword"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		until, e := svc.Reauthenticate(currentSessionToken(r), in.CurrentPassword)
		if e != nil {
			platformError(w, e)
			return
		}
		writeJSON(w, 200, map[string]string{"reauthenticatedUntil": until.UTC().Format(time.RFC3339)})
	})
	r.Get("/account/platform-login", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		out, e := s.Settings(r.Context(), currentSessionToken(r))
		if e != nil {
			platformError(w, e)
			return
		}
		writeJSON(w, 200, out)
	})
	r.Put("/account/platform-login/visibility", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		var in struct {
			ExpectedVersion int64           `json:"expectedVersion"`
			Visibility      map[string]bool `json:"visibility"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		out, e := s.SaveVisibility(r.Context(), currentSessionToken(r), in.ExpectedVersion, in.Visibility)
		if e != nil {
			platformError(w, e)
			return
		}
		platformAudit(r, aud, "platform_visibility_save", "all")
		writeJSON(w, 200, out)
	})
	r.Put("/account/platform-login/{provider}/app", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		var in authlogin.AppInput
		if !platformJSON(w, r, &in) {
			return
		}
		p := chi.URLParam(r, "provider")
		out, e := s.SaveApp(r.Context(), currentSessionToken(r), p, in)
		if e != nil {
			platformError(w, e)
			return
		}
		platformAudit(r, aud, "platform_app_save", p)
		writeJSON(w, 200, out)
	})
	r.Post("/account/platform-login/{provider}/bind/start", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) || !platformOrigin(w, r, s) {
			return
		}
		if !flowCookieCapacity(r) {
			platformError(w, authlogin.ErrRateLimited)
			return
		}
		var in struct {
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		p := chi.URLParam(r, "provider")
		start, e := s.Start(r.Context(), p, "bind", currentSessionToken(r), "/settings/account", in.ExpectedVersion)
		if e != nil {
			platformError(w, e)
			return
		}
		setFlowCookie(w, s, start)
		platformAudit(r, aud, "platform_bind_start", p)
		writeJSON(w, 200, start)
	})
	r.Get("/account/platform-login/bind/pending/{flowId}", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		out, e := s.Pending(r.Context(), currentSessionToken(r), chi.URLParam(r, "flowId"))
		if e != nil {
			platformError(w, e)
			return
		}
		writeJSON(w, 200, out)
	})
	r.Post("/account/platform-login/{provider}/bind/confirm", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		var in struct {
			ExpectedVersion int64  `json:"expectedVersion"`
			FlowID          string `json:"flowId"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		p := chi.URLParam(r, "provider")
		out, e := s.Confirm(r.Context(), currentSessionToken(r), p, in.FlowID, in.ExpectedVersion)
		if e != nil {
			platformError(w, e)
			return
		}
		platformAudit(r, aud, "platform_binding_confirm", p)
		writeJSON(w, 200, out)
	})
	r.Post("/account/platform-login/{provider}/enabled", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		var in struct {
			ExpectedVersion int64 `json:"expectedVersion"`
			Enabled         bool  `json:"enabled"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		p := chi.URLParam(r, "provider")
		out, e := s.SetEnabled(r.Context(), currentSessionToken(r), p, in.ExpectedVersion, in.Enabled)
		if e != nil {
			platformError(w, e)
			return
		}
		platformAudit(r, aud, "platform_enabled_change", p)
		writeJSON(w, 200, out)
	})
	r.Delete("/account/platform-login/{provider}/binding", func(w http.ResponseWriter, r *http.Request) {
		if !platformService(w, s) {
			return
		}
		var in struct {
			ExpectedVersion int64 `json:"expectedVersion"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		p := chi.URLParam(r, "provider")
		out, e := s.Unbind(r.Context(), currentSessionToken(r), p, in.ExpectedVersion)
		if e != nil {
			platformError(w, e)
			return
		}
		platformAudit(r, aud, "platform_binding_remove", p)
		writeJSON(w, 200, out)
	})
}
