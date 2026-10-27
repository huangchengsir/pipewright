package buildcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 恶意归档含 ../ 逃逸条目 → extractTarGz 拒绝(防 Zip Slip),且不在工作区外写文件。
func TestExtractRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("pwned")
	hdr := &tar.Header{Name: "../escape.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()

	dest := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTarGz(context.Background(), bytes.NewReader(buf.Bytes()), dest); err == nil {
		t.Fatal("parent path must be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside path created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("unsafe name was silently rewritten: %v", err)
	}

}

// Absolute archive names are rejected rather than silently rewritten.
func TestExtractRejectsAbsolutePath(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("data")
	_ = tw.WriteHeader(&tar.Header{Name: "/abs/file.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()

	dest := filepath.Join(t.TempDir(), "ws")
	_ = os.MkdirAll(dest, 0o755)
	if _, err := extractTarGz(context.Background(), bytes.NewReader(buf.Bytes()), dest); err == nil {
		t.Fatal("absolute name must be rejected")
	}
	if _, err := os.Stat(filepath.Join(dest, "abs", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("absolute entry was rewritten: %v", err)
	}

}

// Restore 遇损坏归档 → 当未命中处理(false,nil),不让构建失败。
func TestRestoreCorruptArchiveTreatedAsMiss(t *testing.T) {
	s := mustNew(t)
	const key = "k"
	// 直接往 key 的归档路径写一段非 gzip 垃圾。
	dest := s.pathFor(key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("not a gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	hit, err := s.Restore(context.Background(), key, []string{"node_modules"}, t.TempDir())
	if err != nil {
		t.Fatalf("corrupt archive should not error: %v", err)
	}
	if hit {
		t.Fatal("corrupt archive should be reported as miss")
	}
}
