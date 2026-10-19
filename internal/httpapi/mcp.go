package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/mask"
	pmcp "github.com/huangchengsir/pipewright/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const MCPMaxRequestBytes = 64 * 1024

type MCPHTTPOptions struct {
	// Additional exact hostnames, supplied by the composition root from
	// PIPEWRIGHT_MCP_ALLOWED_HOSTS. Wildcards, URLs and ports are rejected.
	AllowedHosts []string
}

// NewMCPHandler is Bearer-only and must be mounted outside cookie/CSRF auth,
// at the exact /mcp path. It opens no listener and performs no redirects.
func NewMCPHandler(tokens *auth.MCPTokenStore, d pmcp.Dependencies, opts MCPHTTPOptions) (http.Handler, error) {
	if tokens == nil {
		return nil, errors.New("mcp: token store required")
	}
	if d.Masker == nil {
		d.Masker = mask.NewMasker()
	}
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if local, err := os.Hostname(); err == nil {
		if h, err := pmcp.ParseHostname(local); err == nil {
			allowed[h] = true
		}
	}
	for _, raw := range opts.AllowedHosts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host, err := pmcp.ParseHostname(raw)
		if err != nil || (strings.ToLower(raw) != host && "["+host+"]" != strings.ToLower(raw)) {
			return nil, errors.New("mcp: allowed hosts must be exact hostnames")
		}
		allowed[host] = true
	}
	stream := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		bearer, _ := mcpBearer(r)
		d.Masker.RegisterSecret(bearer)
		return pmcp.NewServer(d, func(ctx context.Context) (*auth.MCPToken, error) { return tokens.Verify(ctx, bearer) }, r.Context())
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: MCPMaxRequestBytes,
		// This wrapper applies the exact configured allowlist on every request,
		// including loopback proxy requests to explicitly allowed external hosts.
		DisableLocalhostProtection: true})
	active := make(chan struct{}, 32)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		host, err := pmcp.ParseHostname(r.Host)
		if err != nil || !allowed[host] || !mcpOriginAllowed(r, allowed) {
			writeError(w, http.StatusForbidden, "mcp_origin_denied", "MCP request source is not allowed")
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		bearer, ok := mcpBearer(r)
		if !ok {
			mcpUnauthorized(w)
			return
		}
		if _, err := tokens.Verify(r.Context(), bearer); err != nil {
			mcpUnauthorized(w)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		select {
		case active <- struct{}{}:
			defer func() { <-active }()
		default:
			writeError(w, http.StatusTooManyRequests, "mcp_busy", "MCP request limit reached")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MCPMaxRequestBytes))
		if err != nil {
			var large *http.MaxBytesError
			if errors.As(err, &large) {
				writeError(w, http.StatusRequestEntityTooLarge, "mcp_request_too_large", "MCP request is too large")
			} else {
				writeError(w, http.StatusBadRequest, "mcp_invalid_request", "Invalid MCP request")
			}
			return
		}
		// Legacy protocol batches can dispatch many concurrent tools in one HTTP slot.
		if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
			writeError(w, http.StatusBadRequest, "mcp_batch_denied", "MCP batch requests are not supported")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		stream.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

func mcpBearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) != 64 || strings.ContainsAny(token, " \t\r\n,") {
		return "", false
	}
	return token, true
}

func mcpUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="pipewright-mcp"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid MCP bearer token")
}

func mcpOriginAllowed(r *http.Request, allowed map[string]bool) bool {
	values, exists := r.Header["Origin"]
	if !exists {
		return true
	}
	if len(values) != 1 || values[0] == "" {
		return false
	}
	u, err := url.Parse(values[0])
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Scheme+"://"+u.Host != values[0] {
		return false
	}
	host, err := pmcp.ParseHostname(u.Host)
	return err == nil && allowed[host]
}

// MCPTokenHandlers are mounted by the parent inside cookie auth and CSRF.
// They also fail closed unless cookie middleware injected an admin session.
type MCPTokenHandlers struct{ List, Create, Revoke http.HandlerFunc }

func NewMCPTokenHandlers(tokens *auth.MCPTokenStore, rec audit.Recorder) MCPTokenHandlers {
	guard := func(next http.HandlerFunc, write bool) http.HandlerFunc {
		var handler http.Handler = next
		if write {
			handler = requireCSRF(handler)
		}
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if sess, ok := sessionFromContext(r.Context()); !ok || sess == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Administrator session required")
				return
			}
			if tokens == nil {
				writeError(w, http.StatusServiceUnavailable, "internal", "MCP token service unavailable")
				return
			}
			handler.ServeHTTP(w, r)
		}
	}
	return MCPTokenHandlers{
		List: guard(func(w http.ResponseWriter, r *http.Request) {
			items, err := tokens.List(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "Unable to list MCP tokens")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"tokens": items})
		}, false),
		Create: guard(func(w http.ResponseWriter, r *http.Request) {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "JSON required")
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Token request too large")
				return
			}
			var in struct {
				Name          string   `json:"name"`
				ProjectIDs    []string `json:"projectIds"`
				CanTrigger    bool     `json:"canTrigger"`
				ExpiresInDays *int     `json:"expiresInDays"`
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
				writeError(w, http.StatusBadRequest, "invalid_json", "Invalid token request")
				return
			}
			days := 7
			if in.ExpiresInDays != nil {
				days = *in.ExpiresInDays
			}
			if days < 1 || days > 30 {
				writeError(w, http.StatusUnprocessableEntity, "invalid_token_input", "TTL must be 1 to 30 days")
				return
			}
			plaintext, item, err := tokens.Create(r.Context(), auth.MCPTokenInput{Name: in.Name, ProjectIDs: in.ProjectIDs, CanTrigger: in.CanTrigger, ExpiresInDays: days})
			if err != nil {
				status := http.StatusInternalServerError
				code := "internal"
				if errors.Is(err, auth.ErrMCPTokenInput) {
					status = http.StatusUnprocessableEntity
					code = "invalid_token_input"
				}
				writeError(w, status, code, "Unable to issue MCP token")
				return
			}
			recordAudit(r.Context(), rec, audit.Entry{Actor: auditActor, Action: "mcp_token.create", TargetType: "mcp_token", TargetID: item.ID, IP: clientIP(r), Detail: map[string]any{"projectCount": len(item.ProjectIDs), "canTrigger": item.CanTrigger}})
			writeJSON(w, http.StatusCreated, map[string]any{"token": plaintext, "item": item})
		}, true),
		Revoke: guard(func(w http.ResponseWriter, r *http.Request) {
			id := chi.URLParam(r, "id")
			if err := tokens.Revoke(r.Context(), id); err != nil {
				status := http.StatusInternalServerError
				if errors.Is(err, auth.ErrMCPTokenNotFound) {
					status = http.StatusNotFound
				}
				writeError(w, status, "mcp_token_not_found", "Unable to revoke MCP token")
				return
			}
			recordAudit(r.Context(), rec, audit.Entry{Actor: auditActor, Action: "mcp_token.revoke", TargetType: "mcp_token", TargetID: id, IP: clientIP(r)})
			w.WriteHeader(http.StatusNoContent)
		}, true),
	}
}
