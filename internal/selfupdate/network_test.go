package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSecureRedirectUserAgentAndNoDowngrade(t *testing.T) {
	h := fixture(t, nil)
	var reached atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.Write([]byte("should not be reached")) }))
	defer plain.Close()
	var ua string
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ua = r.UserAgent(); http.Redirect(w, r, plain.URL, 302) }))
	defer secure.Close()
	h.s.opts.BaseURL = secure.URL
	h.s.opts.Client = secure.Client()
	_, e := h.s.checksum(context.Background(), Task{Target: "2.0.0", Repository: "owner/repo", Asset: "package"})
	if e == nil || reached.Load() != 0 || !strings.HasPrefix(ua, "pipewright/") {
		t.Fatal(e, reached.Load(), ua)
	}
}
func TestChecksumIdleBudgetAndOldExecutablePreserved(t *testing.T) {
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	h.s.opts.IdleTimeout = 50 * time.Millisecond
	v := submit(t, h)
	wait(t, h.s, v.ID, "interrupted")
	if h.installs.Load() != 0 {
		t.Fatal("stalled transfer installed")
	}
	b, _ := os.ReadFile(h.opts.Executable)
	if string(b) != "old" {
		t.Fatal(string(b))
	}
}
func TestManifestAndArchiveSizeBoundaries(t *testing.T) {
	for _, kind := range []string{"manifest", "archive", "digest", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					if kind == "manifest" {
						w.Write([]byte(strings.Repeat("x", int(MaxManifest+1))))
						return
					}
					if kind == "digest" {
						w.Write([]byte(strings.Repeat("0", 64) + "  pipewright_2.0.0_linux_amd64.tar.gz\n"))
						return
					}
					h.serve(w, r)
					return
				}
				if kind == "archive" {
					w.Header().Set("Content-Length", "268435457")
					return
				}
				h.serve(w, r)
			})
			if kind == "trailing" {
				h.pkg = append(h.pkg, []byte("extra stream bytes")...)
			}
			v := submit(t, h)
			wait(t, h.s, v.ID, "interrupted", "failed")
			if h.installs.Load() != 0 {
				t.Fatal("invalid package installed")
			}
		})
	}
}
func TestBaseExecutableChangedCannotBeOverwritten(t *testing.T) {
	h := fixture(t, nil)
	release, e := h.s.Admit(context.Background(), "test_work")
	if e != nil {
		t.Fatal(e)
	}
	v := submit(t, h)
	wait(t, h.s, v.ID, "ready_blocked")
	if e = os.WriteFile(h.opts.Executable, []byte("external-installation"), 0700); e != nil {
		t.Fatal(e)
	}
	release()
	if _, e = h.s.Resume(context.Background(), v.ID, "continue"); e != nil {
		t.Fatal(e)
	}
	wait(t, h.s, v.ID, "failed")
	if h.installs.Load() != 0 {
		t.Fatal("external installation overwritten")
	}
	b, _ := os.ReadFile(h.opts.Executable)
	if string(b) != "external-installation" {
		t.Fatal(string(b))
	}
}
