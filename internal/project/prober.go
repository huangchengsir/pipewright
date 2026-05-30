package project

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
)

// probeTimeout 是单次 ls-remote 探测的硬超时(防黑洞 IP/慢 DNS 把请求 goroutine 挂到 OS TCP 超时)。
const probeTimeout = 15 * time.Second

// goGitProber 用 go-git 的 ListRemote(= git ls-remote 语义)做仓库连通校验。
// 纯 Go,不要求宿主装 git;HTTPS + token auth(参数化 API,绝不拼命令字符串)。
// 全程在内存进行(memory.NewStorage),不落任何工作区/对象到磁盘。
//
// SSRF 收口:生产路径仅允许 http/https scheme;拒云元数据/链路本地/回环地址;
// 私网 IP(自托管内网 Git)放行。allowInsecureSchemes 仅供测试注入(放行 file:// 等本地夹具),
// 生产构造(project.New 默认)永远为 false。
type goGitProber struct {
	// allowInsecureSchemes 仅供测试:为 true 时跳过 scheme/host SSRF 校验(放行 file:// 夹具)。
	// 生产路径绝不设置此字段。
	allowInsecureSchemes bool
}

// Probe 用 token 对 repoURL 做 ListRemote,成功返回远端默认分支(由 HEAD 符号引用解析)。
//
// 安全:token 仅作为 BasicAuth.Password 经参数化 API 传入,绝不进 URL/日志/错误。
// 失败统一映射为干净领域错误:鉴权类 → ErrCredentialError;其余(DNS/连接/不存在)
// → ErrRepoUnreachable。返回的错误不 %w 原始错误,避免把含敏感细节的底层错误外泄。
func (p goGitProber) Probe(ctx context.Context, repoURL, token string) (string, error) {
	if strings.TrimSpace(repoURL) == "" {
		return "", ErrRepoUnreachable
	}

	if !p.allowInsecureSchemes {
		if err := validateRepoURL(repoURL); err != nil {
			return "", err
		}
	}

	rem := gogit.NewRemote(memory.NewStorage(), &gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})

	// Gitee/GitHub 等 HTTPS token 鉴权:用户名任意非空,密码=token。
	auth := &githttp.BasicAuth{Username: "git", Password: token}

	// 硬超时:防黑洞 IP/慢 DNS 把请求 goroutine 挂死。
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	refs, err := rem.ListContext(cctx, &gogit.ListOptions{Auth: auth})
	if err != nil {
		return "", classifyProbeErr(err)
	}

	return defaultBranchFromRefs(refs), nil
}

// validateRepoURL 对仓库地址做 SSRF 收口(生产路径):
//   - scheme 仅允许 http/https(拒 file://、ssh://、git:// 等);
//   - 解析 host:拒云元数据/链路本地(169.254.0.0/16、fe80::/10)与回环(127.0.0.0/8、::1);
//   - 私网 IP(10/172.16/192.168、fc00::/7)放行(自托管内网 Git 友好)。
//
// 返回 ErrRepoUnreachable(不泄漏内部细节/不含 token)。
func validateRepoURL(repoURL string) error {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return ErrRepoUnreachable
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return ErrRepoUnreachable
	}
	host := u.Hostname()
	if host == "" {
		return ErrRepoUnreachable
	}
	// 字面 IP:直接判定;非 IP 主机名:解析后逐一判定,任一被拒即拒。
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return ErrRepoUnreachable
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		// 解析失败:留给 ls-remote 路径(会归 ErrRepoUnreachable);不在此区分以免误拒临时 DNS 抖动。
		return nil
	}
	for _, ip := range addrs {
		if isBlockedIP(ip) {
			return ErrRepoUnreachable
		}
	}
	return nil
}

// isBlockedIP 判定 IP 是否落在禁止区:回环、链路本地(含云元数据 169.254.169.254)、
// 未指定地址。私网(RFC1918 / fc00::/7)不在此列(放行)。
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// classifyProbeErr 把 go-git/transport 错误映射为干净领域错误(不携带底层文本)。
//
// 仅依赖 go-git/transport 的哨兵错误判定凭据问题,绝不做 "401"/"403" 文本子串嗅探
// (会把 "403ms"、含数字的主机名等无关文本误判为鉴权失败)。无法明确归为凭据错误的
// 一律归不可达(DNS/连接/仓库不存在/协议错误等)。
func classifyProbeErr(err error) error {
	switch {
	case errors.Is(err, transport.ErrAuthenticationRequired),
		errors.Is(err, transport.ErrAuthorizationFailed),
		errors.Is(err, transport.ErrInvalidAuthMethod):
		return ErrCredentialError
	default:
		return ErrRepoUnreachable
	}
}

// defaultBranchFromRefs 从 ls-remote 引用列表解析远端默认分支(HEAD 指向的分支短名)。
// 解析不出时返回空字符串(调用方按缺省处理,不视为错误)。
func defaultBranchFromRefs(refs []*plumbing.Reference) string {
	// HEAD 通常是指向 refs/heads/<branch> 的符号引用。
	for _, r := range refs {
		if r.Name() == plumbing.HEAD && r.Type() == plumbing.SymbolicReference {
			return r.Target().Short()
		}
	}
	// 退化:若 HEAD 是 hash 引用,匹配同 hash 的某个分支。
	var headHash plumbing.Hash
	for _, r := range refs {
		if r.Name() == plumbing.HEAD {
			headHash = r.Hash()
			break
		}
	}
	if !headHash.IsZero() {
		for _, r := range refs {
			if r.Name().IsBranch() && r.Hash() == headHash {
				return r.Name().Short()
			}
		}
	}
	return ""
}
