package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestClonerMetadataFromPinnedCommit(t *testing.T) {
	source := t.TempDir()
	repo, err := gogit.PlainInit(source, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(content, msg, author string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(source, "file"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("file"); err != nil {
			t.Fatal(err)
		}
		h, err := wt.Commit(msg, &gogit.CommitOptions{Author: &object.Signature{Name: author, Email: "private@example.invalid", When: time.Unix(100, 0)}})
		if err != nil {
			t.Fatal(err)
		}
		return h.String()
	}
	first := commit("one", "first title\n\nfirst body\n", "Original Author")
	commit("two", "new title\nnew body", "Later Author")
	r, err := (&Cloner{allowInsecure: true}).Clone(context.Background(), source, "", "", "master", first, filepath.Join(t.TempDir(), "clone"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.MetadataReady || r.CommitFull != first || r.CommitTitle != "first title" || r.CommitAuthor != "Original Author" {
		t.Fatalf("pinned metadata: %+v", r)
	}
}
