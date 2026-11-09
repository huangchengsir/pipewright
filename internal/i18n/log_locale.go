package i18n

import (
	"regexp"
	"strings"
)

// logLocaleName deliberately does not use Normalize: request language tags have
// different fallback rules from process locales. No installed OS locale or CGO
// is required; this also gives native Windows binaries the same environment API.
var logLocaleName = regexp.MustCompile(`^[a-zA-Z]{2,3}(?:[_-][a-zA-Z0-9]{2,8}){0,3}(?:\.[a-zA-Z0-9_-]+)?(?:@[a-zA-Z0-9_-]+)?$`)

// LogLocaleFromEnv resolves the immutable process-message preference. HTTP/UI
// localization is independent. Call once before logging, not per request.
//
// LC_ALL overrides LC_MESSAGES, which overrides LANG. Empty variables are
// ignored. C, POSIX and C.<encoding> disable localization, including LANGUAGE.
// Otherwise LANGUAGE is a colon-separated preference list over the supported
// process languages (Chinese and English); unsupported preferences are skipped.
// Missing, malformed or unsupported settings fall back to English.
func LogLocaleFromEnv(getenv func(string) string) string {
	if getenv == nil {
		return "en"
	}
	base := ""
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			base = value
			break
		}
	}
	if base == "" || cLogLocale(base) || len(base) > 128 || !logLocaleName.MatchString(base) {
		return "en"
	}
	if preferences := strings.TrimSpace(getenv("LANGUAGE")); preferences != "" {
		if len(preferences) > 1024 {
			return "en"
		}
		for _, preference := range strings.Split(preferences, ":") {
			if locale := supportedLogLocale(strings.TrimSpace(preference)); locale != "" {
				return locale
			}
		}
		return "en"
	}
	if locale := supportedLogLocale(base); locale != "" {
		return locale
	}
	return "en"
}

func cLogLocale(locale string) bool {
	upper := strings.ToUpper(locale)
	return upper == "C" || upper == "POSIX" || strings.HasPrefix(upper, "C.")
}

func supportedLogLocale(locale string) string {
	if cLogLocale(locale) {
		return "en"
	}
	if len(locale) > 128 || !logLocaleName.MatchString(locale) {
		return ""
	}
	end := strings.IndexAny(locale, "_-.@")
	if end >= 0 {
		locale = locale[:end]
	}
	switch strings.ToLower(locale) {
	case "zh":
		return "zh-CN"
	case "en":
		return "en"
	default:
		return ""
	}
}
