package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/terminalhttp"
)

func WithTerminalHTTP(m *terminalhttp.Manager) Option { return func(o *options) { o.terminalHTTP = m } }
func terminalOwner(r *http.Request) string {
	s, ok := sessionFromContext(r.Context())
	if !ok {
		return ""
	}
	h := sha256.Sum256([]byte(s.Token))
	return hex.EncodeToString(h[:])
}
func terminalScope(r *http.Request) string {
	return chi.URLParam(r, "id") + "\x00" + chi.URLParam(r, "containerId")
}
func terminalHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, msg := http.StatusInternalServerError, "terminal_failed", humanTerminalError(terminalLocale(r), err)
	switch {
	case errors.Is(err, terminalhttp.ErrNotFound):
		status, code, msg = 404, "terminal_not_found", "终端会话不存在或已过期"
	case errors.Is(err, terminalhttp.ErrLimit):
		status, code, msg = 429, "terminal_limit", "终端会话数量已达上限"
	case errors.Is(err, terminalhttp.ErrConflict), errors.Is(err, terminalhttp.ErrCursor):
		status, code, msg = 409, "terminal_conflict", "终端请求或输出游标冲突"
	case errors.Is(err, terminalhttp.ErrInput):
		status, code, msg = 400, "terminal_input", "终端输入无效"
	case errors.Is(err, terminalhttp.ErrEnded):
		status, code, msg = 410, "terminal_ended", "终端会话已结束"
	case errors.Is(err, terminalhttp.ErrClosed):
		status, code, msg = 410, "terminal_closed", "终端会话已结束"
	}
	writeError(w, status, code, msg)
}
func terminalJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func makeTerminalHTTPHandler(m *terminalhttp.Manager, svc target.Service, aud audit.Recorder, authSvc auth.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				writeError(w, 403, "origin_invalid", "请求来源无效")
				return
			}
		}
		if m == nil || svc == nil {
			writeError(w, 503, "terminal_unavailable", "终端兼容通道未初始化")
			return
		}
		owner, scope := terminalOwner(r), terminalScope(r)
		sid := chi.URLParam(r, "sid")
		if sid == "" {
			var body struct {
				RequestID string `json:"requestId"`
				Shell     string `json:"shell"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
				terminalHTTPError(w, r, terminalhttp.ErrInput)
				return
			}
			id, cid := chi.URLParam(r, "id"), chi.URLParam(r, "containerId")
			var shell string
			var err error
			if cid == "" {
				shell, err = validateHostShell(body.Shell)
			} else {
				shell, err = validateContainerTarget(cid, body.Shell)
			}
			if err != nil || strings.Contains(body.RequestID, "\x00") {
				terminalHTTPError(w, r, terminalhttp.ErrInput)
				return
			}
			if _, err = svc.Get(r.Context(), id); err != nil {
				writeServerError(w, err)
				return
			}
			cmd := []string{shell}
			action := audit.ActionServerTerminal
			detail := map[string]any{"shell": shell, "target": "host"}
			if cid != "" {
				cmd = buildContainerExecCmd(cid, shell)
				action = audit.ActionContainerTerminal
				detail = map[string]any{"shell": shell, "containerId": cid}
			}
			sid, err = m.Create(r.Context(), owner, scope, body.RequestID, shell, func(ctx context.Context) (terminalhttp.Session, error) { return svc.ExecInteractive(ctx, id, cmd) }, func() {
				auditCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
				defer cancel()
				recordAudit(auditCtx, aud, audit.Entry{Actor: auditActor, Action: action, TargetType: audit.TargetServer, TargetID: id, Detail: detail, IP: clientIP(r)})
			})
			if err != nil {
				terminalHTTPError(w, r, err)
				return
			}
			terminalJSON(w, map[string]string{"id": sid})
			return
		}
		switch r.Method {
		case http.MethodGet:
			after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			if err != nil || after < 0 {
				terminalHTTPError(w, r, terminalhttp.ErrCursor)
				return
			}
			cookie, _ := r.Cookie(cookieSession)
			var verifyErr error
			valid := func() bool {
				if cookie == nil || authSvc == nil {
					verifyErr = auth.ErrSessionNotFound
					return false
				}
				_, verifyErr = authSvc.Verify(cookie.Value)
				return verifyErr == nil
			}
			out, err := m.Poll(r.Context(), owner, scope, sid, after, valid)
			if verifyErr != nil {
				if !errors.Is(verifyErr, auth.ErrSessionNotFound) {
					writeError(w, 500, "internal", "服务器内部错误")
					return
				}
				_ = m.Delete(owner, scope, sid)
				writeError(w, 401, "unauthorized", "会话已过期,请重新登录")
				return
			}
			if err != nil {
				terminalHTTPError(w, r, err)
				return
			}
			terminalJSON(w, map[string]any{"data": base64.StdEncoding.EncodeToString(out.Data), "next": out.Next, "closed": out.Closed, "reason": out.Reason})
		case http.MethodPost:
			var body struct {
				Seq  int64  `json:"seq"`
				Data string `json:"data"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<10)).Decode(&body); err != nil {
				terminalHTTPError(w, r, terminalhttp.ErrInput)
				return
			}
			data, err := base64.StdEncoding.DecodeString(body.Data)
			if err != nil {
				terminalHTTPError(w, r, terminalhttp.ErrInput)
				return
			}
			if err = m.Write(r.Context(), owner, scope, sid, terminalhttp.Input{Seq: body.Seq, Data: data, Cols: body.Cols, Rows: body.Rows}); err != nil {
				terminalHTTPError(w, r, err)
				return
			}
			w.WriteHeader(204)
		case http.MethodDelete:
			err := m.Delete(owner, scope, sid)
			if err != nil {
				terminalHTTPError(w, r, err)
				return
			}
			w.WriteHeader(204)
		}
	}
}
