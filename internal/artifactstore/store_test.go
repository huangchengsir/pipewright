package artifactstore

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestPutOpenRoundTrip(t *testing.T) {
	s := newStore(t)
	payload := []byte("hello artifact bytes\n\x00\x01binary")
	key, size, err := s.Put(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	if !validKey(key) {
		t.Fatalf("key 非法: %q", key)
	}
	rc, err := s.Open(key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("取回字节与存入不一致")
	}
	if st, _ := s.Stat(key); st != int64(len(payload)) {
		t.Fatalf("Stat = %d, want %d", st, len(payload))
	}
	if !s.Has(key) {
		t.Fatal("Has 应为 true")
	}
}

func TestContentAddressedDedup(t *testing.T) {
	s := newStore(t)
	k1, _, _ := s.Put(strings.NewReader("same content"))
	k2, _, _ := s.Put(strings.NewReader("same content"))
	if k1 != k2 {
		t.Fatalf("相同内容应同句柄(去重):%q vs %q", k1, k2)
	}
	k3, _, _ := s.Put(strings.NewReader("different"))
	if k3 == k1 {
		t.Fatal("不同内容不应同句柄")
	}
}

func TestOpenRejectsBadKey(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"", "xyz", "../etc/passwd", strings.Repeat("g", 64), strings.Repeat("A", 64)} {
		if _, err := s.Open(bad); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("Open(%q) err = %v, want ErrInvalidKey", bad, err)
		}
	}
}

func TestOpenMissingIsNotFound(t *testing.T) {
	s := newStore(t)
	missing := strings.Repeat("a", 64) // 合法 hex 但未存
	if _, err := s.Open(missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(missing) err = %v, want ErrNotFound", err)
	}
	if s.Has(missing) {
		t.Fatal("Has(missing) 应 false")
	}
}

func TestNewEmptyRootFails(t *testing.T) {
	if _, err := New("  "); err == nil {
		t.Fatal("空 root 应报错")
	}
}
