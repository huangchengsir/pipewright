package mcp

import (
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/run"
)

func TestVersionReferencesAndHostnameParsing(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		ref   string
		valid bool
	}{
		{"registry.example/ns/app:stable", true}, {"localhost:5000/ns/app:1", true}, {"[::1]:5000/ns/app@" + digest, true},
		{"registry.example/ns/app@" + digest, true}, {"https://user:secret@registry.example/app:1", false},
		{"user:secret@registry.example/app:1", false}, {"registry.example/../app:1", false}, {"registry.example/app:--option", false},
		{"registry.example/app@sha256:abc", false}, {"registry.example/app:1?token=secret", false}, {"registry.example/app:1#secret", false},
		{"registry.example/app:1\n", false}, {"registry.example:65536/app:1", false}, {"registry.example/app:1@" + digest + "@secret", false}, {"sha256:" + strings.Repeat("a", 64), false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			ref, _ := safeImageVersion(run.Artifact{Type: run.ArtifactImage, Reference: tc.ref})
			if (ref != nil) != tc.valid {
				t.Fatal(ref, tc.valid)
			}
		})
	}
	ref, d := safeImageVersion(run.Artifact{Type: run.ArtifactImage, Reference: "registry.example/app:1", Metadata: map[string]any{"digest": digest}})
	if ref == nil || d != nil {
		t.Fatal("local digest reported as manifest")
	}
	_, d = safeImageVersion(run.Artifact{Type: run.ArtifactImage, Reference: "registry.example/app:1", Metadata: map[string]any{"digest": digest, "digestRepository": "registry.example/other", "pushed": true}})
	if d != nil {
		t.Fatal("wrong repository digest reported")
	}
	if ref, _ := safeImageVersion(run.Artifact{Type: run.ArtifactArchive, Reference: "https://user:secret@example/file"}); ref != nil {
		t.Fatal("nonimage URL disclosed")
	}
	for _, authority := range []string{"localhost", "LOCALHOST:80", "127.0.0.1:18090", "[::1]", "[::1]:443", "pipewright.example"} {
		if _, err := ParseHostname(authority); err != nil {
			t.Fatal(authority, err)
		}
	}
	for _, authority := range []string{"", "localhost:", "localhost:0", "localhost:65536", "::1", "[example]", "localhost.", "*.example", "user@localhost", "http://localhost", "localhost/path", "localhost?x", "localhost#x", "localhost,evil", "localhost%2e"} {
		if _, err := ParseHostname(authority); err == nil {
			t.Fatal("malformed authority accepted", authority)
		}
	}
}

func TestSafeTextAndInputBounds(t *testing.T) {
	m := mask.NewMasker()
	m.RegisterSecret("private-secret")
	d := Dependencies{Masker: m}
	if got := d.safeText(strings.Repeat("x", 120) + "private-secret"); strings.Contains(got, "private") || len(got) > 128 {
		t.Fatal("clipped secret", got)
	}
	for _, text := range []string{"https://user:pass@example", "Bearer private-token", "password=secret", "token: secret"} {
		if d.safeText(text) != "[redacted]" {
			t.Fatal("unsafe text", text)
		}
	}
	if strings.Contains(d.safeText("hi\n\x00there\u202etext"), "\u202e") {
		t.Fatal("formatting control survived")
	}
	if len(d.safeText(strings.Repeat("字", 100))) > 128 {
		t.Fatal("unicode bound")
	}
	for _, branch := range []string{"https://user:pass@evil", "-option", "main..evil", "main.lock", "main\n", "main?x", "main\\evil", strings.Repeat("x", 129)} {
		if safeBranch(branch) != "" {
			t.Fatal("branch accepted", branch)
		}
	}
	if safeBranch("release/v1") != "release/v1" {
		t.Fatal("valid branch")
	}
	if validParams(map[string]string{"KEY": strings.Repeat("x", 1025)}) || validParams(map[string]string{"BAD-KEY": "x"}) {
		t.Fatal("unbounded parameters")
	}
}
