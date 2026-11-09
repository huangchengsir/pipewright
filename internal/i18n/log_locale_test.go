package i18n

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

func TestLogLocaleFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"empty", nil, "en"},
		{"c disables language", map[string]string{"LC_ALL": "C", "LANGUAGE": "zh_CN", "LANG": "zh_CN.UTF-8"}, "en"},
		{"posix disables language", map[string]string{"LC_MESSAGES": "POSIX", "LANGUAGE": "zh_CN", "LANG": "zh_CN.UTF-8"}, "en"},
		{"c utf8 disables language", map[string]string{"LC_ALL": "C.UTF-8", "LANGUAGE": "zh_CN"}, "en"},
		{"c alternate encoding", map[string]string{"LC_ALL": "C.utf8", "LANGUAGE": "zh"}, "en"},
		{"c encoding family", map[string]string{"LC_ALL": "C.ISO8859-1", "LANGUAGE": "zh"}, "en"},
		{"language alone is not locale", map[string]string{"LANGUAGE": "zh_CN"}, "en"},
		{"all wins messages", map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "zh_CN.UTF-8", "LANG": "zh_CN.UTF-8"}, "en"},
		{"all wins lang", map[string]string{"LC_ALL": "zh_CN.UTF-8", "LANG": "en_US.UTF-8"}, "zh-CN"},
		{"messages wins lang", map[string]string{"LC_MESSAGES": "zh_CN.UTF-8", "LANG": "en_US.UTF-8"}, "zh-CN"},
		{"empty all ignored", map[string]string{"LC_ALL": "", "LC_MESSAGES": "zh_TW.Big5"}, "zh-CN"},
		{"language overrides non c", map[string]string{"LC_ALL": "en_US.UTF-8", "LANGUAGE": "zh_CN"}, "zh-CN"},
		{"language english first", map[string]string{"LANG": "zh_CN.UTF-8", "LANGUAGE": "en:zh"}, "en"},
		{"language skip unsupported", map[string]string{"LANG": "fr_FR.UTF-8", "LANGUAGE": "xx:zh_CN:en"}, "zh-CN"},
		{"language unknown falls english", map[string]string{"LANG": "zh_CN.UTF-8", "LANGUAGE": "xx:yy"}, "en"},
		{"language empty elements", map[string]string{"LANG": "en_US.UTF-8", "LANGUAGE": " : :zh_CN:en"}, "zh-CN"},
		{"language c barrier", map[string]string{"LANG": "zh_CN.UTF-8", "LANGUAGE": "C:zh"}, "en"},
		{"language blank ignored", map[string]string{"LANG": "zh_CN.UTF-8", "LANGUAGE": " "}, "zh-CN"},
		{"chinese bare", map[string]string{"LANG": "zh"}, "zh-CN"},
		{"chinese modifier", map[string]string{"LANG": "zh_CN.GB18030@stroke"}, "zh-CN"},
		{"chinese bcp47", map[string]string{"LANG": "zh-Hant-TW"}, "zh-CN"},
		{"chinese mixed case", map[string]string{"LANG": "ZH_hk.UTF-8"}, "zh-CN"},
		{"english region", map[string]string{"LANG": "en_GB.UTF-8"}, "en"},
		{"other language", map[string]string{"LANG": "ja_JP.UTF-8"}, "en"},
		{"unknown", map[string]string{"LANG": "unknown"}, "en"},
		{"unknown short", map[string]string{"LANG": "xx_XX"}, "en"},
		{"prefix is not language", map[string]string{"LANG": "zhgarbage"}, "en"},
		{"malformed base rejects preferences", map[string]string{"LANG": "not a locale", "LANGUAGE": "zh_CN"}, "en"},
		{"newline", map[string]string{"LANG": "zh_CN\nforged"}, "en"},
		{"overlong locale", map[string]string{"LANG": strings.Repeat("z", 129)}, "en"},
		{"overlong language", map[string]string{"LANG": "zh_CN", "LANGUAGE": strings.Repeat("zh:", 400)}, "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LogLocaleFromEnv(func(key string) string { return tc.env[key] })
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := LogLocaleFromEnv(nil); got != "en" {
		t.Fatal(got)
	}
}

func TestProcessLogCatalogTemplates(t *testing.T) {
	directives := regexp.MustCompile(`%(?:\[[0-9]+\])?[-+# 0]*(?:[0-9]+|\*)?(?:\.(?:[0-9]+|\*))?(?:\[[0-9]+\])?[A-Za-z%]`)
	prefix := regexp.MustCompile(`^\[[^]]+\]`)
	for english, chinese := range processLogChinese {
		if english == "" || chinese == "" {
			t.Fatal("empty process translation")
		}
		if strings.IndexFunc(english, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
			t.Errorf("English source contains Han: %q", english)
		}
		if !reflect.DeepEqual(directives.FindAllString(english, -1), directives.FindAllString(chinese, -1)) {
			t.Errorf("format signature changed: %q => %q", english, chinese)
		}
		if prefix.FindString(english) != prefix.FindString(chinese) {
			t.Errorf("module prefix changed: %q", english)
		}
		if LogMessage("en", english) != english || LogMessage("unknown", english) != english || LogMessage("zh-CN", english) != chinese {
			t.Errorf("incorrect process fallback: %q", english)
		}
	}
	const unregistered = "future English diagnostic"
	if LogMessage("zh-CN", unregistered) != unregistered {
		t.Fatal("unknown template must retain its English source")
	}
	if FromHeaders("", "") != Default || Default != "zh-CN" {
		t.Fatal("process log selection changed HTTP defaults")
	}
}
