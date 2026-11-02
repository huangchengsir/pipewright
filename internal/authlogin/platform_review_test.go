package authlogin

import (
	"context"
	"strings"
	"testing"
)

func TestPlatformReviewAdapterDisplayLabelByteBoundary(t *testing.T) {
	f := coreSetup(t)
	configured := f.save(t)
	f.adapter.identity.DisplayLabel = displayLabel(strings.Repeat("界", 200), "provider")
	t.Logf("adapter displayLabel output: bytes=%d runes=%d", len(f.adapter.identity.DisplayLabel), len([]rune(f.adapter.identity.DisplayLabel)))
	start, err := f.s.Start(context.Background(), ProviderGitHub, "bind", f.session.Token, "/settings/account", configured.Provider.ConfigVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Complete(context.Background(), ProviderGitHub, stateOf(t, start), start.BrowserNonce, f.session.Token, "fixture-code", false); err != nil {
		t.Fatalf("default adapter valid displayLabel output should not invalidate identity: %v", err)
	}
}

func TestPlatformReviewSafeReturnQueryData(t *testing.T) {
	for _, target := range []string{"/runs?q=100%25", "/runs?q=feature%20branch", "/runs/detail?returnTo=%2Fruns%3Fq%3D100%2525", "/runs?q=%E4%B8%AD%E6%96%87#node"} {
		if got, err := SafeReturnTo(target); err != nil || got != target {
			t.Fatalf("context lost %q: %q %v", target, got, err)
		}
	}
}
func TestPlatformReviewCanonicalOriginForms(t *testing.T) {
	for input, want := range map[string]string{"https://127.0.0.1.": "https://127.0.0.1",
		"https://example.com.":         "https://example.com.",
		"https://PUBLIC.example:0443/": "https://public.example", "https://[2001:0db8:0:0:0:0:0:1]:443": "https://[2001:db8::1]", "https://BÜCHER.example": "https://xn--bcher-kva.example", "https://public.example:08443": "https://public.example:8443"} {
		if got, err := PublicOrigin(input, false); err != nil || got != want {
			t.Fatalf("%s -> %s, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"https://public.example:65536", "https://127.1", "https://127.1.", "https://0x7f000001.", "https://0x7f000001", "https://[::ffff:127.0.0.1]"} {
		if _, err := PublicOrigin(input, false); err == nil {
			t.Fatalf("ambiguous/invalid URL accepted %s", input)
		}
	}
}
