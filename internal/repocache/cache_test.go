package repocache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/huangchengsir/pipewright/internal/build"
)

// newTestCache 构造放行本地夹具仓库的缓存(allowInsecure=true,跳过 SSRF 校验)。
func newTestCache(t *testing.T) *Cache {
	t.Helper()
	c, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.allowInsecure = true
	return c
}

// makeSourceRepo 造一个本地源仓库:main 上一个 commit(写 file),再开一条 feature 分支再 commit。
// 返回仓库路径(作 repoURL)+ main HEAD 短 sha。
func makeSourceRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, _ := repo.Worktree()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	sig := &object.Signature{Name: "t", Email: "t@x", When: time.Now()}
	write("README.md", "v1 on main")
	h1, err := wt.Commit("c1", &gogit.CommitOptions{Author: sig})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	// feature 分支 + 一个 commit。
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}); err != nil {
		t.Fatalf("branch: %v", err)
	}
	write("feature.txt", "feature work")
	if _, err := wt.Commit("c2", &gogit.CommitOptions{Author: sig}); err != nil {
		t.Fatalf("commit2: %v", err)
	}
	// 切回 main。
	_ = wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("master")})
	return dir, h1.String()[:7]
}

func TestMirrorCloneCheckoutAndIncremental(t *testing.T) {
	src, mainShort := makeSourceRepo(t)
	c := newTestCache(t)
	ctx := context.Background()

	// 首次 Clone:建镜像 + 从镜像出工作区。
	ws1 := filepath.Join(t.TempDir(), "ws1")
	res, err := c.Clone(ctx, src, "", "master", "", ws1)
	if err != nil {
		t.Fatalf("Clone1: %v", err)
	}
	if res.CommitShort != mainShort {
		t.Fatalf("CommitShort = %q, want %q", res.CommitShort, mainShort)
	}
	if _, err := os.Stat(filepath.Join(ws1, "README.md")); err != nil {
		t.Fatalf("工作区应含 README.md: %v", err)
	}
	// 镜像目录应已建立(下次走增量)。
	if _, err := os.Stat(c.mirrorPath(src)); err != nil {
		t.Fatalf("镜像未建立: %v", err)
	}

	// 在源仓库 main 上加一个新 commit。
	addCommit(t, src, "v2.txt", "v2 content")

	// 第二次 Clone:走**增量 fetch**(镜像已存在),应能拿到新 commit 的内容。
	ws2 := filepath.Join(t.TempDir(), "ws2")
	if _, err := c.Clone(ctx, src, "", "master", "", ws2); err != nil {
		t.Fatalf("Clone2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws2, "v2.txt")); err != nil {
		t.Fatalf("增量 fetch 后工作区应含新增 v2.txt(说明镜像增量更新了): %v", err)
	}
}

func TestCheckoutFeatureBranch(t *testing.T) {
	src, _ := makeSourceRepo(t)
	c := newTestCache(t)
	ws := filepath.Join(t.TempDir(), "ws")
	if _, err := c.Clone(context.Background(), src, "", "feature", "", ws); err != nil {
		t.Fatalf("Clone feature: %v", err)
	}
	// feature 分支独有文件应在。
	if _, err := os.Stat(filepath.Join(ws, "feature.txt")); err != nil {
		t.Fatalf("feature 分支工作区应含 feature.txt: %v", err)
	}
}

func TestListRefs(t *testing.T) {
	src, _ := makeSourceRepo(t)
	c := newTestCache(t)
	refs, err := c.ListRefs(context.Background(), src, "")
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}
	names := map[string]bool{}
	for _, b := range refs.Branches {
		names[b.Name] = true
		if b.Commit == "" {
			t.Fatalf("分支 %s 无 commit", b.Name)
		}
	}
	if !names["master"] || !names["feature"] {
		t.Fatalf("应列出 master 与 feature 分支,实际 %v", names)
	}
}

func TestListCommits(t *testing.T) {
	src, _ := makeSourceRepo(t)
	addCommit(t, src, "a.txt", "a")
	addCommit(t, src, "b.txt", "b")
	c := newTestCache(t)

	commits, err := c.ListCommits(context.Background(), src, "", "master", 10)
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) < 3 {
		t.Fatalf("应至少 3 条提交(c1 + a + b),实际 %d", len(commits))
	}
	if commits[0].Subject != "add b.txt" {
		t.Fatalf("最新提交说明应为 add b.txt,实际 %q", commits[0].Subject)
	}
	if commits[0].Short == "" || len(commits[0].SHA) != 40 {
		t.Fatalf("commit sha 异常: short=%q sha=%q", commits[0].Short, commits[0].SHA)
	}
	one, _ := c.ListCommits(context.Background(), src, "", "master", 1)
	if len(one) != 1 {
		t.Fatalf("limit=1 应只返回 1 条,实际 %d", len(one))
	}
}

// fakeFallback 记录是否被调用。
type fakeFallback struct{ called bool }

func (f *fakeFallback) Clone(_ context.Context, _, _, _, _, destDir string) (*build.CloneResolved, error) {
	f.called = true
	_ = os.MkdirAll(destDir, 0o755)
	return &build.CloneResolved{CommitShort: "fallbck"}, nil
}

func TestFallbackWhenMirrorFails(t *testing.T) {
	c := newTestCache(t)
	fb := &fakeFallback{}
	c.fallback = fb
	// 不存在的仓库地址 → 镜像 clone 失败 → 回退。
	res, err := c.Clone(context.Background(), "/nonexistent/repo/path.git", "", "main", "", filepath.Join(t.TempDir(), "ws"))
	if err != nil {
		t.Fatalf("应回退而非报错: %v", err)
	}
	if !fb.called {
		t.Fatal("镜像失败应回退直连克隆")
	}
	if res.CommitShort != "fallbck" {
		t.Fatalf("应返回回退结果")
	}
}

func TestNewEmptyRootFails(t *testing.T) {
	if _, err := New("  ", nil); err == nil {
		t.Fatal("空 root 应报错")
	}
}

// addCommit 在源仓库 master 上加一个新 commit。
func addCommit(t *testing.T, dir, name, content string) {
	t.Helper()
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	wt, _ := repo.Worktree()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := wt.Commit("add "+name, &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@x", When: time.Now()}}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_ = errors.New("")
}
