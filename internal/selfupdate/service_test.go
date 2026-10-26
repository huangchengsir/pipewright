package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/version"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func archive(t *testing.T, duplicate bool) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	n := 1
	if duplicate {
		n = 2
	}
	for i := 0; i < n; i++ {
		body := []byte("verified-new-binary-contents")
		if e := tw.WriteHeader(&tar.Header{Name: "pipewright", Typeflag: tar.TypeReg, Size: int64(len(body)), Mode: 0755}); e != nil {
			t.Fatal(e)
		}
		tw.Write(body)
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

type harness struct {
	s        *Service
	db       *sql.DB
	opts     Options
	installs atomic.Int32
	server   *httptest.Server
	pkg      []byte
}

func fixture(t *testing.T, handler func(*harness, http.ResponseWriter, *http.Request)) *harness {
	t.Helper()
	dir := t.TempDir()
	st := storetest.Open(t)
	var e error
	h := &harness{db: st.DB, pkg: archive(t, false)}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler != nil {
			handler(h, w, r)
			return
		}
		h.serve(w, r)
	}))
	exe := filepath.Join(dir, "current")
	os.WriteFile(exe, []byte("old"), 0700)
	h.opts = Options{Repository: "huangchengsir/pipewright", Executable: exe, Directory: filepath.Join(dir, "work"), Platform: "linux_amd64", BaseURL: h.server.URL, Client: h.server.Client(), Current: func() version.Info { return version.Info{Version: "1.0.0", Platform: "linux/amd64"} }, Install: func(a, b string) error { h.installs.Add(1); return os.Rename(a, b) }, RequestRestart: func() error { return nil }}
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	h.s.Start(context.Background())
	t.Cleanup(func() { h.s.Close(); h.server.Close(); st.Close() })
	return h
}
func (h *harness) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "checksums.txt") {
		sum := sha256.Sum256(h.pkg)
		fmt.Fprintf(w, "%x  pipewright_2.0.0_linux_amd64.tar.gz\n", sum)
		return
	}
	w.Header().Set("ETag", `"fixture-v1"`)
	w.Write(h.pkg)
}
func wait(t *testing.T, s *Service, id string, states ...string) *Task {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		v, e := s.Get(context.Background(), id)
		if e == nil {
			for _, state := range states {
				if v.State == state {
					return v
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, e := s.Get(context.Background(), id)
	t.Fatalf("state did not converge: %+v %v", v, e)
	return nil
}
func submit(t *testing.T, h *harness) *Task {
	t.Helper()
	v, e := h.s.Submit(context.Background(), Request{RequestID: "test-request", Target: "2.0.0"})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestDurableRequestIdentityAndReconcile(t *testing.T) {
	h := fixture(t, nil)
	v := submit(t, h)
	same, e := h.s.Submit(context.Background(), Request{RequestID: "test-request", Target: "2.0.0"})
	if e != nil || same.ID != v.ID {
		t.Fatal(same, e)
	}
	if _, e = h.s.Submit(context.Background(), Request{RequestID: "test-request", Target: "3.0.0"}); e != ErrConflict {
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
	got := wait(t, h.s, v.ID, "completed")
	if h.installs.Load() != 1 || got.SHA256 == "" || got.BinarySHA256 == "" {
		t.Fatal(got, h.installs.Load())
	}
	var fence string
	h.db.QueryRow("SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence)
	if fence != "" {
		t.Fatal(fence)
	}
}
func TestDifferentVersionDoesNotConfirmOrReinstall(t *testing.T) {
	h := fixture(t, nil)
	v := submit(t, h)
	wait(t, h.s, v.ID, "restarting")
	h.s.Close()
	h.opts.Current = func() version.Info { return version.Info{Version: "3.0.0", Platform: "linux/amd64"} }
	var e error
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	h.s.Start(context.Background())
	wait(t, h.s, v.ID, "unknown")
	if _, e = h.s.Resume(context.Background(), v.ID, "resume"); e != ErrConflict {
		t.Fatal(e)
	}
	if h.installs.Load() != 1 {
		t.Fatal(h.installs.Load())
	}
	if _, e = maintenance.Enter(context.Background(), h.db, "new_work"); e == nil {
		t.Fatal("unknown installation must retain fence")
	}
}
func TestWrongInstalledDigestNotConfirmed(t *testing.T) {
	h := fixture(t, nil)
	v := submit(t, h)
	wait(t, h.s, v.ID, "restarting")
	h.s.Close()
	os.WriteFile(h.opts.Executable, []byte("different"), 0700)
	h.opts.Current = func() version.Info { return version.Info{Version: "2.0.0", Platform: "linux/amd64"} }
	var e error
	h.s, e = New(h.db, h.opts)
	if e != nil {
		t.Fatal(e)
	}
	h.s.Start(context.Background())
	wait(t, h.s, v.ID, "unknown")
}
func TestActiveLeaseBlocksUntilExplicitContinue(t *testing.T) {
	h := fixture(t, nil)
	release, e := maintenance.Enter(context.Background(), h.db, "terminal_pty")
	if e != nil {
		t.Fatal(e)
	}
	v := submit(t, h)
	wait(t, h.s, v.ID, "ready_blocked")
	if h.installs.Load() != 0 {
		t.Fatal("installed during active work")
	}
	release()
	time.Sleep(50 * time.Millisecond)
	if h.installs.Load() != 0 {
		t.Fatal("automatically continued")
	}
	if _, e = h.s.Resume(context.Background(), v.ID, "resume-one"); e != nil {
		t.Fatal(e)
	}
	wait(t, h.s, v.ID, "restarting")
	if h.installs.Load() != 1 {
		t.Fatal("not installed once")
	}
}
func TestBrowserCancellationDoesNotCancelTask(t *testing.T) {
	h := fixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	v, e := h.s.Submit(ctx, Request{RequestID: "cancel-browser", Target: "2.0.0"})
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	wait(t, h.s, v.ID, "restarting")
	if h.installs.Load() != 1 {
		t.Fatal(h.installs.Load())
	}
}
func TestInterruptedTransferExplicitRangeResume(t *testing.T) {
	var first atomic.Bool
	first.Store(true)
	var rangeSeen atomic.Bool
	h := fixture(t, func(h *harness, w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			h.serve(w, r)
			return
		}
		w.Header().Set("ETag", `"fixture-v1"`)
		if first.Swap(false) {
			w.Header().Set("Content-Length", strconv.Itoa(len(h.pkg)))
			w.Write(h.pkg[:len(h.pkg)/2])
			return
		}
		off, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-"))
		if e != nil || off <= 0 || r.Header.Get("If-Range") != `"fixture-v1"` {
			t.Error("invalid resume", r.Header)
		} else {
			rangeSeen.Store(true)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, len(h.pkg)-1, len(h.pkg)))
		w.Header().Set("Content-Length", strconv.Itoa(len(h.pkg)-off))
		w.WriteHeader(206)
		w.Write(h.pkg[off:])
	})
	v := submit(t, h)
	wait(t, h.s, v.ID, "interrupted")
	if h.installs.Load() != 0 {
		t.Fatal("partial installed")
	}
	if _, e := h.s.Resume(context.Background(), v.ID, "resume-download"); e != nil {
		t.Fatal(e)
	}
	wait(t, h.s, v.ID, "restarting")
	if !rangeSeen.Load() || h.installs.Load() != 1 {
		t.Fatal("resume not verified")
	}
}
func TestRejectDuplicateBinaryAndCorruptGzip(t *testing.T) {
	for _, kind := range []string{"duplicate", "trailer", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			h := fixture(t, nil)
			if kind == "duplicate" {
				h.pkg = archive(t, true)
			} else if kind == "trailer" {
				h.pkg[len(h.pkg)-1] ^= 1
			} else {
				h.pkg = []byte("not a gzip")
			}
			v := submit(t, h)
			wait(t, h.s, v.ID, "failed")
			if h.installs.Load() != 0 {
				t.Fatal("bad archive installed")
			}
			b, _ := os.ReadFile(h.opts.Executable)
			if string(b) != "old" {
				t.Fatal("old binary changed")
			}
		})
	}
}
func TestTaskJSONDoesNotExposePrivateMetadata(t *testing.T) {
	b, _ := json.Marshal(Task{ETag: "private-etag", LastResumeID: "private-resume"})
	if strings.Contains(string(b), "private") {
		t.Fatal(string(b))
	}
}
func TestRangeValidation(t *testing.T) {
	for _, value := range []string{"", "bytes 1-0/2", "bytes 0-2/2", "bytes 0-1/*", "items 0-1/2"} {
		if _, _, _, e := parseRange(value); e == nil {
			t.Fatal(value)
		}
	}
	if a, b, n, e := parseRange("bytes 2-9/10"); e != nil || a != 2 || b != 9 || n != 10 {
		t.Fatal(a, b, n, e)
	}
}
func TestEngineProcessLock(t *testing.T) {
	h := fixture(t, nil)
	if s, e := New(h.db, h.opts); e == nil {
		s.Close()
		t.Fatal("second engine acquired same files")
	}
}
func TestChecksumStrictness(t *testing.T) {
	for _, kind := range []string{"short", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			h := fixture(t, nil)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sum := sha256.Sum256(h.pkg)
				entry := fmt.Sprintf("%x  pipewright_2.0.0_linux_amd64.tar.gz\n", sum)
				if kind == "short" {
					entry = "aaaa pipewright_2.0.0_linux_amd64.tar.gz"
				}
				if kind == "duplicate" {
					entry += entry
				}
				if kind == "missing" {
					entry = ""
				}
				fmt.Fprint(w, entry)
			}))
			defer srv.Close()
			h.s.opts.BaseURL = srv.URL
			if _, e := h.s.checksum(context.Background(), Task{Target: "2.0.0", Asset: "pipewright_2.0.0_linux_amd64.tar.gz"}); e == nil {
				t.Fatal(kind)
			}
		})
	}
}
func TestInstallFailureUnknownNoReplay(t *testing.T) {
	h := fixture(t, nil)
	h.s.opts.Install = func(a, b string) error { h.installs.Add(1); return fmt.Errorf("simulated failure") }
	v := submit(t, h)
	wait(t, h.s, v.ID, "unknown")
	if _, e := h.s.Resume(context.Background(), v.ID, "resume"); e != ErrConflict {
		t.Fatal(e)
	}
	if h.installs.Load() != 1 {
		t.Fatal(h.installs.Load())
	}
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
