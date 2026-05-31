// Package gitauth centralizes how an HTTPS git token is turned into BasicAuth
// credentials for clone / ls-remote across the codebase (source reader, project
// prober, build cloner, AI diff/analyze).
//
// 背景:不同平台对「token 经 HTTPS BasicAuth」的用户名要求不同:
//   - GitHub / GitLab / 自建 Gitea 等:用户名任意非空,密码=token(历史上本项目
//     一律写死 Username="git",对这些平台没问题)。
//   - **Gitee**:个人访问令牌走 HTTPS 时,用户名必须是「真实账号用户名」,密码=token;
//     用 "git" 当用户名会被拒(表现为「凭据错误 / 认证失败」)。这正是用户真实
//     Gitee 令牌克隆失败的根因。
//
// 因此本包按仓库 URL 的 host 决定用户名:仅对 gitee.com(及其子域)取 URL 路径里的
// owner 段当用户名(个人仓库场景 owner==账号名,正确);其余 host 一律保持 "git"
// (与历史行为完全一致,绝不回归 GitHub/GitLab)。
//
// 安全:token 仅作为 BasicAuth.Password,绝不拼进 URL / 日志 / 错误。
package gitauth

import (
	"net/url"
	"strings"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// defaultUsername 是非 Gitee 平台沿用的 BasicAuth 用户名(任意非空即可)。
const defaultUsername = "git"

// BasicAuth 依据 repoURL 选择合适的 BasicAuth 用户名,密码恒为 token。
// token 为空时仍返回非 nil（go-git 对匿名公开仓亦可,鉴权失败再由调用方映射干净错误)。
func BasicAuth(repoURL, token string) *githttp.BasicAuth {
	return &githttp.BasicAuth{Username: Username(repoURL), Password: token}
}

// Username 返回该 repoURL 应使用的 BasicAuth 用户名。
//   - gitee.com / *.gitee.com:取 URL 路径第一段(owner)作用户名;取不到则回退 "git"。
//   - 其余 host:恒为 "git"(历史行为,不回归)。
//
// 解析失败 / 无 host 时回退 "git",绝不 panic。
func Username(repoURL string) string {
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
