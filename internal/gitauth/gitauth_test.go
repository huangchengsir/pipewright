package gitauth

import "testing"

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
			if got := Username(c.repoURL); got != c.want {
				t.Errorf("Username(%q) = %q, want %q", c.repoURL, got, c.want)
			}
		})
	}
}

func TestBasicAuthCarriesToken(t *testing.T) {
	auth := BasicAuth("https://gitee.com/cool-jiawei/aireboot.git", "tok123")
	if auth.Username != "cool-jiawei" {
		t.Errorf("username = %q, want cool-jiawei", auth.Username)
	}
	if auth.Password != "tok123" {
		t.Errorf("password not carried through")
	}
}
