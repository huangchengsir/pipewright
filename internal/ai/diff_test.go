package ai

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// commitSpec 是一个提交快照:写入/覆盖的文件集(content 为空串表示删除该文件)。
type commitSpec map[string]string

// makeMultiCommitRepo 在临时目录建一个含多个提交的裸仓库(file:// 可克隆),
// 按 specs 顺序逐次 commit,返回 file:// URL 与各 commit 的完整 sha(与 specs 一一对应)。
// 需宿主有 git;无 git 时 t.Skip。
func makeMultiCommitRepo(t *testing.T, specs ...commitSpec) (string, []string) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("宿主无 git,无法构造本地仓库夹具")
	}

	work := t.TempDir()
	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	runGit(work, "init", "-q", "-b", "main")

	shas := make([]string, 0, len(specs))
	for i, spec := range specs {
		for name, content := range spec {
			full := filepath.Join(work, name)
			if content == "" {
				_ = os.Remove(full)
				runGit(work, "rm", "-q", "--ignore-unmatch", name)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			runGit(work, "add", name)
		}
		runGit(work, "commit", "-q", "--allow-empty", "-m", "c"+string(rune('0'+i)))
		shas = append(shas, runGit(work, "rev-parse", "HEAD"))
	}

	bare := t.TempDir()
	bareRepo := filepath.Join(bare, "repo.git")
	runGit(bare, "clone", "-q", "--bare", work, bareRepo)
	return "file://" + bareRepo, shas
}

// findFile 在 files 中按 path 查 FileDiff(便于断言)。
func findFile(files []FileDiff, path string) (FileDiff, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return FileDiff{}, false
}

// TestRunDiffModifiedAddedDeleted 验证 baseline→current 的文件级 diff(改/增/删 + 行数)。
func TestRunDiffModifiedAddedDeleted(t *testing.T) {
	url, shas := makeMultiCommitRepo(t,
		commitSpec{
			"app.txt":     "line1\nline2\nline3\n",
			"removed.txt": "gone\n",
		},
		commitSpec{
			"app.txt":     "line1\nCHANGED\nline3\nline4\n", // modified: +2 / -1
			"new.txt":     "brand new\n",                    // added
			"removed.txt": "",                               // deleted
		},
	)
	d := newInsecureRunDiffer()
	res := d.Diff(context.Background(), url, "", shas[0], shas[1])
	if !res.Available {
		t.Fatalf("expected Available=true, reason=%q", res.Reason)
	}
	if res.Truncated {
		t.Fatalf("unexpected truncation")
	}

	mod, ok := findFile(res.Files, "app.txt")
	if !ok {
		t.Fatalf("app.txt not in diff: %+v", res.Files)
	}
	if mod.Status != diffStatusModified {
		t.Fatalf("app.txt status = %q, want modified", mod.Status)
	}
	if mod.Additions != 2 || mod.Deletions != 1 {
		t.Fatalf("app.txt +%d/-%d, want +2/-1", mod.Additions, mod.Deletions)
	}

	added, ok := findFile(res.Files, "new.txt")
	if !ok || added.Status != diffStatusAdded {
		t.Fatalf("new.txt missing or wrong status: %+v", added)
	}
	if added.Additions != 1 || added.Deletions != 0 {
		t.Fatalf("new.txt +%d/-%d, want +1/-0", added.Additions, added.Deletions)
	}

	deleted, ok := findFile(res.Files, "removed.txt")
	if !ok || deleted.Status != diffStatusDeleted {
		t.Fatalf("removed.txt missing or wrong status: %+v", deleted)
	}
	if deleted.Deletions != 1 || deleted.Additions != 0 {
		t.Fatalf("removed.txt +%d/-%d, want +0/-1", deleted.Additions, deleted.Deletions)
	}

	if res.Summary == "" {
		t.Fatalf("expected non-empty summary")
	}
}

// TestRunDiffDependencyManifestSuspect 验证依赖清单变更被点名为「最可疑」。
func TestRunDiffDependencyManifestSuspect(t *testing.T) {
	url, shas := makeMultiCommitRepo(t,
		commitSpec{"package.json": `{"deps":{"a":"1"}}` + "\n"},
		commitSpec{"package.json": `{"deps":{"a":"2"}}` + "\n"},
	)
	d := newInsecureRunDiffer()
	res := d.Diff(context.Background(), url, "", shas[0], shas[1])
	if !res.Available {
		t.Fatalf("expected Available=true, reason=%q", res.Reason)
	}
	if !strings.Contains(res.Summary, "package.json") {
		t.Fatalf("summary should call out package.json, got %q", res.Summary)
	}
}

// TestRunDiffCloneFailedDegraded 验证不可达仓库 → Available=false 降级(不 panic)。
func TestRunDiffCloneFailedDegraded(t *testing.T) {
	d := newInsecureRunDiffer()
	res := d.Diff(context.Background(), "file:///nonexistent/path/repo.git", "", "abc123", "def456")
	if res.Available {
		t.Fatalf("expected Available=false for unreachable repo")
	}
	if res.Reason == "" {
		t.Fatalf("expected human reason on degrade")
	}
	if len(res.Files) != 0 {
		t.Fatalf("degraded diff should have no files")
	}
}

// TestRunDiffUnreachableCommit 验证 commit 不在历史中 → Available=false 降级。
func TestRunDiffUnreachableCommit(t *testing.T) {
	url, shas := makeMultiCommitRepo(t,
		commitSpec{"a.txt": "1\n"},
		commitSpec{"a.txt": "2\n"},
	)
	d := newInsecureRunDiffer()
	// baseline 用真实 sha,current 用一个不可达 sha。
	res := d.Diff(context.Background(), url, "", shas[0], "0000000000000000000000000000000000000000")
	if res.Available {
		t.Fatalf("expected Available=false for unreachable commit")
	}
}

// TestRunDiffEmptyCommit 验证空 commit 入参 → 降级(不克隆)。
func TestRunDiffEmptyCommit(t *testing.T) {
	d := newInsecureRunDiffer()
	res := d.Diff(context.Background(), "file:///whatever", "", "", "def456")
	if res.Available {
		t.Fatalf("expected Available=false for empty baseline commit")
	}
}

// TestRunDiffSSRFBlocked 验证生产 differ 拒回环地址(SSRF 收口)→ 降级。
func TestRunDiffSSRFBlocked(t *testing.T) {
	d := NewRunDiffer()
	res := d.Diff(context.Background(), "http://127.0.0.1/repo.git", "", "abc", "def")
	if res.Available {
		t.Fatalf("expected Available=false for blocked loopback URL")
	}
}
