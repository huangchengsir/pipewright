package httpapi

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// makeBareSourceRepo 在临时目录建一个含给定文件的裸仓库(file:// 可克隆),返回其 file:// URL。
// 需宿主有 git;无 git 时 t.Skip。
func makeBareSourceRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("宿主无 git,无法构造本地仓库夹具")
	}
	work := t.TempDir()
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(work, "init", "-q", "-b", "main")
	for name, content := range files {
		full := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		runGit(work, "add", name)
	}
	runGit(work, "commit", "-q", "-m", "init")
	bare := t.TempDir()
	bareRepo := filepath.Join(bare, "repo.git")
	runGit(bare, "clone", "-q", "--bare", work, bareRepo)
	return "file://" + bareRepo
}

// seedSourceProject 直接插一个项目(file:// repoURL),绕过 SSRF/probe,返回 project id。
func seedSourceProject(t *testing.T, db *sql.DB, repoURL string) string {
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

// setupSourceServer 构造带 auth + project + 不安全 SourceReader(放行 file://)的测试 server。
func setupSourceServer(t *testing.T, repoURL string) (*httptest.Server, *http.Client, string, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	projID := seedSourceProject(t, st.DB, repoURL)

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithSource(newInsecureSourceReader())))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf, projID
}

func getJSON(t *testing.T, client *http.Client, url, csrf string) (int, map[string]any, string) {
	t.Helper()
	resp := doJSON(t, client, http.MethodGet, url, csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

// TestSourceTreeListsEntries 验证 tree 列 file:// 夹具树(dir 在前,含 size)。
func TestSourceTreeListsEntries(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{
		"go.mod":      "module x\n",
		"src/main.go": "package main\n",
		"README.md":   "# hi\n",
	})
	srv, client, csrf, projID := setupSourceServer(t, url)

	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/source/tree", csrf)
	if code != http.StatusOK {
		t.Fatalf("tree status=%d: %s", code, raw)
	}
	if m["ref"] != "main" {
		t.Fatalf("ref 应默认默认分支 main, got %v", m["ref"])
	}
	entries, _ := m["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("根应 3 项(src/go.mod/README.md), got %d: %s", len(entries), raw)
	}
	// dir 在前。
	first, _ := entries[0].(map[string]any)
	if first["type"] != "dir" || first["name"] != "src" {
		t.Fatalf("首项应为 dir src: %v", first)
	}
	// file 含 size。
	var sawFileSize bool
	for _, e := range entries {
		em, _ := e.(map[string]any)
		if em["type"] == "file" {
			if _, ok := em["size"]; ok {
				sawFileSize = true
			}
		}
	}
	if !sawFileSize {
		t.Fatalf("file 项应含 size: %s", raw)
	}
}

// TestSourceTreeSubdir 验证按 path 列子目录。
func TestSourceTreeSubdir(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"src/main.go": "package main\n"})
	srv, client, csrf, projID := setupSourceServer(t, url)
	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/source/tree?path=src", csrf)
	if code != http.StatusOK {
		t.Fatalf("tree status=%d: %s", code, raw)
	}
	entries, _ := m["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("src/ 应 1 项, got %d", len(entries))
	}
	e0, _ := entries[0].(map[string]any)
	if e0["name"] != "main.go" || e0["path"] != "src/main.go" {
		t.Fatalf("子项 path 应为 src/main.go: %v", e0)
	}
}

// TestSourceBlobReadsFile 验证 blob 读文件内容(非二进制、未截断)。
func TestSourceBlobReadsFile(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"go.mod": "module example\n\ngo 1.22\n"})
	srv, client, csrf, projID := setupSourceServer(t, url)
	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/source/blob?path=go.mod", csrf)
	if code != http.StatusOK {
		t.Fatalf("blob status=%d: %s", code, raw)
	}
	if m["binary"] != false || m["truncated"] != false {
		t.Fatalf("文本文件应 binary=false truncated=false: %s", raw)
	}
	content, _ := m["content"].(string)
	if !strings.Contains(content, "module example") {
		t.Fatalf("content 应含文件内容: %s", raw)
	}
}

// TestSourceBlobBinary 验证含 NUL 字节文件 → binary=true 且不返字节(content 空)。
func TestSourceBlobBinary(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"bin.dat": "ab\x00cd\x00ef"})
	srv, client, csrf, projID := setupSourceServer(t, url)
	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/source/blob?path=bin.dat", csrf)
	if code != http.StatusOK {
		t.Fatalf("blob status=%d: %s", code, raw)
	}
	if m["binary"] != true {
		t.Fatalf("含 NUL 应 binary=true: %s", raw)
	}
	if c, _ := m["content"].(string); c != "" {
		t.Fatalf("binary 不应返字节流, content=%q", c)
	}
}

