package buildcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"testing"
)

func TestRestoreRejectsTrailingGzipMembers(t *testing.T) {
	var compressed bytes.Buffer
	first := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(first)
	if err := tw.WriteHeader(&tar.Header{Name: "cache/dep", Size: 1, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := gzip.NewWriter(&compressed)
	if _, err := second.Write(make([]byte, 1024*1024)); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTarGz(context.Background(), bytes.NewReader(compressed.Bytes()), t.TempDir()); err == nil {
		t.Fatal("concatenated member accepted")
	}
}

type cancelPaddingReader struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelPaddingReader) Read(b []byte) (int, error) {
	r.reads++
	clear(b)
	r.cancel()
	return len(b), nil
}

func TestTarPaddingDrainIsCanceledAndBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelPaddingReader{cancel: cancel}
	if err := drainTarPadding(ctx, r); !errors.Is(err, context.Canceled) || r.reads != 1 {
		t.Fatalf("padding ignored cancellation: err=%v reads=%d", err, r.reads)
	}
	if err := drainTarPadding(context.Background(), io.LimitReader(zeroPaddingReader{}, 1024*1024)); err == nil {
		t.Fatal("excessive padding accepted")
	}
	if err := drainTarPadding(context.Background(), bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("nonzero trailing data accepted")
	}
	if err := drainTarPadding(context.Background(), bytes.NewReader(make([]byte, 1024))); err != nil {
		t.Fatal(err)
	}
}

type zeroPaddingReader struct{}

func (zeroPaddingReader) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}
