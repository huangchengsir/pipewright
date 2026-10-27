package selfupdate

import (
	"context"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/version"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPartFileRejectsLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			h := fixture(t, nil)
			outside := filepath.Join(t.TempDir(), "keep")
			os.WriteFile(outside, []byte("do-not-change"), 0600)
			name := "part"
			target := filepath.Join(h.opts.Directory, name)
			var e error
			if kind == "symlink" {
				e = os.Symlink(outside, target)
			} else {
				e = os.Link(outside, target)
			}
			if e != nil {
				t.Skipf("link privilege unavailable: %v", e)
			}
			f, e := h.s.openPrivate(name)
			if e == nil {
				f.Close()
				t.Fatal("linked part accepted")
			}
			b, _ := os.ReadFile(outside)
			if string(b) != "do-not-change" {
				t.Fatal("outside modified")
			}
		})
	}
}
func TestResumePinnedRepository(t *testing.T) {
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	v := submit(t, h)
	wait(t, h.s, v.ID, "interrupted")
	h.s.opts.Repository = "other/repository"
	if _, e := h.s.Resume(context.Background(), v.ID, "different-source"); e != ErrConflict {
		t.Fatal(e)
	}
	got, _ := h.s.Get(context.Background(), v.ID)
	if got.Repository != "huangchengsir/pipewright" {
		t.Fatal(got.Repository)
	}
}
func TestRequestIDsAreCaseSensitive(t *testing.T) {
	h := fixture(t, nil)
	v, e := h.s.Submit(context.Background(), Request{RequestID: "Case-ID", Target: "2.0.0"})
	if e != nil {
		t.Fatal(e)
	}
	wait(t, h.s, v.ID, "restarting")
	h.s.Close()
	h.opts.Current = func() version.Info { return version.Info{Version: "2.0.0", Platform: "linux/amd64"} }
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	h.s.Start(context.Background())
	newer, e := h.s.Submit(context.Background(), Request{RequestID: "case-id", Target: "3.0.0"})
	if e != nil || newer.ID == v.ID {
		t.Fatal(newer, e)
	}
}
func TestRangeIgnoredRestartsWithoutAppending(t *testing.T) {
	var count atomic.Int32
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			h.serve(w, r)
			return
		}
		w.Header().Set("ETag", `"fixture-v1"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(h.pkg)))
		if count.Add(1) == 1 {
			w.Write(h.pkg[:len(h.pkg)/2])
			return
		}
		w.Write(h.pkg)
	})
	v := submit(t, h)
	wait(t, h.s, v.ID, "interrupted")
	h.s.Resume(context.Background(), v.ID, "continue")
	wait(t, h.s, v.ID, "restarting")
	if h.installs.Load() != 1 {
		t.Fatal("200 response was appended instead of reset")
	}
}
func TestInvalid206CannotInstall(t *testing.T) {
	var count atomic.Int32
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			h.serve(w, r)
			return
		}
		w.Header().Set("ETag", `"fixture-v1"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(h.pkg)))
		if count.Add(1) == 1 {
			w.Write(h.pkg[:len(h.pkg)/2])
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(h.pkg)-1, len(h.pkg)))
		w.WriteHeader(206)
		w.Write(h.pkg)
	})
	v := submit(t, h)
	wait(t, h.s, v.ID, "interrupted")
	h.s.Resume(context.Background(), v.ID, "continue")
	wait(t, h.s, v.ID, "interrupted")
	if h.installs.Load() != 0 {
		t.Fatal("invalid range installed")
	}
}
