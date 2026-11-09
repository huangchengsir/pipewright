package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReleaseExternalTagFreezesVerifiedManifestBytes(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	hash := sha256.Sum256([]byte(body))
	digest := "sha256:" + hex.EncodeToString(hash[:])
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/v2/team/app/manifests/") {
			t.Error("wrong repository path", r.URL.Path)
		}
		w.Header().Set("Docker-Content-Digest", digest)
		w.Write([]byte(body))
	}))
	defer server.Close()
	image, e := resolveReleaseRegistryImage(context.Background(), server.URL+"/team/app:prod", "", "")
	if e != nil || image != strings.TrimPrefix(server.URL, "http://")+"/team/app@"+digest || calls.Load() != 1 {
		t.Fatal("tag not frozen", image, e, calls.Load())
	}
	if _, e = resolveReleaseRegistryImage(context.Background(), server.URL+"/team/app@sha256:"+strings.Repeat("0", 64), "", ""); e == nil {
		t.Fatal("foreign manifest digest accepted")
	}
}
func TestReleaseExternalResolverRejectsRedirectAndDigestLies(t *testing.T) {
	for _, kind := range []string{"redirect", "digest"} {
		t.Run(kind, func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "redirect" {
					http.Redirect(w, r, target.URL, 302)
					return
				}
				w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("0", 64))
				w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`))
			}))
			defer server.Close()
			if _, e := resolveReleaseRegistryImage(context.Background(), server.URL+"/team/app:prod", "", ""); e == nil {
				t.Fatal("unsafe registry response accepted")
			}
			if redirected.Load() != 0 {
				t.Fatal("registry redirect followed")
			}
		})
	}
	for _, bad := range []string{"http://10.0.0.1/team/app:v1", "reg.invalid/team/../app:v1", "https://user:password@reg.invalid/app:v1", "reg.invalid/app?token=private", "reg.invalid/app@sha256:invalid"} {
		if _, _, _, e := releaseRegistryReference(bad); e == nil {
			t.Fatal("bad image input", bad)
		}
	}
}
