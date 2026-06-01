package buildcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeTree 在 base 下按 rel→content 建文件(含父目录)。
func writeTree(t *testing.T, base string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func mustNew(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// Save 后到新工作区 Restore,内容应逐字节一致(round-trip)。
func TestSaveRestoreRoundTrip(t *testing.T) {
	s := mustNew(t)
	ctx := context.Background()

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"node_modules/dep/index.js": "module.exports = 1\n",
		"node_modules/.bin/tool":    "#!/bin/sh\necho hi\n",
		"keep/a.txt":                "alpha",
		"untracked.txt":             "should-not-be-cached",
	})

	const key = "branch=main|lock=abc"
	paths := []string{"node_modules", "keep"}
	if err := s.Save(ctx, key, paths, src); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dst := t.TempDir()
	hit, err := s.Restore(ctx, key, paths, dst)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !hit {
		t.Fatal("Restore: expected cache hit")
	}

	// 缓存的路径应恢复且内容一致。
	for _, rel := range []string{"node_modules/dep/index.js", "node_modules/.bin/tool", "keep/a.txt"} {
		got, rerr := os.ReadFile(filepath.Join(dst, rel))
		if rerr != nil {
			t.Errorf("restored %s missing: %v", rel, rerr)
			continue
		}
		want, _ := os.ReadFile(filepath.Join(src, rel))
		if string(got) != string(want) {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	// 未声明缓存的路径不应被恢复。
	if _, err := os.Stat(filepath.Join(dst, "untracked.txt")); !os.IsNotExist(err) {
		t.Errorf("untracked.txt should not be restored (err=%v)", err)
	}
}

// 缺失 key → 冷恢复:返回 (false, nil),不报错,工作区不被改。
func TestRestoreMiss(t *testing.T) {
	s := mustNew(t)
	dst := t.TempDir()
	hit, err := s.Restore(context.Background(), "no-such-key", []string{"node_modules"}, dst)
	if err != nil {
		t.Fatalf("Restore miss returned error: %v", err)
	}
	if hit {
		t.Fatal("Restore miss should report no hit")
	}
}

// 同 key 再次 Save 覆盖:Restore 拿到最新内容。
func TestSaveOverwrite(t *testing.T) {
	s := mustNew(t)
	ctx := context.Background()
	const key = "k"

	src1 := t.TempDir()
	writeTree(t, src1, map[string]string{"cache/v.txt": "v1"})
	if err := s.Save(ctx, key, []string{"cache"}, src1); err != nil {
		t.Fatalf("Save v1: %v", err)
	}

	src2 := t.TempDir()
	writeTree(t, src2, map[string]string{"cache/v.txt": "v2"})
	if err := s.Save(ctx, key, []string{"cache"}, src2); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	dst := t.TempDir()
	if _, err := s.Restore(ctx, key, []string{"cache"}, dst); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "cache/v.txt"))
	if string(got) != "v2" {
		t.Errorf("after overwrite = %q, want v2", got)
	}
}

// 不存在的缓存路径被跳过,不致命;无任何合法路径 → Save 无操作不报错。
func TestSaveSkipsMissingAndEmpty(t *testing.T) {
	s := mustNew(t)
	ctx := context.Background()
	src := t.TempDir()
	writeTree(t, src, map[string]string{"node_modules/x": "y"})

	// 一条存在、一条不存在 → 只缓存存在的那条,不报错。
	if err := s.Save(ctx, "k1", []string{"node_modules", "does-not-exist"}, src); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !s.Has("k1") {
		t.Fatal("expected k1 archived")
	}

	// 全部为越界/空 → 无合法路径 → 无操作(不写归档,不报错)。
	if err := s.Save(ctx, "k2", []string{"", "..", "/abs", "../escape"}, src); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
	if s.Has("k2") {
		t.Fatal("k2 should not be archived (no valid paths)")
	}
}

// 非法参数(空 key / 空 workspace)→ 返回 error(与 best-effort 的「未命中」区分开)。
func TestInvalidArgs(t *testing.T) {
	s := mustNew(t)
	ctx := context.Background()
	if _, err := s.Restore(ctx, "", nil, "/tmp"); err == nil {
		t.Error("Restore empty key should error")
	}
	if _, err := s.Restore(ctx, "k", nil, ""); err == nil {
		t.Error("Restore empty workspace should error")
	}
	if err := s.Save(ctx, "", []string{"x"}, "/tmp"); err == nil {
		t.Error("Save empty key should error")
	}
	if err := s.Save(ctx, "k", []string{"x"}, ""); err == nil {
		t.Error("Save empty workspace should error")
	}
}

// nil Store 安全:Restore→(false,nil),Save→nil(main 未注入缓存库时调用方零分支)。
func TestNilStoreSafe(t *testing.T) {
	var s *Store
	hit, err := s.Restore(context.Background(), "k", []string{"x"}, "/tmp")
	if hit || err != nil {
		t.Errorf("nil Restore = (%v,%v), want (false,nil)", hit, err)
	}
	if err := s.Save(context.Background(), "k", []string{"x"}, "/tmp"); err != nil {
		t.Errorf("nil Save = %v, want nil", err)
	}
	if s.Has("k") {
		t.Error("nil Has should be false")
	}
}
