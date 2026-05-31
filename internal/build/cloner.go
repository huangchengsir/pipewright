package build

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/huangchengsir/pipewright/internal/gitauth"
)

// cloneTimeout 是单次克隆的硬超时(防黑洞 IP / 慢 DNS 把构建 goroutine 挂死)。
const cloneTimeout = 5 * time.Minute

// 克隆领域错误(不泄漏 URL 密钥/底层细节)。
var (
	// ErrRepoBlocked 表示仓库地址被 SSRF 收口拒绝(云元数据/链路本地/回环等)。
	ErrRepoBlocked = errors.New("build: repo url blocked by ssrf guard")
	// ErrCloneFailed 表示克隆/检出失败(鉴权/网络/ref 不存在等;不泄漏底层文本)。
	ErrCloneFailed = errors.New("build: clone failed")
)

// Cloner 把项目仓库在指定 commit/branch 上克隆到**磁盘临时工作区**(Docker build 需 `.` 上下文落盘)。
//
// 与 2-5/3-6 的内存克隆(memfs)不同:构建上下文须是真实目录树供容器 CLI 读取。工作区由调用方
// (Builder)经 MkdirTemp 建、defer RemoveAll 销(宿主零污染,FR-5)。SSRF 收口复用全平台同款策略:
// 仅 http/https;拒云元数据/链路本地/回环;私网放行(自托管内网 Git 友好)。
type Cloner struct {
	// allowInsecure 仅供测试:为 true 时跳过 SSRF scheme/host 校验(放行 file:// 本地夹具)。
	// 生产路径绝不设置(NewCloner 默认 false)。
	allowInsecure bool
}

// NewCloner 构造生产 Cloner(严格 SSRF 收口)。
func NewCloner() *Cloner { return &Cloner{} }

// CloneResolved 是克隆结果:实际检出的 commit 短 sha(供产物 tag/引用),空则未解析出。
type CloneResolved struct {
	CommitShort string
}

// Clone 把 repoURL 在 ref(commit sha 优先,否则分支名;皆空则默认分支)上克隆到 destDir。
// token 经 BasicAuth.Password 传入(绝不进 URL/日志/错误)。失败统一映射干净错误。
//
// 策略:先克隆默认/指定分支(Depth:1 浅克隆省带宽),若指定了 commit 则再 checkout 到该 commit
//
//	(commit 不在浅克隆历史里时回退为不限深克隆重试一次,best-effort)。
func (c *Cloner) Clone(ctx context.Context, repoURL, token, branch, commit, destDir string) (*CloneResolved, error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return nil, ErrCloneFailed
	}
	if !c.allowInsecure && !validRepoURL(repoURL) {
		return nil, ErrRepoBlocked
	}

	cctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	auth := gitauth.BasicAuth(repoURL, token)
	commit = strings.TrimSpace(commit)
	branch = strings.TrimSpace(branch)

	opts := &gogit.CloneOptions{
		URL:  repoURL,
		Auth: auth,
		Tags: gogit.NoTags,
	}
	// 指定了具体 commit 时不浅克隆(浅克隆默认分支可能不含该 commit);否则浅克隆指定/默认分支。
	if commit == "" {
		opts.Depth = 1
		opts.SingleBranch = true
		if branch != "" {
			opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		}
	}

	repo, err := gogit.PlainCloneContext(cctx, destDir, false, opts)
	if err != nil {
		// 浅克隆 + 指定分支失败时不再重试(可能是鉴权/不可达);映射干净错误。
		return nil, ErrCloneFailed
	}

	resolved := &CloneResolved{}
	if commit != "" {
		wt, werr := repo.Worktree()
		if werr != nil {
			return nil, ErrCloneFailed
		}
		hash := plumbing.NewHash(commit)
		if cerr := wt.Checkout(&gogit.CheckoutOptions{Hash: hash, Force: true}); cerr != nil {
			return nil, ErrCloneFailed
		}
		resolved.CommitShort = shortSHA(commit)
	} else if head, herr := repo.Head(); herr == nil {
		resolved.CommitShort = shortSHA(head.Hash().String())
	}
	return resolved, nil
}

// validRepoURL 对仓库地址做 SSRF 收口(生产路径),复用全平台同款策略:
// 仅 http/https;拒云元数据/链路本地/回环;私网放行(自托管内网 Git 友好)。
func validRepoURL(repoURL string) bool {
	u, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !blockedIP(ip)
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return true // 留给 clone 路径(会失败映射);不在此误拒临时 DNS 抖动。
	}
	for _, ip := range addrs {
		if blockedIP(ip) {
			return false
		}
	}
	return true
}

// blockedIP 判定 IP 是否落在禁止区:回环、链路本地(含云元数据 169.254.169.254)、未指定。
// 私网(RFC1918 / fc00::/7)不在此列(放行)。
func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// shortSHA 取 commit 的前 7 位(短 sha);不足 7 位原样返回。
func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// mkTempWorkspace 建一个临时构建工作区目录(调用方 defer RemoveAll 销毁;宿主零污染,FR-5)。
func mkTempWorkspace() (string, error) {
	return os.MkdirTemp("", "pipewright-build-*")
}
