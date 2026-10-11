package mask

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStreamNeverEmitsSecretPrefixesAcrossEverySplit(t *testing.T) {
	for _, secret := range []string{"secret-password", "abcabcabc", "中文密钥-test"} {
		for split := 1; split < len(secret); split++ {
			m := NewMasker()
			m.RegisterSecret(secret)
			s := m.NewStream()
			first := s.Write("Hello: " + secret[:split])
			if first != "Hello: " {
				t.Fatalf("split %d leaked %q", split, first)
			}
			out := first + s.Write(secret[split:]+" done") + s.Finish()
			if out != "Hello: "+Placeholder+" done" {
				t.Fatal(out)
			}
		}
	}
}
func TestStreamUTF8TruncationAndOverlappingSecrets(t *testing.T) {
	m := NewMasker()
	m.RegisterSecret("abcdef")
	m.RegisterSecret("defghi")
	s := m.NewStream()
	out := ""
	for _, b := range []byte("你 abcdefghi !") {
		part := s.Write(string([]byte{b}))
		if !utf8.ValidString(part) {
			t.Fatal("split UTF-8")
		}
		out += part
	}
	out += s.Finish()
	if strings.Contains(out, "abc") || strings.Contains(out, "ghi") {
		t.Fatal(out)
	}
	s = m.NewStream()
	if s.Write("safe a") != "safe " || s.Finish() != Placeholder {
		t.Fatal("partial secret leaked at EOF")
	}
}
