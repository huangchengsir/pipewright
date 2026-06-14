package httpapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// makeTwoCommitRepo 建一个含两次提交的裸仓库(file:// 可克隆),返回 URL + [baselineSha, currentSha]。
// baseline: app.txt 三行;current: 改一行 + 新增 new.txt。需宿主有 git。
func makeTwoCommitRepo(t *testing.T) (string, []string) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("宿主无 git,无法构造本地仓库夹具")
	}
	work := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = work
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v\n%s", args, e, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		full := filepath.Join(work, name)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	runGit("init", "-q", "-b", "main")
	write("app.txt", "a\nb\nc\n")
	runGit("add", "app.txt")
	runGit("commit", "-q", "-m", "baseline")
	baseSha := runGit("rev-parse", "HEAD")

	write("app.txt", "a\nCHANGED\nc\n")
	write("new.txt", "hello\n")
	runGit("add", "app.txt", "new.txt")
	runGit("commit", "-q", "-m", "current")
	curSha := runGit("rev-parse", "HEAD")

	bare := t.TempDir()
	bareRepo := filepath.Join(bare, "repo.git")
	cmd := exec.Command(gitBin, "clone", "-q", "--bare", work, bareRepo)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("clone bare: %v\n%s", e, out)
	}
	return "file://" + bareRepo, []string{baseSha, curSha}
}

// seedDiffProject 直插一个项目(file:// repoURL,绕过 SSRF/probe),返回 project id。
func seedDiffProject(t *testing.T, db *sql.DB, repoURL string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'acme', ?, 'main', ?, ?, ?)`,
		projID, repoURL, credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// seedDiffRun 直插一条运行(全控 branch/commit/status/created_at),返回 run id。
func seedDiffRun(t *testing.T, db *sql.DB, projID, branch, commit, status string, createdAt time.Time) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, 'manual', ?, ?, 'admin', '', '[]', ?, NULL, NULL)`,
		id, projID, status, branch, commit, createdAt.UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return id
}

// setupDiffServer 构造带 auth + project + run + 不安全 RunDiffer(放行 file://)的测试 server。
func setupDiffServer(t *testing.T, repoURL string) (*httptest.Server, *http.Client, string, *sql.DB, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, nil), WithRunDiff(ai.NewInsecureRunDifferForTest())))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	projID := seedDiffProject(t, st.DB, repoURL)
	return srv, client, csrf, st.DB, projID
}

// TestRunDiffFileLevel 验证 baseline成功/current失败 → GET diff 返回文件级差异。
func TestRunDiffFileLevel(t *testing.T) {
	repoURL, shas := makeTwoCommitRepo(t)
	srv, client, csrf, db, projID := setupDiffServer(t, repoURL)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDiffRun(t, db, projID, "main", shas[0], run.StatusSuccess, base.Add(-1*time.Hour))
	curID := seedDiffRun(t, db, projID, "main", shas[1], run.StatusFailed, base)

	code, body, raw := getJSON(t, client, srv.URL+"/api/runs/"+curID+"/diff", csrf)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}
	if body["available"] != true {
		t.Fatalf("available = %v, want true; body=%s", body["available"], raw)
	}
	if body["baselineCommit"] != shas[0] || body["currentCommit"] != shas[1] {
		t.Fatalf("commits mismatch: %s", raw)
	}
	files, _ := body["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files len = %d, want 2; body=%s", len(files), raw)
	}
	// 找 app.txt(modified)与 new.txt(added)。
	seen := map[string]string{}
	for _, fi := range files {
		f, _ := fi.(map[string]any)
		path, _ := f["path"].(string)
		status, _ := f["status"].(string)
		seen[path] = status
	}
	if seen["app.txt"] != "modified" {
		t.Fatalf("app.txt status = %q, want modified; body=%s", seen["app.txt"], raw)
	}
	if seen["new.txt"] != "added" {
		t.Fatalf("new.txt status = %q, want added; body=%s", seen["new.txt"], raw)
	}
	if s, _ := body["summary"].(string); s == "" {
		t.Fatalf("expected non-empty summary; body=%s", raw)
	}
}

