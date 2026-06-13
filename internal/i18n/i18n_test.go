package i18n

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"en": "en", "en-US": "en", "EN-us": "en",
		"zh-CN": "zh-CN", "zh": "zh-CN", "zh-Hans": "zh-CN",
		"zh-TW": "zh-TW", "zh-Hant": "zh-TW", "zh-HK": "zh-TW",
		"ja": "ja", "ja-JP": "ja", "ko-KR": "ko",
		"es-ES": "es", "fr": "fr", "de-DE": "de",
		"ru": "", "": "", "xx": "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromHeaders(t *testing.T) {
	// Explicit UI-locale header wins over Accept-Language.
	if got := FromHeaders("en", "zh-CN,zh;q=0.9"); got != "en" {
		t.Errorf("header should win: got %q", got)
	}
	// Falls back to first matching Accept-Language tag.
	if got := FromHeaders("", "fr-FR,fr;q=0.9,en;q=0.8"); got != "fr" {
		t.Errorf("accept-language: got %q, want fr", got)
	}
	// Nothing usable → default.
	if got := FromHeaders("", "ru-RU"); got != Default {
		t.Errorf("unknown → default: got %q", got)
	}
}

func TestTranslate(t *testing.T) {
	// Default/empty locale passes through unchanged.
	if got := T("zh-CN", "项目不存在"); got != "项目不存在" {
		t.Errorf("default passthrough: %q", got)
	}
	// Exact match: a catalogued message translates (not passthrough); casing/
	// wording is owned by the catalog, so assert it changed rather than ==literal.
	if got := T("en", "服务器内部错误"); got == "服务器内部错误" || got == "" {
		t.Errorf("exact: expected a translation, got %q", got)
	}
	// Unknown message passes through.
	if got := T("en", "这条没在词条表里"); got != "这条没在词条表里" {
		t.Errorf("unknown passthrough: %q", got)
	}
	// Prefix match preserves the appended detail.
	if got := T("en", "镜像引用非法:foo/bar:@@"); got != "Invalid image reference: foo/bar:@@" {
		t.Errorf("prefix: got %q", got)
	}
}
