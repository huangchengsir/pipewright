package buildcache

import (
	"os"
	"path/filepath"
	"testing"
)

// 同输入恒等同输出(确定性);分支/lockfile/userKey 任一变 → key 变。
func TestDeriveKeyDeterministic(t *testing.T) {
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{"package-lock.json": `{"v":1}`})

	k1 := DeriveKey("main", "", ws)
	k2 := DeriveKey("main", "", ws)
	if k1 != k2 {
		t.Fatalf("non-deterministic: %s != %s", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("key not 64-hex: %q", k1)
	}

	// 分支变 → key 变。
	if DeriveKey("dev", "", ws) == k1 {
		t.Error("different branch should yield different key")
	}
	// userKey 变 → key 变。
	if DeriveKey("main", "custom-v2", ws) == k1 {
		t.Error("different userKey should yield different key")
	}
}

// lockfile 内容变 → key 变(依赖确实变了,旧缓存不应复用);不变 → key 稳定。
func TestDeriveKeyTracksLockfile(t *testing.T) {
	ws := t.TempDir()
	lock := filepath.Join(ws, "package-lock.json")

	_ = os.WriteFile(lock, []byte(`{"deps":"a"}`), 0o644)
	before := DeriveKey("main", "", ws)

	_ = os.WriteFile(lock, []byte(`{"deps":"b"}`), 0o644)
	after := DeriveKey("main", "", ws)

	if before == after {
		t.Error("lockfile content change should change key")
	}
}

// 子目录 lockfile(单体仓库前后端分目录)也计入 key。
func TestDeriveKeyScansSubdirs(t *testing.T) {
	ws := t.TempDir()
	base := DeriveKey("main", "", ws) // 无 lockfile

	writeTree(t, ws, map[string]string{"frontend/package-lock.json": `{}`})
	withFront := DeriveKey("main", "", ws)
	if base == withFront {
		t.Error("frontend/package-lock.json should affect key")
	}
}

// 无 lockfile / 空工作区仍给确定性合法 key(降级为仅分支)。
func TestDeriveKeyNoLockfile(t *testing.T) {
	ws := t.TempDir()
	k := DeriveKey("main", "", ws)
	if len(k) != 64 {
		t.Errorf("key not 64-hex: %q", k)
	}
	if k != DeriveKey("main", "", ws) {
		t.Error("no-lockfile key not deterministic")
	}
}