// TestRunDiffNoBaseline 验证:即使没有更早的成功 baseline 运行,也展示「该提交自身」diff
// (相对其父提交)——新语义(commit-self / git show)不依赖任何 baseline 运行,成功/失败运行都恒可展示。
func TestRunDiffNoBaseline(t *testing.T) {
	repoURL, shas := makeTwoCommitRepo(t)
	srv, client, csrf, db, projID := setupDiffServer(t, repoURL)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 仅一条失败运行、无任何 baseline 成功运行。
	curID := seedDiffRun(t, db, projID, "main", shas[1], run.StatusFailed, base)

	code, body, raw := getJSON(t, client, srv.URL+"/api/runs/"+curID+"/diff", csrf)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}
	if body["available"] != true {
		t.Fatalf("available = %v, want true(该提交自身 diff,无需 baseline); body=%s", body["available"], raw)
	}
	// baselineCommit 应为本提交的父提交(shas[0]),currentCommit 为本提交(shas[1])。
	if body["baselineCommit"] != shas[0] || body["currentCommit"] != shas[1] {
		t.Fatalf("commits mismatch(want 父 %s → 本 %s): %s", shas[0], shas[1], raw)
	}
	files, _ := body["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files len = %d, want 2; body=%s", len(files), raw)
	}
}

// TestRunDiffNoCommit 验证本次运行无 commit → available:false。
func TestRunDiffNoCommit(t *testing.T) {
	repoURL, shas := makeTwoCommitRepo(t)
	srv, client, csrf, db, projID := setupDiffServer(t, repoURL)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDiffRun(t, db, projID, "main", shas[0], run.StatusSuccess, base.Add(-1*time.Hour))
	curID := seedDiffRun(t, db, projID, "main", "", run.StatusFailed, base) // 无 commit

	code, body, raw := getJSON(t, client, srv.URL+"/api/runs/"+curID+"/diff", csrf)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", code, raw)
	}
	if body["available"] != false {
		t.Fatalf("available = %v, want false; body=%s", body["available"], raw)
	}
}

// TestRunDiffCloneFailedDegraded 验证不可达仓库 → available:false(不 500)。
func TestRunDiffCloneFailedDegraded(t *testing.T) {
	srv, client, csrf, db, projID := setupDiffServer(t, "file:///nonexistent/path/repo.git")

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedDiffRun(t, db, projID, "main", "1111111111111111111111111111111111111111", run.StatusSuccess, base.Add(-1*time.Hour))
	curID := seedDiffRun(t, db, projID, "main", "2222222222222222222222222222222222222222", run.StatusFailed, base)

	code, body, raw := getJSON(t, client, srv.URL+"/api/runs/"+curID+"/diff", csrf)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degraded, not 500); body=%s", code, raw)
	}
	if body["available"] != false {
		t.Fatalf("available = %v, want false; body=%s", body["available"], raw)
	}
}

// TestRunDiffRunNotFound 验证 run 不存在 → 404。
func TestRunDiffRunNotFound(t *testing.T) {
	repoURL, _ := makeTwoCommitRepo(t)
	srv, client, csrf, _, _ := setupDiffServer(t, repoURL)

	code, body, raw := getJSON(t, client, srv.URL+"/api/runs/"+uuid.NewString()+"/diff", csrf)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", code, raw)
	}
	_ = body
}

// TestRunDiffRequiresAuth 验证未认证 → 401。
func TestRunDiffRequiresAuth(t *testing.T) {
	repoURL, _ := makeTwoCommitRepo(t)
	srv, _, _, db, projID := setupDiffServer(t, repoURL)
	curID := seedDiffRun(t, db, projID, "main", "abc", run.StatusFailed, time.Now())

	resp, err := http.Get(srv.URL + "/api/runs/" + curID + "/diff")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
