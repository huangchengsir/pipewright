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

func TestClonerFullCommitComesFromCheckedOutHEAD(t *testing.T) {
	source := t.TempDir()
	repo, err := gogit.PlainInit(source, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(text string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(source, "README.md"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("README.md"); err != nil {
			t.Fatal(err)
		}
		hash, err := wt.Commit(text, &gogit.CommitOptions{Author: &object.Signature{
			Name: "fixture", Email: "fixture@example.invalid", When: time.Unix(1_700_000_000, 0),
		}})
		if err != nil {
			t.Fatal(err)
		}
		return hash.String()
	}
	first := commit("first")
	latest := commit("latest")
	for _, tc := range []struct {
		name, requested, want, content string
	}{
		{"branch", "", latest, "latest"},
		{"explicit checkout", first, first, "first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), "work")
			resolved, err := (&Cloner{allowInsecure: true}).Clone(context.Background(), source, "", "", "master", tc.requested, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.CommitFull != tc.want || resolved.CommitShort != tc.want[:7] {
				t.Fatalf("resolved=%+v, want actual HEAD=%s", resolved, tc.want)
			}
			content, err := os.ReadFile(filepath.Join(workspace, "README.md"))
			if err != nil || string(content) != tc.content {
				t.Fatalf("checked-out content=%q err=%v", content, err)
			}
		})
	}
}
