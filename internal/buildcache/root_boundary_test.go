package buildcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type cacheTestEntry struct {
	name, body, link string
	kind             byte
}

func boundaryArchive(t *testing.T, entries ...cacheTestEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(0)
		if kind == tar.TypeReg {
			size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Linkname: e.link, Typeflag: kind, Size: size, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func boundaryStoreArchive(t *testing.T, s *Store, key string, data []byte) {
	t.Helper()
	name := s.pathFor(key)
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func boundarySymlink(t *testing.T, old, name string) {
	t.Helper()
	if err := os.Symlink(old, name); err != nil {
		t.Skipf("symlinks unavailable on %s: %v", runtime.GOOS, err)
	}
}
func boundaryUnchanged(t *testing.T, name, want string) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil || string(got) != want {
		t.Fatalf("protected bytes changed: got=%q err=%v", got, err)
	}
}

func TestRestoreRejectsUnsafeArchiveNames(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/drive", "C:relative", "//server/share", "\\server\\share", "cache\\escape", "cache/../escape", "cache//escape", "./escape"} {
		t.Run(strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(name), func(t *testing.T) {
			s := mustNew(t)
			ws := t.TempDir()
			boundaryStoreArchive(t, s, "unsafe", boundaryArchive(t, cacheTestEntry{name: name, body: "bad"}))
			if hit, err := s.Restore(context.Background(), "unsafe", nil, ws); hit || err != nil {
				t.Fatalf("unsafe name must be cold: %v %v", hit, err)
			}
			entries, err := os.ReadDir(ws)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unsafe name created workspace entries: %v %v", entries, err)
			}
		})
	}
}
func TestRestoreRejectsEscapingSymlinkTargets(t *testing.T) {
	for _, link := range []string{"../../escape", "/absolute", "C:/drive", "C:relative", "//server/share", "..\\escape"} {
		t.Run(strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(link), func(t *testing.T) {
			s := mustNew(t)
			ws := t.TempDir()
			boundaryStoreArchive(t, s, "link", boundaryArchive(t, cacheTestEntry{name: "cache/data", body: "payload"}, cacheTestEntry{name: "cache/link", link: link, kind: tar.TypeSymlink}))
			if hit, err := s.Restore(context.Background(), "link", nil, ws); hit || err != nil {
				t.Fatalf("unsafe link must be cold: %v %v", hit, err)
			}
			if _, err := os.Lstat(filepath.Join(ws, "cache/link")); !os.IsNotExist(err) {
				t.Fatalf("unsafe link retained: %v", err)
			}
		})
	}
}
func TestRestorePreexistingOutsideSymlinkIsCold(t *testing.T) {
	for _, kind := range []string{"parent", "file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			out := t.TempDir()
			marker := filepath.Join(out, "marker")
			if err := os.WriteFile(marker, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			s := mustNew(t)
			ws := t.TempDir()
			name := "cache/marker"
			if kind == "parent" || kind == "directory" {
				boundarySymlink(t, out, filepath.Join(ws, "cache"))
			} else {
				if err := os.Mkdir(filepath.Join(ws, "cache"), 0o755); err != nil {
					t.Fatal(err)
				}
				boundarySymlink(t, marker, filepath.Join(ws, "cache/marker"))
			}
			entry := cacheTestEntry{name: name, body: "replacement"}
			if kind == "directory" {
				entry = cacheTestEntry{name: "cache", kind: tar.TypeDir}
			}
			boundaryStoreArchive(t, s, "existing", boundaryArchive(t, entry))
			if hit, err := s.Restore(context.Background(), "existing", nil, ws); hit || err != nil {
				t.Fatalf("outside link must be cold: %v %v", hit, err)
			}
			boundaryUnchanged(t, marker, "outside")
		})
	}
}
func TestRestoreHardLinkDoesNotOverwriteOriginal(t *testing.T) {
	out := t.TempDir()
	marker := filepath.Join(out, "marker")
	if err := os.WriteFile(marker, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.Link(marker, filepath.Join(ws, "entry")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	s := mustNew(t)
	boundaryStoreArchive(t, s, "hardlink", boundaryArchive(t, cacheTestEntry{name: "entry", body: "restored"}))
	if hit, err := s.Restore(context.Background(), "hardlink", nil, ws); !hit || err != nil {
		t.Fatalf("restore hardlink destination: %v %v", hit, err)
	}
	boundaryUnchanged(t, marker, "outside")
	boundaryUnchanged(t, filepath.Join(ws, "entry"), "restored")
}
func TestSaveRestoreLocalSymlinks(t *testing.T) {
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"node_modules/.pnpm/tool/bin.js": "package bytes"})
	if err := os.Chmod(filepath.Join(ws, "node_modules/.pnpm/tool/bin.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "node_modules/.bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	boundarySymlink(t, "../.pnpm/tool/bin.js", filepath.Join(ws, "node_modules/.bin/tool"))
	boundarySymlink(t, ".pnpm/tool", filepath.Join(ws, "node_modules/tool"))
	s := mustNew(t)
	if err := s.Save(context.Background(), "local-links", []string{"node_modules"}, ws); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if hit, err := s.Restore(context.Background(), "local-links", nil, dst); !hit || err != nil {
		t.Fatalf("restore: %v %v", hit, err)
	}
	for _, name := range []string{"node_modules/.bin/tool", "node_modules/tool/bin.js"} {
		boundaryUnchanged(t, filepath.Join(dst, filepath.FromSlash(name)), "package bytes")
	}
	if fi, err := os.Stat(filepath.Join(dst, "node_modules/tool")); err != nil || !fi.IsDir() {
		t.Fatalf("directory link lost: %v %v", fi, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dst, "node_modules/.bin/tool")); err != nil || fi.Mode().Perm() != 0o755 {
			t.Fatalf("mode lost: %v %v", fi, err)
		}
	}
}

func TestSaveDoesNotFollowOutsideStoreShard(t *testing.T) {
	s := mustNew(t)
	out := t.TempDir()
	shard := filepath.Dir(s.pathFor("store-link"))
	boundarySymlink(t, out, shard)
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"cache/value": "safe bytes"})
	if err := s.Save(context.Background(), "store-link", []string{"cache"}, ws); err == nil {
		t.Fatal("outside cache shard accepted")
	}
	files, err := os.ReadDir(out)
	if err != nil || len(files) != 0 {
		t.Fatalf("save escaped store root: %v %v", files, err)
	}
}
func TestRestoreDoesNotFollowOutsideStoreArchive(t *testing.T) {
	s := mustNew(t)
	out := t.TempDir()
	archive := filepath.Join(out, "external.tgz")
	if err := os.WriteFile(archive, boundaryArchive(t, cacheTestEntry{name: "entry", body: "external bytes"}), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := s.pathFor("archive-link")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	boundarySymlink(t, archive, dest)
	ws := t.TempDir()
	if hit, err := s.Restore(context.Background(), "archive-link", nil, ws); hit || err != nil {
		t.Fatalf("outside archive accepted: %v %v", hit, err)
	}
	if s.Has("archive-link") {
		t.Fatal("Has accepted outside archive")
	}
	files, err := os.ReadDir(ws)
	if err != nil || len(files) != 0 {
		t.Fatalf("outside archive consumed: %v %v", files, err)
	}
}
func TestDeriveKeyDoesNotReadOutsideLockfile(t *testing.T) {
	ws := t.TempDir()
	want := DeriveKey("main", "", ws)
	outside := filepath.Join(t.TempDir(), "private-lock")
	if err := os.WriteFile(outside, []byte("outside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	boundarySymlink(t, outside, filepath.Join(ws, "package-lock.json"))
	if got := DeriveKey("main", "", ws); got != want {
		t.Fatalf("outside lockfile influenced cache key: %s != %s", got, want)
	}
}

type cancelArchiveReader struct {
	r         *bytes.Reader
	remaining int
	cancel    context.CancelFunc
}

func (r *cancelArchiveReader) Read(p []byte) (int, error) {
	if r.remaining > 0 && len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.r.Read(p)
	r.remaining -= n
	if r.remaining <= 0 {
		r.cancel()
	}
	return n, err
}
func TestRestoreCanceledWritePreservesExistingFile(t *testing.T) {
	payload := make([]byte, 128*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	data := boundaryArchive(t, cacheTestEntry{name: "entry", body: string(payload)})
	ws := t.TempDir()
	file := filepath.Join(ws, "entry")
	if err := os.WriteFile(file, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelArchiveReader{r: bytes.NewReader(data), remaining: 2048, cancel: cancel}
	if _, err := extractTarGz(ctx, r, ws); err == nil {
		t.Fatal("canceled write succeeded")
	}
	boundaryUnchanged(t, file, "existing")
	files, err := os.ReadDir(ws)
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary file leaked: %v %v", files, err)
	}
}

// A failed entry must not partially replace an existing dependency.
type boundaryBrokenReader struct{ sent bool }

func (r *boundaryBrokenReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, "partial replacement"), nil
}
func TestCacheAtomicWriteFailurePreservesExistingFile(t *testing.T) {
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"entry": "existing"})
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeFileFromTar(context.Background(), root, &boundaryBrokenReader{}, "entry", 0o755); err == nil {
		t.Fatal("short entry succeeded")
	}
	boundaryUnchanged(t, filepath.Join(ws, "entry"), "existing")
	files, err := os.ReadDir(ws)
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary file leaked: %v %v", files, err)
	}
}
func TestCacheAtomicWriteCanceledPreservesExistingFile(t *testing.T) {
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"entry": "existing"})
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelArchiveReader{r: bytes.NewReader(bytes.Repeat([]byte("x"), 128*1024)), remaining: 1024, cancel: cancel}
	if err := writeFileFromTar(ctx, root, r, "entry", 0o755); err == nil {
		t.Fatal("canceled entry succeeded")
	}
	boundaryUnchanged(t, filepath.Join(ws, "entry"), "existing")
	files, err := os.ReadDir(ws)
	if err != nil || len(files) != 1 {
		t.Fatalf("temporary file leaked: %v %v", files, err)
	}
}
func TestRestoreSymlinkThroughExistingAliasIsRejected(t *testing.T) {
	// The lexical target cache/../outside is inside the root. Through cache's
	// existing alias to "." it would actually resolve outside the workspace.
	ws := t.TempDir()
	outside := filepath.Join(filepath.Dir(ws), "outside")
	if err := os.WriteFile(outside, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	boundarySymlink(t, ".", filepath.Join(ws, "cache"))
	s := mustNew(t)
	boundaryStoreArchive(t, s, "alias", boundaryArchive(t, cacheTestEntry{name: "cache/link", link: "../outside", kind: tar.TypeSymlink}, cacheTestEntry{name: "payload", body: "bytes"}))
	if hit, err := s.Restore(context.Background(), "alias", nil, ws); hit || err != nil {
		t.Fatalf("alias escape accepted: %v %v", hit, err)
	}
	boundaryUnchanged(t, outside, "protected")
	if _, err := os.Lstat(filepath.Join(ws, "link")); !os.IsNotExist(err) {
		t.Fatalf("escaping alias link retained: %v", err)
	}
}
func boundaryLinuxSwap(t *testing.T, ws, name, outside string, regular bool) func() {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("atomic symlink swap case requires Linux; covered by native-cache acceptance")
	}
	ready := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, target := range []string{outside, "safe"} {
				temp := filepath.Join(ws, ".swap-temp")
				_ = os.Remove(temp)
				if regular && target == "safe" {
					_ = os.WriteFile(temp, []byte("inside"), 0o600)
				} else {
					_ = os.Symlink(target, temp)
				}
				_ = os.Rename(temp, filepath.Join(ws, name))
				if first {
					close(ready)
					first = false
				}
			}
		}
	}()
	<-ready
	return func() { close(stop); <-done; _ = os.Remove(filepath.Join(ws, ".swap-temp")) }
}
func TestCacheRootRestoreSymlinkSwap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native link/rename acceptance")
	}
	ws := t.TempDir()
	outside := t.TempDir()
	writeTree(t, ws, map[string]string{"safe/marker": "inside"})
	writeTree(t, outside, map[string]string{"marker": "outside"})
	boundarySymlink(t, "safe", filepath.Join(ws, "cache"))
	stop := boundaryLinuxSwap(t, ws, "cache", outside, false)
	defer stop()
	data := boundaryArchive(t, cacheTestEntry{name: "cache/marker", body: "restored"})
	completed := 0
	for range 128 {
		if _, err := extractTarGz(context.Background(), bytes.NewReader(data), ws); err == nil {
			completed++
		}
		boundaryUnchanged(t, filepath.Join(outside, "marker"), "outside")
	}
	t.Logf("128 bounded restore attempts during atomic parent-link swaps; completed=%d", completed)
}
func TestCacheRootSaveSymlinkSwap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native link/rename acceptance")
	}
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(outside, []byte("do-not-cache-outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTree(t, ws, map[string]string{"cache/value": "inside"})
	stop := boundaryLinuxSwap(t, ws, "cache/value", outside, true)
	defer stop()
	for range 128 {
		var buf bytes.Buffer
		if _, err := writeTarGz(context.Background(), &buf, ws, []string{"cache"}); err != nil {
			continue
		}
		gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if hdr.Typeflag == tar.TypeReg {
				body, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(body, []byte("do-not-cache-outside")) {
					t.Fatal("save read through a swapped outside link")
				}
			}
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	boundaryUnchanged(t, outside, "do-not-cache-outside")
	t.Log("128 bounded save attempts during atomic regular-file/outside-link swaps")
}

func TestRestoreExplicitFileAndDirectoryModes(t *testing.T) {
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"cache/nested/tool": "executable dependency"})
	if err := os.Chmod(filepath.Join(ws, "cache/nested/tool"), 0o751); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(ws, "cache/nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(ws, "cache"), 0o701); err != nil {
		t.Fatal(err)
	}
	s := mustNew(t)
	if err := s.Save(context.Background(), "modes", []string{"cache"}, ws); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if hit, err := s.Restore(context.Background(), "modes", nil, dst); !hit || err != nil {
		t.Fatalf("restore modes: %v %v", hit, err)
	}
	boundaryUnchanged(t, filepath.Join(dst, "cache/nested/tool"), "executable dependency")
	if runtime.GOOS != "windows" {
		for name, want := range map[string]os.FileMode{"cache": 0o701, "cache/nested": 0o750, "cache/nested/tool": 0o751} {
			info, err := os.Stat(filepath.Join(dst, filepath.FromSlash(name)))
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("%s mode=%v err=%v want=%v", name, info, err, want)
			}
		}
	}
}
