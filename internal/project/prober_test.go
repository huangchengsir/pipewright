package project

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestProberUnreachable 验证真实 go-git prober 对不可达地址返回 ErrRepoUnreachable
// (且错误不含 token)。用 RFC2606 保留的不可解析主机,确保不真正触网外部服务。
func TestProberUnreachable(t *testing.T) {
	pr := goGitProber{}
	_, err := pr.Probe(context.Background(), "https://nonexistent.invalid/foo/bar.git", "supersecrettoken")
	if !errors.Is(err, ErrRepoUnreachable) {
		t.Fatalf("err = %v, want ErrRepoUnreachable", err)
	}
	if strings.Contains(err.Error(), "supersecrettoken") {
		t.Fatalf("错误信息泄漏了 token: %v", err)
	}
}

// TestProberEmptyURL 验证空 URL 立即判不可达,不触网。
func TestProberEmptyURL(t *testing.T) {
	pr := goGitProber{}
	if _, err := pr.Probe(context.Background(), "   ", "tok"); !errors.Is(err, ErrRepoUnreachable) {
		t.Fatalf("err = %v, want ErrRepoUnreachable", err)
	}
}

// TestProberSSRFRejectsFileScheme 验证生产路径(默认严格)拒绝 file:// scheme。
func TestProberSSRFRejectsFileScheme(t *testing.T) {
	pr := goGitProber{} // 生产默认:allowInsecureSchemes=false
	if _, err := pr.Probe(context.Background(), "file:///etc/passwd", ""); !errors.Is(err, ErrRepoUnreachable) {
		t.Fatalf("file:// 应被拒为 ErrRepoUnreachable, got %v", err)
	}
}

// TestProberSSRFRejectsNonHTTPScheme 验证拒绝 ssh:// / git:// 等非 http(s) scheme。
func TestProberSSRFRejectsNonHTTPScheme(t *testing.T) {
	pr := goGitProber{}
	for _, u := range []string{"ssh://git@host/repo.git", "git://host/repo.git", "ftp://host/x"} {
		if _, err := pr.Probe(context.Background(), u, "tok"); !errors.Is(err, ErrRepoUnreachable) {
			t.Fatalf("%s 应被拒为 ErrRepoUnreachable, got %v", u, err)
		}
	}
}

// TestProberSSRFRejectsMetadataAndLoopback 验证拒绝云元数据(169.254.169.254 链路本地)与回环。
func TestProberSSRFRejectsMetadataAndLoopback(t *testing.T) {
	pr := goGitProber{}
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"https://127.0.0.1/repo.git",
		"http://[::1]/repo.git",
	} {
		if _, err := pr.Probe(context.Background(), u, "tok"); !errors.Is(err, ErrRepoUnreachable) {
			t.Fatalf("%s 应被拒为 ErrRepoUnreachable, got %v", u, err)
		}
	}
}

// TestProberSSRFAllowsPrivateIP 验证私网 IP(自托管内网 Git)放行 scheme 校验
// (不被 SSRF 拦截;真实连接因无服务而 ErrRepoUnreachable,但不是被 scheme/host 校验拒的)。
func TestProberSSRFAllowsPrivateIP(t *testing.T) {
	// 直接验证校验器:私网 IP 字面量通过 validateRepoURL。
	for _, u := range []string{
		"http://10.0.0.5/repo.git",
		"http://172.16.3.4/repo.git",
		"https://192.168.1.10/repo.git",
	} {
		if err := validateRepoURL(u); err != nil {
			t.Fatalf("私网 %s 应放行,got %v", u, err)
		}
	}
}

// TestProberLocalBareRepo 用本地 file:// 裸仓库覆盖 ls-remote 成功路径 + 默认分支探测。
// go-git 的 file 传输不要求 token,故 token 传空;需宿主有 git 以建测试夹具,无 git 则跳过。
func TestProberLocalBareRepo(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("宿主无 git,无法构造本地裸仓库夹具;真实 ls-remote 成功路径跳过")
	}
	dir := t.TempDir()
	run := func(args ...string) {
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
	// 建工作仓库并提交,设默认分支为 trunk。
	run("init", "-q", "-b", "trunk")
	run("commit", "-q", "--allow-empty", "-m", "init")

	// file:// 本地夹具走 test-only 注入例外(生产路径默认严格拒 file://)。
	pr := goGitProber{allowInsecureSchemes: true}
	branch, err := pr.Probe(context.Background(), "file://"+dir, "")
	if err != nil {
		t.Fatalf("Probe local repo: %v", err)
	}
	if branch != "trunk" {
		t.Fatalf("defaultBranch = %q, want trunk", branch)
	}
}
