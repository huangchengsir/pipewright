package ai

import (
	"net/url"
	"strings"
)

// Only documented official OpenAI-compatible bases omit the usual /v1 segment.
// Custom gateways continue to retain their own prefix and root/v1 normalization.
func directCompatibleBase(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.Opaque != "" || strings.Contains(base, "#") {
		return false
	}
	switch u.Host {
	case "generativelanguage.googleapis.com":
		return u.Path == "/v1beta/openai"
	case "open.bigmodel.cn":
		return u.Path == "/api/paas/v4" || u.Path == "/api/coding/paas/v4"
	}
	return false
}

func apiBase(provider, base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if provider == ProviderOpenAI && directCompatibleBase(base) {
		return base
	}
	base = endpointBase(provider, base)
	switch provider {
	case ProviderOpenAI, ProviderClaude:
		return base + "/v1"
	case ProviderOllama:
		return base + "/api"
	}
	return base
}

func apiEndpoint(provider, base, resource string) string {
	if resource == "models" {
		if _, catalogBase, special := catalogDestination(provider, base); special {
			return catalogBase + "/models"
		}
	}
	return apiBase(provider, base) + "/" + resource
}
