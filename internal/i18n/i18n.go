// Package i18n provides server-side localization of user-facing strings
// (primarily API error messages) for the 8 languages the web UI supports.
//
// Design:
//   - The web client sends the *active UI language* in the `X-Pipewright-Locale`
//     header (the browser's Accept-Language is only a fallback, since the user
//     may have picked a different UI language than their browser default).
//   - Messages are authored in zh-CN (the product's source language) at the call
//     site. `T(locale, msg)` translates that source string to the target locale
//     via a catalog keyed by the exact zh-CN string.
//   - Many error messages are built by concatenation (`"prefix:" + detail`). For
//     those, the catalog registers the static prefix and `T` does a longest-prefix
//     match, translating the prefix and preserving the appended detail.
//
// Anything not in the catalog passes through unchanged (so partial catalogs and
// zh-CN itself are always correct).
package i18n

import "strings"

// Default is the source/fallback language; zh-CN strings are authored inline.
const Default = "zh-CN"

// catalog maps a zh-CN source message → {locale: translation}. Populated
// additively by per-area files via register()/registerPrefix() in their init().
var catalog = map[string]map[string]string{}

// prefixCatalog holds messages built by concatenation ("<prefix>" + detail);
// T does a longest-prefix match and preserves the appended detail.
var prefixCatalog = map[string]map[string]string{}

// register merges exact-match entries into the catalog (call from init()).
func register(m map[string]map[string]string) {
	for k, v := range m {
		catalog[k] = v
	}
}

// registerPrefix merges concatenation-prefix entries (call from init()).
func registerPrefix(m map[string]map[string]string) {
	for k, v := range m {
		prefixCatalog[k] = v
	}
}

// Supported lists every locale the catalog (and the web UI) cover.
var Supported = []string{"zh-CN", "zh-TW", "en", "ja", "ko", "es", "fr", "de"}

func isSupported(code string) bool {
	for _, c := range Supported {
		if c == code {
			return true
		}
	}
	return false
}

// Normalize maps an arbitrary BCP-47-ish tag (from a header) to a supported
// locale code, or "" when nothing matches.
func Normalize(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if isSupported(tag) {
		return tag
	}
	lower := strings.ToLower(tag)
	switch {
	case strings.HasPrefix(lower, "zh"):
		if strings.Contains(lower, "hant") || strings.Contains(lower, "tw") ||
			strings.Contains(lower, "hk") || strings.Contains(lower, "mo") {
			return "zh-TW"
		}
		return "zh-CN"
	case strings.HasPrefix(lower, "en"):
		return "en"
	case strings.HasPrefix(lower, "ja"):
		return "ja"
	case strings.HasPrefix(lower, "ko"):
		return "ko"
	case strings.HasPrefix(lower, "es"):
		return "es"
	case strings.HasPrefix(lower, "fr"):
		return "fr"
	case strings.HasPrefix(lower, "de"):
		return "de"
	}
	return ""
}

// FromHeaders resolves the request locale: the explicit UI-locale header wins,
// then the first matching Accept-Language tag, else the default.
func FromHeaders(localeHeader, acceptLanguage string) string {
	if loc := Normalize(localeHeader); loc != "" {
		return loc
	}
	// Accept-Language: "en-US,en;q=0.9,zh;q=0.8" — try tags left to right.
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := part
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = tag[:i]
		}
		if loc := Normalize(tag); loc != "" {
			return loc
		}
	}
	return Default
}

// T translates a zh-CN source message to the target locale. Falls back to the
// original string when the locale is the default/unknown or no entry exists.
// Supports prefix matching for concatenated messages (e.g. "镜像引用非法:" + detail).
func T(locale, msg string) string {
	if locale == "" || locale == Default || msg == "" {
		return msg
	}
	if m, ok := catalog[msg]; ok {
		if t, ok := m[locale]; ok && t != "" {
			return t
		}
		return msg
	}
	// Longest-prefix match for concatenated messages.
	bestKey := ""
	for key := range prefixCatalog {
		if len(key) > len(bestKey) && strings.HasPrefix(msg, key) {
			bestKey = key
		}
	}
	if bestKey != "" {
		if t, ok := prefixCatalog[bestKey][locale]; ok && t != "" {
			return t + msg[len(bestKey):]
		}
	}
	return msg
}
