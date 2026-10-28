package artifactstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentIdenticalPutRetainsVerifiedBytes(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("shared-matrix-output"), 8192)
	h := sha256.Sum256(payload)
	want := hex.EncodeToString(h[:])
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			key, size, e := s.Put(bytes.NewReader(payload))
			if e != nil || key != want || size != int64(len(payload)) {
				t.Errorf("put key=%s size=%d err=%v", key, size, e)
			}
		}()
	}
	close(start)
	wg.Wait()
	rc, err := s.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("CAS winner bytes corrupted", err)
	}
}
func TestPutRejectsCorruptOrNonRegularWinner(t *testing.T) {
	payload := []byte("verified-byte-proof")
	digest := sha256.Sum256(payload)
	key := hex.EncodeToString(digest[:])
	for _, kind := range []string{"corrupt", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			dest := s.pathFor(key)
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "corrupt":
				err = os.WriteFile(dest, bytes.Repeat([]byte("x"), len(payload)), 0600)
			case "directory":
				err = os.Mkdir(dest, 0700)
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if e := os.WriteFile(outside, payload, 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(outside, dest); e != nil {
					t.Skip("host does not permit test symlink")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Put(bytes.NewReader(payload)); err == nil {
				t.Fatal("unverified existing CAS target accepted")
			}
		})
	}
}
