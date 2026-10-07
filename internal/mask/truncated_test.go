package mask

import (
	"strings"
	"testing"
)

func TestScrubTruncatedSuffixPrefixesAndFullSecrets(t *testing.T) {
	m := NewMasker()
	for _, secret := range []string{"provider-secret-key", "vault-password", "abcdabcd", "aaaaaa", "abcd\u4e2d\u6587secret", "line-one\nline-two"} {
		m.RegisterSecret(secret)
	}
	for _, tc := range []struct{ input, want string }{
		{"key=provider-secret-ke", "key=" + Placeholder},
		{"key=vault-pass", "key=" + Placeholder},
		{"provider-secret-key", Placeholder},
		{"abcdabcd", Placeholder}, {"aaaaaa", Placeholder},
		{"key=abcd\u4e2d", "key=" + Placeholder},
		{"key=line-one\n", "key=" + Placeholder},
		{"key=pro", "key=pro"}, {"key=prov", "key=" + Placeholder},
		{"provider-secret-ke followed by ordinary text", "provider-secret-ke followed by ordinary text"},
		{"provider-secret-key then vault-pass", Placeholder + " then " + Placeholder},
	} {
		if got := m.ScrubTruncated(tc.input); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.input, got, tc.want)
		}
	}
	if got := m.Scrub("key=provider-secret-ke"); got != "key=provider-secret-ke" {
		t.Fatal("global Scrub semantics changed", got)
	}
	overlapping := NewMasker()
	overlapping.RegisterSecret("short")
	overlapping.RegisterSecret("short-long-credential")
	if got := overlapping.ScrubTruncated("key=short-long-credentia"); got != "key="+Placeholder {
		t.Fatal("short secret broke longer truncated prefix", got)
	}
	var absent *Masker
	if absent.ScrubTruncated("text") != "text" || m.ScrubTruncated("") != "" {
		t.Fatal("nil/empty")
	}
	long := NewMasker()
	long.RegisterSecret(strings.Repeat("a", 64<<10) + "b")
	if got := long.ScrubTruncated(strings.Repeat("a", 64<<10)); got != Placeholder {
		t.Fatal("repetitive long prefix not masked", len(got))
	}
}
