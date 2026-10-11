package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type ModelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ModelsResult struct {
	OK    bool          `json:"ok"`
	Items []ModelOption `json:"items"`
	Error string        `json:"error,omitempty"`
}
type ModelLister interface {
	Models(context.Context, TestInput) (*ModelsResult, error)
}

// Preserve custom gateway prefixes; root and versioned bases address the same API.
func endpointBase(provider, base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	suffix := "/v1"
	if provider == ProviderOllama {
		suffix = "/api"
	}
	return strings.TrimSuffix(base, suffix)
}

// DeepSeek's official Anthropic messages API shares its OpenAI model catalog.
// Never strip a custom gateway prefix or broaden the saved-key destination.
func catalogDestination(provider, base string) (string, string, bool) {
	u, err := url.Parse(strings.TrimSpace(base))
	if provider == ProviderClaude && err == nil && u.Scheme == "https" && u.Host == "api.deepseek.com" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && u.RawPath == "" && u.Opaque == "" && !strings.Contains(base, "#") {
		path := strings.TrimRight(u.Path, "/")
		if path == "/anthropic" || path == "/anthropic/v1" {
			return ProviderOpenAI, "https://api.deepseek.com", true
		}
	}
	return provider, base, false
}
func sameDestination(provider, base string, saved *Config) bool {
	if provider != saved.Provider {
		return false
	}
	old := saved.BaseURL
	if old == "" {
		old = defaultBaseURL(provider)
	}
	identity := func(base string) string {
		u, e := url.Parse(apiBase(provider, base))
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		return u.String()
	}
	return identity(base) != "" && identity(base) == identity(old)
}
func (s *service) resolveDraft(ctx context.Context, in TestInput) (provider, base, key string, err error) {
	saved, sealed, err := s.load(ctx)
	if err != nil {
		return "", "", "", err
	}
	provider = saved.Provider
	if in.Provider != nil {
		provider, err = normalizeProvider(*in.Provider)
		if err != nil {
			return "", "", "", err
		}
	}
	if provider == "" {
		return "", "", "", ErrInvalidProvider
	}
	base = saved.BaseURL
	if in.BaseURL != nil {
		base = strings.TrimSpace(*in.BaseURL)
	}
	if base == "" {
		base = defaultBaseURL(provider)
	}
	if base == "" {
		return "", "", "", ErrBaseURLRequired
	}
	if in.APIKey != nil && *in.APIKey != "" {
		key = *in.APIKey
	} else if provider != ProviderOllama && len(sealed) > 0 && sameDestination(provider, base, saved) {
		key, err = s.openSecret(sealed)
	}
	if err != nil {
		return "", "", "", err
	}
	if provider != ProviderOllama && key == "" {
		return "", "", "", ErrAPIKeyRequired
	}
	return
}
func (s *service) Models(ctx context.Context, in TestInput) (*ModelsResult, error) {
	return (&opsModel{service: s}).models(ctx, in)
}

func (m *opsModel) models(ctx context.Context, in TestInput) (*ModelsResult, error) {
	provider, base, key, err := m.service.resolveDraft(ctx, in)
	if err != nil {
		return nil, err
	}
	result := &ModelsResult{Items: []ModelOption{}}
	fail := func(message string) (*ModelsResult, error) {
		result.Items = []ModelOption{}
		result.Error = message
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	catalogProvider, catalogBase, _ := catalogDestination(provider, base)
	client, cleanup, err := m.pinnedClient(ctx, catalogBase)
	if err != nil {
		return fail("Model endpoint is not allowed or DNS resolution failed.")
	}
	defer cleanup()
	endpoint, headers := probeRequest(provider, base, key)
	seen := map[string]bool{}
	cursor := ""
	wire := 0
	for page := 0; page < 4; page++ {
		target := endpoint
		if catalogProvider == ProviderClaude {
			u, e := url.Parse(target)
			if e != nil {
				return fail("Invalid model endpoint.")
			}
			q := u.Query()
			q.Set("limit", "1000")
			if cursor != "" {
				q.Set("after_id", cursor)
			}
			u.RawQuery = q.Encode()
			target = u.String()
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if e != nil {
			return fail("Invalid model endpoint.")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, e := client.Do(req)
		if e != nil {
			return fail(mapTransportError(e))
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
		resp.Body.Close()
		wire += len(raw)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fail(mapStatusError(resp.StatusCode))
		}
		if e != nil || len(raw) > 2<<20 || wire > 4<<20 {
			return fail("Model list exceeds response limit.")
		}
		var body struct {
			Data *[]struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			Models *[]struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"models"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if !utf8.Valid(raw) || json.Unmarshal(raw, &body) != nil {
			return fail("Invalid model list JSON.")
		}
		rows := []ModelOption{}
		if catalogProvider == ProviderOllama {
			if body.Models == nil {
				return fail("Invalid Ollama model list.")
			}
			for _, m := range *body.Models {
				id := m.Model
				if id == "" {
					id = m.Name
				}
				rows = append(rows, ModelOption{id, m.Name})
			}
		} else {
			if body.Data == nil {
				return fail("Invalid compatible model list.")
			}
			for _, m := range *body.Data {
				name := m.DisplayName
				if name == "" {
					name = m.Name
				}
				rows = append(rows, ModelOption{m.ID, name})
			}
		}
		for _, m := range rows {
			if m.ID == "" || len(m.ID) > 256 || len(m.Name) > 512 || strings.IndexFunc(m.ID, unicode.IsControl) >= 0 || strings.IndexFunc(m.Name, unicode.IsControl) >= 0 || (key != "" && (strings.Contains(m.ID, key) || strings.Contains(m.Name, key))) {
				return fail("Invalid model identifier or label.")
			}
			if !seen[m.ID] {
				seen[m.ID] = true
				if len(result.Items) >= 2000 {
					return fail("Model list exceeds item limit.")
				}
				if m.Name == "" {
					m.Name = m.ID
				}
				result.Items = append(result.Items, m)
			}
		}
		if !body.HasMore {
			if len(result.Items) == 0 {
				return fail("No models returned; a model ID can still be entered manually.")
			}
			sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].ID < result.Items[j].ID })
			result.OK = true
			return result, nil
		}
		if catalogProvider != ProviderClaude || body.LastID == "" || body.LastID == cursor || len(body.LastID) > 256 {
			return fail("Unsupported or invalid model pagination.")
		}
		cursor = body.LastID
	}
	return fail("Model list exceeds page limit.")
}
