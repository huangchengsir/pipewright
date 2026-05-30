package ai

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeBareRepo 在临时目录建一个含给定文件的裸仓库(file:// 可克隆),返回其 file:// URL。
// 需宿主有 git;无 git 时调用方应 t.Skip。
func makeBareRepo(t *testing.T, files map[string]string) string {
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

	// 裸克隆为 file:// 远端(go-git CloneContext 可读)。
	bare := t.TempDir()
	bareRepo := filepath.Join(bare, "repo.git")
	runGit(bare, "clone", "-q", "--bare", work, bareRepo)
	return "file://" + bareRepo
}

func TestAnalyzeNodeRepo(t *testing.T) {
	url := makeBareRepo(t, map[string]string{
		"package.json": `{"name":"app","engines":{"node":">=22.0.0"}}`,
	})
	a := newInsecureRepoAnalyzer()
	res := a.Analyze(context.Background(), url, "")
	if !res.Cloned {
		t.Fatalf("expected Cloned=true, degrade reason=%q", res.DegradeReason)
	}
	if res.Language != langNode {
		t.Fatalf("Language = %q, want node", res.Language)
	}
	if res.LanguageVersion != "22" {
		t.Fatalf("LanguageVersion = %q, want 22", res.LanguageVersion)
	}
	if res.BuildTool != "npm" {
		t.Fatalf("BuildTool = %q, want npm", res.BuildTool)
	}
}

func TestAnalyzeNodeRepoWithDockerfile(t *testing.T) {
	url := makeBareRepo(t, map[string]string{
		"package.json": `{"engines":{"node":"20"}}`,
		"Dockerfile":   "FROM node:20\n",
	})
	res := newInsecureRepoAnalyzer().Analyze(context.Background(), url, "")
	if !res.Cloned {
		t.Fatalf("expected Cloned=true")
	}
	if !res.HasDockerfile {
		t.Fatalf("expected HasDockerfile=true")
	}
	if res.ArtifactHint != "image" {
		t.Fatalf("ArtifactHint = %q, want image (Dockerfile present)", res.ArtifactHint)
	}
	if res.LanguageVersion != "20" {
		t.Fatalf("LanguageVersion = %q, want 20", res.LanguageVersion)
	}
}

func TestAnalyzeGoRepo(t *testing.T) {
	url := makeBareRepo(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.24\n",
	})
	res := newInsecureRepoAnalyzer().Analyze(context.Background(), url, "")
	if !res.Cloned {
		t.Fatalf("expected Cloned=true")
	}
	if res.Language != langGo {
		t.Fatalf("Language = %q, want go", res.Language)
	}
	if res.LanguageVersion != "1.24" {
		t.Fatalf("LanguageVersion = %q, want 1.24", res.LanguageVersion)
	}
	if res.BuildTool != "go" {
		t.Fatalf("BuildTool = %q, want go", res.BuildTool)
	}
}

func TestAnalyzeNvmrc(t *testing.T) {
	url := makeBareRepo(t, map[string]string{
		"package.json": `{"name":"app"}`,
		".nvmrc":       "v18.16.0\n",
	})
	res := newInsecureRepoAnalyzer().Analyze(context.Background(), url, "")
	if res.LanguageVersion != "18" {
		t.Fatalf("LanguageVersion = %q, want 18 (from .nvmrc)", res.LanguageVersion)
	}
}

// TestAnalyzeCloneFailDegrade 验证克隆失败(不可达 URL)优雅降级:Cloned=false,不报致命错。
func TestAnalyzeCloneFailDegrade(t *testing.T) {
	res := newInsecureRepoAnalyzer().Analyze(context.Background(), "file:///nonexistent/path/repo.git", "")
	if res.Cloned {
		t.Fatalf("expected Cloned=false on clone failure")
	}
	if res.DegradeReason == "" {
		t.Fatalf("expected non-empty DegradeReason on degrade")
	}
	if res.Signals == nil {
		t.Fatalf("Signals must be non-nil even when degraded")
	}
}

// TestAnalyzeSSRFRejectsFileScheme 验证生产路径(严格)拒绝 file:// → 降级(不克隆本地路径)。
func TestAnalyzeSSRFRejectsFileScheme(t *testing.T) {
	res := NewRepoAnalyzer().Analyze(context.Background(), "file:///etc/passwd", "")
	if res.Cloned {
		t.Fatalf("生产路径应拒绝 file:// 并降级,got Cloned=true")
	}
}

// TestAnalyzeSSRFRejectsLoopback 验证生产路径拒绝回环/元数据地址。
func TestAnalyzeSSRFRejectsLoopback(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/repo.git",
		"http://169.254.169.254/repo.git",
		"http://[::1]/repo.git",
	} {
		if validRepoURL(u) {
			t.Fatalf("%s 应被 SSRF 拒绝", u)
		}
	}
}

func TestAnalyzeEmptyURL(t *testing.T) {
	res := NewRepoAnalyzer().Analyze(context.Background(), "   ", "tok")
	if res.Cloned {
		t.Fatalf("空 URL 应降级")
	}
}

func TestMajorVersion(t *testing.T) {
	cases := map[string]string{
		">=22.0.0": "22",
		"^20":      "20",
		"v18.1":    "18",
		"22":       "22",
		"lts/*":    "",
		"":         "",
	}
	for in, want := range cases {
		if got := majorVersion(in); got != want {
			t.Fatalf("majorVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
