package vault

import (
	"strings"
	"testing"
)

// TestMaskGitToken 验证 git_token 掩码:保留前缀 + 末 4 位 + 点掩码,无完整明文。
func TestMaskGitToken(t *testing.T) {
	got := mask(TypeGitToken, "ghp_0123456789abcdefa91f")
	if !strings.HasPrefix(got, "ghp_") {
		t.Fatalf("missing prefix: %q", got)
	}
	if !strings.HasSuffix(got, "a91f") {
		t.Fatalf("missing tail: %q", got)
	}
	if !strings.Contains(got, maskDots) {
		t.Fatalf("missing mask dots: %q", got)
	}
	if strings.Contains(got, "0123456789") {
		t.Fatalf("leaks middle of secret: %q", got)
	}
}

// TestMaskSSHKey 验证 ssh_key 掩码:公钥取算法前缀;私钥 PEM 不暴露 BEGIN 头。
func TestMaskSSHKey(t *testing.T) {
	pub := mask(TypeSSHKey, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIQz1k")
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Fatalf("public key missing algo prefix: %q", pub)
	}

	priv := mask(TypeSSHKey, "-----BEGIN OPENSSH PRIVATE KEY-----\nabcdEFGH\n-----END OPENSSH PRIVATE KEY-----")
	if strings.Contains(priv, "BEGIN") || strings.Contains(priv, "-----") || strings.Contains(priv, "----") {
		t.Fatalf("private key mask leaks PEM punctuation: %q", priv)
	}
	if !strings.Contains(priv, maskDots) {
		t.Fatalf("missing mask dots: %q", priv)
	}
}

// TestMaskShortGitTokenFullyDotted 验证短 token 全打点,绝不暴露任何前缀/尾部(=明文)。
func TestMaskShortGitToken(t *testing.T) {
	got := mask(TypeGitToken, "ab_cdef") // 7 字符 < 阈值
	if got != maskDots {
		t.Fatalf("短 token 应全打点, got %q", got)
	}
	// 不得包含明文任何片段。
	for _, frag := range []string{"ab", "cdef", "ab_"} {
		if strings.Contains(got, frag) {
			t.Fatalf("短 token 掩码泄漏明文片段 %q: %q", frag, got)
		}
	}
}

// TestMaskGitTokenNonWhitelistPrefixHidden 验证非白名单前缀不被暴露(只暴露 ghp_/gho_/github_pat_)。
func TestMaskGitTokenNonWhitelistPrefixHidden(t *testing.T) {
	got := mask(TypeGitToken, "custompfx_0123456789abcdef")
	if strings.HasPrefix(got, "custompfx_") {
		t.Fatalf("非白名单前缀不应暴露: %q", got)
	}
	if !strings.HasPrefix(got, maskDots) {
		t.Fatalf("非白名单前缀掩码应以点开头: %q", got)
	}
}

// TestMaskGitHubPATPrefix 验证 github_pat_ 长前缀被识别并暴露。
func TestMaskGitHubPATPrefix(t *testing.T) {
	got := mask(TypeGitToken, "github_pat_0123456789ABCDEFa91f")
	if !strings.HasPrefix(got, "github_pat_") {
		t.Fatalf("github_pat_ 前缀应暴露: %q", got)
	}
}

// TestMaskShortSSHKeyFullyDotted 验证极短 ssh key(密钥体不足阈值)全打点。
func TestMaskShortSSHKey(t *testing.T) {
	got := mask(TypeSSHKey, "ssh-rsa ab12") // 密钥体 ab12 仅 4 字符
	if strings.Contains(got, "ab12") {
		t.Fatalf("短 ssh key 不应暴露尾部: %q", got)
	}
}

// TestMaskRegistry 验证 registry 掩码:用户名可见,密码掩码。
func TestMaskRegistry(t *testing.T) {
	got := mask(TypeRegistry, "alice:supersecretpw")
	if !strings.HasPrefix(got, "alice ") {
		t.Fatalf("username not visible: %q", got)
	}
	if strings.Contains(got, "supersecretpw") {
		t.Fatalf("password leaked: %q", got)
	}
}
