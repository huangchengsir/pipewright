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
	// 防穿越:前导 ../ 被锚到工作区根后中和,绝不写到工作区外(无论解包成功与否)。
	_ = extractTarGz(context.Background(), bytes.NewReader(buf.Bytes()), dest)
	if _, serr := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); !os.IsNotExist(serr) {
		t.Errorf("escape file written outside workspace (err=%v)", serr)
	}
	// 条目应落在工作区内(中和为 dest/escape.txt),内容完整。
	if got, rerr := os.ReadFile(filepath.Join(dest, "escape.txt")); rerr != nil || string(got) != "pwned" {
		t.Errorf("entry not neutralized into workspace: got=%q err=%v", got, rerr)
	}
}

// 显式绝对路径条目(/etc/x)也被锚回工作区内,不写到真实绝对路径。
func TestExtractNeutralizesAbsolutePath(t *testing.T) {
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
	if err := extractTarGz(context.Background(), bytes.NewReader(buf.Bytes()), dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, rerr := os.Stat(filepath.Join(dest, "abs", "file.txt")); rerr != nil {
		t.Errorf("absolute entry not anchored into workspace: %v", rerr)
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
