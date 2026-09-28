package gitauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

func TestSSHAuthUsesPinnedHostKeyAlgorithms(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(fmt.Sprintf("[192.168.31.105]:2424 %s", ssh.MarshalAuthorizedKey(signer.PublicKey()))), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)
	auth, err := AuthMethod("ssh://git@192.168.31.105:2424/org/repo.git", "", "password")
	if err != nil {
		t.Fatal(err)
	}
	password, ok := auth.(*gitssh.Password)
	if !ok {
		t.Fatalf("auth method = %T, want SSH password", auth)
	}
	config, err := password.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.HostKeyAlgorithms) != 1 || config.HostKeyAlgorithms[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("host key algorithms = %v, want ed25519 only", config.HostKeyAlgorithms)
	}
}

func TestAllowedRepoURLSSH(t *testing.T) {
	for _, addr := range []string{
		"ssh://git@192.168.31.105:2424/root/repo.git",
		"git@git.example.com:team/repo.git",
		"https://gitee.com/org/repo.git",
	} {
		if !AllowedRepoURL(addr) {
			t.Errorf("allowed Git address rejected: %q", addr)
		}
	}
	for _, addr := range []string{
		"ssh://git@127.0.0.1/repo.git",
		"git@169.254.169.254:repo.git",
		"ssh://git:password@192.168.31.105/repo.git",
		"file:///tmp/repo.git",
		"git://git.example.com/repo.git",
		"git@git.example.com:",
	} {
		if AllowedRepoURL(addr) {
			t.Errorf("unsafe Git address accepted: %q", addr)
		}
	}
}

func TestAuthMethodSSHKeyAndPassword(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	auth, err := AuthMethod("ssh://git@192.168.31.105:2424/root/repo.git", "ignored", key)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := auth.(*gitssh.PublicKeys); !ok || got.User != "git" {
		t.Fatalf("key auth = %T", auth)
	}
	auth, err = AuthMethod("deploy@host:org/repo.git", "ignored", "my-password")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := auth.(*gitssh.Password); !ok || got.User != "deploy" || got.Password != "my-password" {
		t.Fatalf("password auth = %T", auth)
	}
	if _, err = AuthMethod("git@host:org/repo.git", "", "-----BEGIN PRIVATE KEY-----\nbad"); !errors.Is(err, ErrInvalidAuth) {
		t.Fatalf("bad key should fail without leaking bytes: %v", err)
	}
}

func TestUsername(t *testing.T) {
	cases := []struct {
		name    string
		repoURL string
		want    string
	}{
		// Gitee:取 owner 段当用户名(用户的真实仓库场景)
		{"gitee personal repo", "https://gitee.com/cool-jiawei/aireboot.git", "cool-jiawei"},
		{"gitee no .git suffix", "https://gitee.com/cool-jiawei/aireboot", "cool-jiawei"},
		{"gitee with port", "https://gitee.com:443/octo/app.git", "octo"},
		{"gitee subdomain", "https://api.gitee.com/octo/app.git", "octo"},
		{"gitee with creds in url", "https://u:p@gitee.com/octo/app.git", "octo"},
		// 非 Gitee:沿用 "git"(不回归)
		{"github", "https://github.com/octo/app.git", "git"},
		{"gitlab", "https://gitlab.com/octo/app.git", "git"},
		{"self-hosted", "https://git.example.com/octo/app.git", "git"},
		// 退化:仍回退 "git",不 panic
		{"empty", "", "git"},
		{"garbage", "::::not a url", "git"},
		{"gitee no path", "https://gitee.com", "git"},
		{"gitee root slash", "https://gitee.com/", "git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Username(c.repoURL, ""); got != c.want {
				t.Errorf("Username(%q, empty) = %q, want %q", c.repoURL, got, c.want)
			}
		})
	}
}

func TestBasicAuthCarriesToken(t *testing.T) {
	auth := BasicAuth("https://gitee.com/cool-jiawei/aireboot.git", "actual-account", "token-value")
	if auth.Username != "actual-account" {
		t.Errorf("username = %q, want actual-account", auth.Username)
	}
	if auth.Password != "token-value" {
		t.Errorf("password not carried through")
	}
}

func TestBasicAuthReturnsNilWithoutToken(t *testing.T) {
	if auth := BasicAuth("file:///tmp/fixture", "", ""); auth != nil {
		t.Fatalf("empty token should use anonymous access, got %+v", auth)
	}
}

func TestUsernameUsesExplicitAccountForEveryHost(t *testing.T) {
	if got := Username("https://gitee.com/university-org/private-repo.git", "actual-account"); got != "actual-account" {
		t.Fatalf("Username() = %q, want actual-account", got)
	}
	for _, repoURL := range []string{
		"https://github.com/org/private-repo.git",
		"https://gitlab.example.com:8443/group/subgroup/private-repo.git",
		"http://git.internal.example/group/private-repo.git",
	} {
		if got := Username(repoURL, " actual-account "); got != "actual-account" {
			t.Fatalf("Username(%q) = %q, want actual-account", repoURL, got)
		}
	}
	if got := Username("https://gitlab.example.com/group/repo.git", "   "); got != "git" {
		t.Fatalf("empty explicit username = %q, want git", got)
	}
	auth := BasicAuth("https://gitlab.example.com/group/repo.git", "actual-account", " secret with spaces ")
	if auth.Username != "actual-account" || auth.Password != " secret with spaces " {
		t.Fatal("BasicAuth must preserve the explicit identity and secret bytes")
	}
}
