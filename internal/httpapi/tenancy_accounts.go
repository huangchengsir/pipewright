package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func WithTenancyAccounts(accounts *tenancy.Accounts) Option {
	return func(o *options) { o.accessAccounts = accounts }
}
func accessError(w http.ResponseWriter, err error) {
	var locked *auth.ErrLockedOut
	switch {
	case errors.As(err, &locked):
		seconds := int(locked.RetryAfter.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, 429, "rate_limited", "操作过于频繁，请稍后再试")
	case errors.Is(err, configstate.ErrMaintenance), errors.Is(err, configstate.ErrMaintenanceForbidden):
		writeError(w, 409, "maintenance_active", "维护期间暂不能修改配置")
	case errors.Is(err, authz.ErrInvalidPolicy):
		writeError(w, 400, "invalid_policy", "权限配置无效，请检查菜单、动作和数据范围")
	case errors.Is(err, tenancy.ErrForbidden):
		writeError(w, 403, "access_denied", "没有此操作的权限")
	case errors.Is(err, tenancy.ErrProtectedAccount):
		writeError(w, 409, "protected_account", "不能修改受保护的管理员")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, 400, "weak_password", "密码不符合要求")
	case errors.Is(err, tenancy.ErrInvalidInput):
		writeError(w, 400, "invalid_account", "请检查账户信息")
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidCurrentPassword):
		writeError(w, 400, "invalid_current_password", "当前密码不正确")
	case errors.Is(err, tenancy.ErrAccountConflict), errors.Is(err, tenancy.ErrConflict):
		writeError(w, 409, "account_conflict", "账户状态已变化，请刷新后重试")
	default:
		writeError(w, 500, "internal", "服务器内部错误")
	}
}
func accessActor(w http.ResponseWriter, r *http.Request) (tenancy.Actor, bool) {
	session, _ := sessionFromContext(r.Context())
	actor, err := tenancy.ActorFromSession(session)
	if err != nil {
		accessError(w, err)
		return tenancy.Actor{}, false
	}
	return actor, true
}
func accessAudit(r *http.Request, aud audit.Recorder, action, target string) {
	if aud == nil {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if session == nil {
		return
	}
	_ = aud.Record(r.Context(), audit.Entry{Actor: session.UserID, Action: action, TargetType: "account", TargetID: target})
}
func mountTenancyAccounts(router chi.Router, accounts *tenancy.Accounts, aud audit.Recorder, policy authCookiePolicy) {
	if accounts == nil {
		return
	}
	router.Get("/access/users", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		a, ok := accessActor(w, r)
		if !ok {
			return
		}
		out, err := accounts.Users(r.Context(), a, r.URL.Query().Get("after"))
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	router.Get("/access/self/sessions", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		a, ok := accessActor(w, r)
		if !ok {
			return
		}
		out, err := accounts.Sessions(r.Context(), a)
		if err != nil {
			accessError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"sessions": out})
	})
	router.Delete("/access/self/sessions/{sessionId}", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		a, ok := accessActor(w, r)
		if !ok {
			return
		}
		if err := accounts.RevokeSession(r.Context(), a, chi.URLParam(r, "sessionId")); err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "account_session_revoke", a.UserID)
		w.WriteHeader(204)
	})
	router.Get("/access/self", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		s, _ := sessionFromContext(r.Context())
		if _, ok := accessActor(w, r); !ok {
			return
		}
		writeJSON(w, 200, tenantSessionDTO(s))
	})
	router.Patch("/access/self", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		actor, ok := accessActor(w, r)
		if !ok {
			return
		}
		var in struct {
			DisplayName string `json:"displayName"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		if err := accounts.UpdateDisplayName(r.Context(), actor, actor.UserID, in.DisplayName); err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "account_profile_update", actor.UserID)
		w.WriteHeader(http.StatusNoContent)
	})
	router.Post("/access/self/password", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		actor, ok := accessActor(w, r)
		if !ok {
			return
		}
		var in struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		session, err := accounts.ChangePassword(r.Context(), currentSessionToken(r), in.CurrentPassword, in.NewPassword)
		if err != nil {
			accessError(w, err)
			return
		}
		setAuthCookies(w, r, &auth.Session{Token: session.Token, CSRFToken: session.CSRFToken, ExpiresAt: time.Unix(session.ExpiresAt, 0)}, policy)
		accessAudit(r, aud, "account_password_change", actor.UserID)
		writeJSON(w, 200, map[string]bool{"mustChangePassword": false})
	})
	router.Post("/access/users", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		actor, ok := accessActor(w, r)
		if !ok {
			return
		}
		var in tenancy.CreateUserInput
		if !platformJSON(w, r, &in) {
			return
		}
		out, err := accounts.Create(r.Context(), actor, in)
		if err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "account_create", out.User.ID)
		writeJSON(w, http.StatusCreated, out)
	})
	router.Patch("/access/users/{userId}", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		actor, ok := accessActor(w, r)
		if !ok {
			return
		}
		var in struct {
			DisplayName string `json:"displayName"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		id := chi.URLParam(r, "userId")
		if err := accounts.UpdateDisplayName(r.Context(), actor, id, in.DisplayName); err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "account_profile_update", id)
		w.WriteHeader(http.StatusNoContent)
	})
	router.Post("/access/users/{userId}/reset-password", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		actor, ok := accessActor(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "userId")
		out, err := accounts.ResetPassword(r.Context(), actor, id)
		if err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "account_password_reset", id)
		writeJSON(w, 200, out)
	})
	router.Put("/access/users/{userId}/enabled", func(w http.ResponseWriter, r *http.Request) {
		platformHeaders(w)
		actor, ok := accessActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !platformJSON(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			accessError(w, tenancy.ErrInvalidInput)
			return
		}
		id := chi.URLParam(r, "userId")
		if err := accounts.SetEnabled(r.Context(), actor, id, *in.Enabled); err != nil {
			accessError(w, err)
			return
		}
		accessAudit(r, aud, "account_enabled_change", id)
		w.WriteHeader(http.StatusNoContent)
	})
}
