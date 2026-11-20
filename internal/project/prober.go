package project

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/huangchengsir/pipewright/internal/gitauth"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
)

// probeTimeout 是单次 ls-remote 探测的硬超时(防黑洞 IP/慢 DNS 把请求 goroutine 挂到 OS TCP 超时)。
const probeTimeout = 15 * time.Second

// goGitProber 用 go-git 的 ListRemote(= git ls-remote 语义)做仓库连通校验。
// 纯 Go,不要求宿主装 git;HTTP(S) 或 SSH 凭据经参数化 API 传入。
// 全程在内存进行(memory.NewStorage),不落任何工作区/对象到磁盘。
//
// 共享地址策略拒绝云元数据、链路本地和回环地址。管理员登记的同机 Gitea 仓库经
// 独立只读传输访问。allowInsecureSchemes 仅供测试注入(放行 file:// 等本地夹具),
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
func (p goGitProber) Probe(ctx context.Context, repoURL, username, token string) (string, error) {
	if strings.TrimSpace(repoURL) == "" {
		return "", ErrRepoUnreachable
	}

	networkURL := repoURL
	if !p.allowInsecureSchemes {
		var err error
		networkURL, err = resolveProbeRepoURL(repoURL)
		if err != nil {
			return "", err
		}
	}

	rem := gogit.NewRemote(memory.NewStorage(), &gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{networkURL},
	})

	auth, err := gitauth.AuthMethod(repoURL, username, token)
	if err != nil {
		return "", ErrCredentialError
	}

	// 硬超时:防黑洞 IP/慢 DNS 把请求 goroutine 挂死。
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	refs, err := rem.ListContext(cctx, &gogit.ListOptions{Auth: auth})
	if err != nil {
		return "", classifyProbeErr(err)
	}

	return defaultBranchFromRefs(refs), nil
}

// resolveProbeRepoURL reports policy rejection separately from network and credential errors.
func resolveProbeRepoURL(repoURL string) (string, error) {
	resolved, err := gitauth.ResolveRepoURL(repoURL)
	if err == nil {
		return resolved, nil
	}
	repoURL = strings.ToLower(strings.TrimSpace(repoURL))
	if strings.Contains(repoURL, "://") && !strings.HasPrefix(repoURL, "http://") && !strings.HasPrefix(repoURL, "https://") && !gitauth.IsSSH(repoURL) {
		return "", ErrUnsupportedRepoProtocol
	}
	return "", ErrRepoPolicyBlocked
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