// TestSourcePathTraversalRejected 验证穿越路径(../、绝对)→ 400 invalid_path。
func TestSourcePathTraversalRejected(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"go.mod": "module x\n"})
	srv, client, csrf, projID := setupSourceServer(t, url)
	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "src/../../x", "a/../../b"} {
		code, m, raw := getJSON(t, client,
			srv.URL+"/api/projects/"+projID+"/source/tree?path="+bad, csrf)
		if code != http.StatusBadRequest {
			t.Fatalf("穿越 %q 应 400, got %d: %s", bad, code, raw)
		}
		errObj, _ := m["error"].(map[string]any)
		if errObj["code"] != "invalid_path" {
			t.Fatalf("穿越 %q 应 invalid_path: %s", bad, raw)
		}
	}
}

// TestSourcePathNotFound 验证不存在路径 → 404 path_not_found。
func TestSourcePathNotFound(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"go.mod": "module x\n"})
	srv, client, csrf, projID := setupSourceServer(t, url)
	code, m, raw := getJSON(t, client,
		srv.URL+"/api/projects/"+projID+"/source/blob?path=nope.txt", csrf)
	if code != http.StatusNotFound {
		t.Fatalf("不存在文件应 404, got %d: %s", code, raw)
	}
	errObj, _ := m["error"].(map[string]any)
	if errObj["code"] != "path_not_found" {
		t.Fatalf("应 path_not_found: %s", raw)
	}
}

// TestSourceProjectNotFound 验证项目不存在 → 404 project_not_found。
func TestSourceProjectNotFound(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"go.mod": "module x\n"})
	srv, client, csrf, _ := setupSourceServer(t, url)
	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/nope/source/tree", csrf)
	if code != http.StatusNotFound {
		t.Fatalf("项目不存在应 404, got %d: %s", code, raw)
	}
	errObj, _ := m["error"].(map[string]any)
	if errObj["code"] != "project_not_found" {
		t.Fatalf("应 project_not_found: %s", raw)
	}
}

// TestSourceCloneFailedDegraded 验证克隆失败 → 200 degraded(空树,绝不 500)。
func TestSourceCloneFailedDegraded(t *testing.T) {
	// 指向不存在的裸仓库路径:克隆必失败。
	srv, client, csrf, projID := setupSourceServer(t, "file:///nonexistent/path/repo.git")
	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/source/tree", csrf)
	if code != http.StatusOK {
		t.Fatalf("克隆失败应 200 degraded(绝不 500), got %d: %s", code, raw)
	}
	if m["degraded"] != true {
		t.Fatalf("克隆失败应 degraded=true: %s", raw)
	}
	entries, _ := m["entries"].([]any)
	if len(entries) != 0 {
		t.Fatalf("degraded 应空 entries, got %d", len(entries))
	}
}

// TestSourceRequiresAuth 验证未认证访问 source 端点 → 401。
func TestSourceRequiresAuth(t *testing.T) {
	url := makeBareSourceRepo(t, map[string]string{"go.mod": "module x\n"})
	srv, _, _, projID := setupSourceServer(t, url)
	anon := newTestClient(t)
	resp, err := anon.Get(srv.URL + "/api/projects/" + projID + "/source/tree")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证 source 应 401, got %d", resp.StatusCode)
	}
}

// TestSourceBlobTruncated 验证超 512KB → truncated=true。
func TestSourceBlobTruncated(t *testing.T) {
	big := strings.Repeat("a", maxBlobBytes+100)
	url := makeBareSourceRepo(t, map[string]string{"big.txt": big})
	srv, client, csrf, projID := setupSourceServer(t, url)
	code, m, raw := getJSON(t, client, srv.URL+"/api/projects/"+projID+"/source/blob?path=big.txt", csrf)
	if code != http.StatusOK {
		t.Fatalf("blob status=%d: %s", code, raw)
	}
	if m["truncated"] != true {
		t.Fatalf("超 512KB 应 truncated=true: %s", raw)
	}
	content, _ := m["content"].(string)
	if len(content) != maxBlobBytes {
		t.Fatalf("截断 content 应为 %d 字节, got %d", maxBlobBytes, len(content))
	}
}
