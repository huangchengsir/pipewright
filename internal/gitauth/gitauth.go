// Package gitauth centralizes HTTP(S) and Git SSH authentication and repository
// URL validation across clone and ls-remote callers.
//
// 背景:不同平台对「token 经 HTTPS BasicAuth」的用户名要求不同:
//   - GitHub / GitLab / 自建 Gitea 等:用户名任意非空,密码=token(历史上本项目
//     一律写死 Username="git",对这些平台没问题)。
//   - **Gitee**:个人访问令牌走 HTTPS 时,用户名必须是「真实账号用户名」,密码=token;
//     用 "git" 当用户名会被拒(表现为「凭据错误 / 认证失败」)。这正是用户真实
//     Gitee 令牌克隆失败的根因。
//
// 显式设置的用户名优先；未设置时，Gitee 从 URL owner 回退，其他平台沿用 "git"。
//
// 安全:token 仅作为 BasicAuth.Password,绝不拼进 URL / 日志 / 错误。
package gitauth

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// defaultUsername 是非 Gitee 平台沿用的 BasicAuth 用户名(任意非空即可)。
const defaultUsername = "git"

var ErrInvalidAuth = errors.New("git: invalid authentication credential")

// IsSSH reports whether the address uses an SSH URL or scp-style Git address.
func IsSSH(repoURL string) bool {
	repoURL = strings.TrimSpace(repoURL)
	return strings.HasPrefix(strings.ToLower(repoURL), "ssh://") ||
		(strings.Contains(repoURL, "@") && !strings.Contains(repoURL, "://") && strings.Contains(repoURL, ":"))
}

// AllowedRepoURL applies the same production SSRF policy to every Git entry point.
// Private network hosts are permitted for self-hosted Git; loopback and link-local are not.
func AllowedRepoURL(repoURL string) bool {
	repoURL = strings.TrimSpace(repoURL)
	var host string
	if IsSSH(repoURL) {
		if strings.HasPrefix(strings.ToLower(repoURL), "ssh://") {
			u, err := url.Parse(repoURL)
			if err != nil || u.User == nil || u.User.Username() == "" || u.Path == "" {
				return false
			}
			if _, hasPassword := u.User.Password(); hasPassword {
				return false
			}
		} else if strings.ContainsAny(repoURL, " \t\r\n") || !strings.Contains(repoURL, "@") {
			return false
		}
		ep, err := transport.NewEndpoint(repoURL)
		if err != nil || ep.Protocol != "ssh" || ep.User == "" || ep.Path == "" || ep.Password != "" {
			return false
		}
		host = ep.Host
	} else {
		u, err := url.Parse(repoURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path == "" {
			return false
		}
		host = u.Hostname()
	}
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !blockedIP(ip)
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return true
	}
	for _, ip := range addrs {
		if blockedIP(ip) {
			return false
		}
	}
	return true
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// AuthMethod keeps secrets out of repository URLs. SSH host keys are checked
// against the service account's known_hosts (or SSH_KNOWN_HOSTS); never TOFU.
func AuthMethod(repoURL, username, secret string) (transport.AuthMethod, error) {
	if !IsSSH(repoURL) {
		return BasicAuth(repoURL, username, secret), nil
	}
	ep, err := transport.NewEndpoint(strings.TrimSpace(repoURL))
	if err != nil || ep.Protocol != "ssh" || ep.User == "" || strings.TrimSpace(secret) == "" {
		return nil, ErrInvalidAuth
	}
	port := ep.Port
	if port <= 0 {
		port = 22
	}
	var hostKeyAlgorithms []string
	if db, err := gitssh.NewKnownHostsDb(); err == nil {
		hostKeyAlgorithms = db.HostKeyAlgorithms(net.JoinHostPort(ep.Host, strconv.Itoa(port)))
	}
	if strings.Contains(secret, "PRIVATE KEY") {
		keys, err := gitssh.NewPublicKeys(ep.User, []byte(secret), "")
		if err != nil {
			return nil, ErrInvalidAuth
		}
		keys.HostKeyAlgorithms = hostKeyAlgorithms
		return keys, nil
	}
	return &gitssh.Password{User: ep.User, Password: secret, HostKeyCallbackHelper: gitssh.HostKeyCallbackHelper{HostKeyAlgorithms: hostKeyAlgorithms}}, nil
}

// BasicAuth 依据 repoURL 选择合适的 BasicAuth 用户名,密码恒为 token。
// token 为空时返回 nil，让 go-git 按匿名公开仓访问。
func BasicAuth(repoURL, username, token string) *githttp.BasicAuth {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: Username(repoURL, username), Password: token}
}

// Username 返回该 repoURL 应使用的 BasicAuth 用户名。
//   - 显式用户名非空时优先使用。
//   - gitee.com / *.gitee.com:否则取 URL 路径第一段(owner)作用户名。
//   - 其余 host:回退 "git"(历史行为)。
//
// 解析失败 / 无 host 时回退 "git",绝不 panic。
func Username(repoURL, explicitUsername string) string {
	if username := strings.TrimSpace(explicitUsername); username != "" {
		return username
	}
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return defaultUsername
	}
	host := strings.ToLower(u.Hostname()) // Hostname() 自动剥离端口与用户信息
	if host == "" {
		return defaultUsername
	}
	if host == "gitee.com" || strings.HasSuffix(host, ".gitee.com") {
		if owner := firstPathSegment(u.Path); owner != "" {
			return owner
		}
	}
	return defaultUsername
}

// firstPathSegment 取 URL path 的第一段(owner)。"/cool-jiawei/aireboot.git" → "cool-jiawei"。
func firstPathSegment(p string) string {
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return ""
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return strings.TrimSpace(p)
}
