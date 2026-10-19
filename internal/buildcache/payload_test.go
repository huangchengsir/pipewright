package buildcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveEmptyPayloadPreservesWarmCache(t *testing.T) {
	for _, kind := range []string{"missing", "directories", "zero-length", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			s := mustNew(t)
			ws := t.TempDir()
			paths := []string{".m2/repository"}
			if kind != "missing" {
				if err := os.MkdirAll(filepath.Join(ws, ".m2/repository/empty"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "zero-length" {
				writeTree(t, ws, map[string]string{".m2/repository/marker": ""})
			}
			if kind == "invalid" {
				paths = []string{"", "..", "../escape"}
			}
			if err := s.Save(context.Background(), "cold", paths, ws); !errors.Is(err, ErrNoPayload) || s.Has("cold") {
				t.Fatalf("empty save = %v, archive = %v", err, s.Has("cold"))
			}
			warm := t.TempDir()
			writeTree(t, warm, map[string]string{".m2/repository/dep.jar": "real dependency bytes"})
			if err := s.Save(context.Background(), "warm", []string{".m2/repository"}, warm); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(s.pathFor("warm"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Save(context.Background(), "warm", paths, ws); !errors.Is(err, ErrNoPayload) {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(s.pathFor("warm"))
			if !bytes.Equal(before, after) {
				t.Fatal("empty save replaced the useful cache")
			}
			if hit, err := s.Restore(context.Background(), "warm", nil, t.TempDir()); err != nil || !hit {
				t.Fatalf("warm cache lost: %v %v", hit, err)
			}
		})
	}
}

func TestRestoreLegacyEmptyPayloadIsCold(t *testing.T) {
	for _, kind := range []string{"empty-tar", "directories", "zero-length", "symlink-only"} {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			var hdr *tar.Header
			switch kind {
			case "directories":
				hdr = &tar.Header{Name: ".m2/repository/", Mode: 0o755, Typeflag: tar.TypeDir}
			case "zero-length":
				hdr = &tar.Header{Name: ".m2/repository/empty", Mode: 0o644, Typeflag: tar.TypeReg}
			case "symlink-only":
				hdr = &tar.Header{Name: ".m2", Linkname: t.TempDir(), Typeflag: tar.TypeSymlink}
			}
			if hdr != nil {
				if err := tw.WriteHeader(hdr); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			s := mustNew(t)
			archive := s.pathFor("legacy")
			if err := os.MkdirAll(filepath.Dir(archive), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			// Unrelated preexisting bytes must not turn an empty archive into a hit.
			ws := t.TempDir()
			writeTree(t, ws, map[string]string{"unrelated": "not restored"})
			if hit, err := s.Restore(context.Background(), "legacy", nil, ws); hit || err != nil {
				t.Fatalf("empty archive reported warm: %v %v", hit, err)
			}
		})
	}
}

func TestSaveDoesNotFollowSymlinksForPayload(t *testing.T) {
	out := t.TempDir()
	writeTree(t, out, map[string]string{"repository/dep.jar": "must not be counted"})
	for _, rel := range []string{"link", "link/repository", "cache"} {
		t.Run(rel, func(t *testing.T) {
			ws := t.TempDir()
			if err := os.Symlink(out, filepath.Join(ws, "link")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := os.Mkdir(filepath.Join(ws, "cache"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(out, filepath.Join(ws, "cache/nested")); err != nil {
				t.Fatal(err)
			}
			s := mustNew(t)
			if err := s.Save(context.Background(), "k", []string{rel}, ws); !errors.Is(err, ErrNoPayload) || s.Has("k") {
				t.Fatalf("linked bytes were cached: %v", err)
			}
		})
	}
}

func TestCanceledSaveNeverCommits(t *testing.T) {
	s := mustNew(t)
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"cache/dep": "bytes"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Save(ctx, "k", []string{"cache"}, ws); !errors.Is(err, context.Canceled) || s.Has("k") {
		t.Fatalf("canceled save = %v, archived = %v", err, s.Has("k"))
	}
	if err := s.Save(context.Background(), "k", []string{"cache"}, ws); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.pathFor("k"))
	writeTree(t, ws, map[string]string{"cache/dep": "changed"})
	if err := s.Save(ctx, "k", []string{"cache"}, ws); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(s.pathFor("k"))
	if !bytes.Equal(before, after) {
		t.Fatal("cancellation overwrote warm cache")
	}
	if hit, err := s.Restore(ctx, "k", nil, t.TempDir()); hit || err != nil {
		t.Fatalf("canceled restore = %v %v", hit, err)
	}
}

func TestRestoreTruncatedOrBadChecksumIsCold(t *testing.T) {
	for _, damage := range []string{"truncated", "checksum"} {
		t.Run(damage, func(t *testing.T) {
			s := mustNew(t)
			ws := t.TempDir()
			writeTree(t, ws, map[string]string{"cache/dep": "real bytes"})
			if err := s.Save(context.Background(), "k", []string{"cache"}, ws); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(s.pathFor("k"))
			if err != nil {
				t.Fatal(err)
			}
			if damage == "truncated" {
				data = data[:len(data)-4]
			} else {
				data[len(data)-8] ^= 0xff
			}
			if err := os.WriteFile(s.pathFor("k"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if hit, err := s.Restore(context.Background(), "k", nil, t.TempDir()); hit || err != nil {
				t.Fatalf("corrupt payload reported warm: %v %v", hit, err)
			}
		})
	}
}
