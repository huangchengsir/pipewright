package repocache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
)

func commitCachedFixture(t *testing.T, repo *gogit.Repository, dir string, files map[string]string, remove []string) string {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range remove {
		if _, err := wt.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := wt.Commit("fixture", &gogit.CommitOptions{Author: &object.Signature{Name: "fixture", Email: "fixture@example.invalid", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

func TestCompareLocalUsesCachedDeploymentEndpointsOnly(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	base := commitCachedFixture(t, repo, dir, map[string]string{"app.txt": "old", "gone.txt": "remove", "private.txt": "DO_NOT_EXPORT_SOURCE_PASSWORD"}, nil)
	current := commitCachedFixture(t, repo, dir, map[string]string{"app.txt": "new", "added.txt": "new"}, []string{"gone.txt"})
	c := newTestCache(t)
	if _, err = c.Clone(context.Background(), dir, "", "", "master", "", filepath.Join(t.TempDir(), "warm")); err != nil {
		t.Fatal(err)
	}
	got := c.CompareLocal(context.Background(), dir, base, current)
	want := []releaseguard.SourceFileChange{{Path: "added.txt", Status: "added"}, {Path: "app.txt", Status: "modified"}, {Path: "gone.txt", Status: "deleted"}}
	if !got.Available || got.BaseSHA != base || got.CurrentSHA != current || got.ComparisonKind != "direct_trees" || !reflect.DeepEqual(got.Files, want) || got.Truncated {
		t.Fatalf("cached direct endpoint diff=%+v", got)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), "DO_NOT_EXPORT_SOURCE_PASSWORD") {
		t.Fatal("source text exported")
	}
	lock := c.repoLock(dir)
	lock.Lock()
	started := time.Now()
	busy := c.CompareLocal(context.Background(), dir, base, current)
	lock.Unlock()
	if busy.Available || busy.Reason != "source_cache_busy" || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("comparison blocked an active build: %+v", busy)
	}
	missing := c.CompareLocal(context.Background(), dir, strings.Repeat("f", 40), current)
	if missing.Available || missing.Reason != "base_commit_not_cached" {
		t.Fatal(missing)
	}
	short := c.CompareLocal(context.Background(), dir, base[:7], current)
	if short.Available || short.Reason != "full_commit_required" {
		t.Fatal(short)
	}
}

func TestCompareLocalNeverCreatesCacheOrCallsFallback(t *testing.T) {
	fallback := &fakeFallback{}
	c, err := New(t.TempDir(), fallback)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://does-not-exist.invalid/private.git"
	got := c.CompareLocal(context.Background(), url, strings.Repeat("a", 40), strings.Repeat("b", 40))
	if got.Available || got.Reason != "source_cache_missing" || fallback.called {
		t.Fatalf("uncached comparison must remain offline: %+v fallback=%v", got, fallback.called)
	}
	if _, err := os.Stat(c.mirrorPath(url)); !os.IsNotExist(err) {
		t.Fatalf("comparison created a mirror: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = c.CompareLocal(ctx, url, strings.Repeat("a", 40), strings.Repeat("b", 40))
	if got.Reason != "source_comparison_cancelled" {
		t.Fatal(got)
	}
}

func TestCompareLocalBoundsFileList(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	base := commitCachedFixture(t, repo, dir, map[string]string{"base.txt": "base"}, nil)
	files := map[string]string{}
	for i := 0; i < 45; i++ {
		files[fmt.Sprintf("changed-%02d.txt", i)] = "never export file contents"
	}
	current := commitCachedFixture(t, repo, dir, files, nil)
	c := newTestCache(t)
	if _, err = c.Clone(context.Background(), dir, "", "", "master", "", filepath.Join(t.TempDir(), "warm")); err != nil {
		t.Fatal(err)
	}
	got := c.CompareLocal(context.Background(), dir, base, current)
	if !got.Available || !got.Truncated || got.Reason != "file_limit" || len(got.Files) != 40 {
		t.Fatalf("file cap not enforced: %+v", got)
	}
}
