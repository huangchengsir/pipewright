package runretry

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

func TestVerifyStoredFilesFindsCheckpointOnlyReferences(t *testing.T) {
	db := storetest.Open(t)
	f := newFixture(t, db)
	var node string
	for _, ref := range f.nodes {
		node = ref.Key
		break
	}
	hash := strings.Repeat("a", 64)
	cipher, e := f.s.seal("checkpoint", f.root, f.root+":"+node, Outcome{Status: "success", Outputs: Inputs{Files: []FileRef{{Key: hash, SHA256: hash, Size: 7}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec("UPDATE run_retry_nodes SET ciphertext=? WHERE attempt_id=? AND node_key=?", cipher, f.root, node); e != nil {
		t.Fatal(e)
	}
	calls := 0
	if e = f.s.VerifyStoredFiles(context.Background(), func(ctx context.Context, key string, size int64) error {
		calls++
		if key != hash || size != 7 {
			t.Fatal("reference corrupted")
		}
		return nil
	}); e != nil || calls != 1 {
		t.Fatal("checkpoint reference missed", calls, e)
	}
	if e = f.s.VerifyStoredFiles(context.Background(), func(context.Context, string, int64) error { return errors.New("missing") }); !errors.Is(e, ErrUnavailable) {
		t.Fatal("missing retained file accepted", e)
	}
	if _, e = f.db.Exec("UPDATE run_retry_nodes SET ciphertext=? WHERE attempt_id=? AND node_key=?", []byte("invalid"), f.root, node); e != nil {
		t.Fatal(e)
	}
	if e = f.s.VerifyStoredFiles(context.Background(), func(context.Context, string, int64) error { return nil }); !errors.Is(e, ErrUnavailable) {
		t.Fatal("unreadable checkpoint accepted", e)
	}
}
