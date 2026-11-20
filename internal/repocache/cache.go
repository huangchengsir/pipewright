// Package repocache 是「代码管理区」(本地仓库镜像缓存 · Story 8-18 / FR-8-18)。
//
// 背景:每次构建都对远端做一次浅克隆(用完即删),大仓库 / 高频构建时反复全量拉取慢且费网;
// 且前端触发流水线时分支/commit 是用户手敲、无数据支撑。本包在中控机维护**持久 bare 镜像**:
//
//	<root>/<repo-hash>.git   (git --mirror,含全部分支/tag)
//
// 首次 `clone --mirror`;之后每次构建只 `fetch`(增量,只拉新 commit)→ 再从**本地镜像**秒级出工作区
// (走本地文件系统,不触网)。镜像同时给「列分支/tag」提供数据(ListRefs),供前端下拉。
//
// 设计:纯 go-git(不依赖宿主 git,延续 cloner 风格);每仓库一把锁(并发构建同仓库串行 fetch/checkout);
// **任何缓存问题都回退直连网络克隆**(永不让构建因缓存挂)。token 经 BasicAuth.Password,绝不进 URL/日志。
package repocache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/gitauth"
)

// fetchTimeout 是单次镜像 clone/fetch 的硬超时(防大仓库黑洞拖死构建)。
const fetchTimeout = 10 * time.Minute

// networkCloner 抽象「直连网络克隆」回退(*build.Cloner 即满足)。
type networkCloner interface {
	Clone(ctx context.Context, repoURL, username, token, branch, commit, destDir string) (*build.CloneResolved, error)
}

// Cache 是持久本地仓库镜像缓存。每仓库一把锁;失败回退 fallback。
type Cache struct {
	root     string
	fallback networkCloner
	locks    sync.Map // repoKey → *sync.Mutex
	// allowInsecure 仅供测试:为 true 时跳过 SSRF scheme/host 校验(放行本地夹具仓库)。
	// 生产构造(New)绝不设置;共享镜像使用全平台地址策略，同机仓库独立读取。
	allowInsecure bool
}

// New 构造代码缓存,root 不存在则创建(0700:源码可能私有)。fallback 为缓存不可用时的直连克隆器。
func New(root string, fallback networkCloner) (*Cache, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("repocache: empty root")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Cache{root: root, fallback: fallback}, nil
}

// ErrCacheDisabled indicates that shared mirrors are unavailable for ordinary repositories.
var ErrCacheDisabled = errors.New("repocache: shared mirrors unavailable")

// NewLocalRefs supports authenticated refs for local Gitea with no shared cache.
func NewLocalRefs() *Cache { return &Cache{} }

// Ref 是一条仓库引用(分支或 tag)+ 其指向的 commit。
type Ref struct {
	Name   string // 分支名 / tag 名(不含 refs/heads|tags/ 前缀)
	Commit string // 完整 commit sha
	IsTag  bool
}

// Refs 是某仓库的分支与 tag 列表(供前端下拉)。
type Refs struct {
	Branches []Ref
	Tags     []Ref
}

// mirrorPath 把 repoURL 映射为镜像目录(<root>/<sha16>.git)。
func (c *Cache) mirrorPath(repoURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(repoURL)))
	return filepath.Join(c.root, hex.EncodeToString(sum[:])[:16]+".git")
}

// repoLock 取某仓库的互斥锁(并发构建同仓库串行,避免 fetch/checkout 打架)。
func (c *Cache) repoLock(repoURL string) *sync.Mutex {
	m, _ := c.locks.LoadOrStore(c.mirrorPath(repoURL), &sync.Mutex{})
	return m.(*sync.Mutex)
}

// ensureMirror 确保本地 bare 镜像存在并增量更新到最新(首次 mirror 克隆,后续 fetch)。
// 返回镜像目录;失败返回 error(调用方回退直连克隆)。已是最新(ErrAlreadyUpToDate)视为成功。
func (c *Cache) ensureMirror(ctx context.Context, repoURL, username, token string) (string, error) {
	if !c.allowInsecure {
		repoURL = strings.TrimSpace(repoURL)
		if gitauth.IsLocalGitAddress(repoURL) || !gitauth.AllowedRepoURL(repoURL) {
			return "", build.ErrRepoBlocked
		}
	}
	mirror := c.mirrorPath(repoURL)
	auth, err := gitauth.AuthMethod(repoURL, username, token)
	if err != nil {
		return "", errors.New("repocache: invalid git credential")
	}
	cctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	if _, err := os.Stat(mirror); os.IsNotExist(err) {
		// 首次:mirror 克隆(含全部分支/tag)。失败清理半截目录。
		_, cerr := gogit.PlainCloneContext(cctx, mirror, true, &gogit.CloneOptions{
			URL:    repoURL,
			Auth:   auth,
			Mirror: true,
		})
		if cerr != nil {
			_ = os.RemoveAll(mirror)
			return "", cerr
		}
		return mirror, nil
	}

	// 增量:打开镜像 → fetch(mirror 远端 refspec 已是 +refs/*:refs/*)。
	repo, oerr := gogit.PlainOpen(mirror)
	if oerr != nil {
		return "", oerr
	}
	ferr := repo.FetchContext(cctx, &gogit.FetchOptions{Auth: auth, Force: true, Prune: true})
	if ferr != nil && !errors.Is(ferr, gogit.NoErrAlreadyUpToDate) {
		return "", ferr
	}
	return mirror, nil
}

// Clone 实现 build 的克隆契约:经本地镜像出工作区(命中走本地、镜像不可用回退直连)。
// branch/commit 语义同 build.Cloner.Clone:commit 优先,否则 branch,皆空则默认分支。
func (c *Cache) Clone(ctx context.Context, repoURL, username, token, branch, commit, destDir string) (*build.CloneResolved, error) {
	if !c.allowInsecure && gitauth.IsLocalGitAddress(repoURL) {
		if _, err := gitauth.ResolveRepoURL(repoURL); err != nil {
			return nil, build.ErrRepoBlocked
		}
		// Local Gitea is credential-scoped and never seeds the shared mirror.
		if c.fallback != nil {
			return c.fallback.Clone(ctx, repoURL, username, token, branch, commit, destDir)
		}
		return build.NewCloner().Clone(ctx, repoURL, username, token, branch, commit, destDir)
	}
	lock := c.repoLock(repoURL)
	lock.Lock()
	defer lock.Unlock()

	mirror, err := c.ensureMirror(ctx, repoURL, username, token)
	if err != nil {
		if errors.Is(err, build.ErrRepoBlocked) {
			return nil, err
		}
		// 镜像不可用(首次拉取失败 / 损坏)→ 回退直连网络克隆,绝不让构建挂。
		if c.fallback != nil {
			return c.fallback.Clone(ctx, repoURL, username, token, branch, commit, destDir)
		}
		return nil, err
	}

	// 从**本地镜像**克隆到工作区(走本地 FS,不触网,秒级)。
	resolved, cerr := c.checkoutFromMirror(ctx, mirror, branch, commit, destDir)
	if cerr != nil {
		// 本地 checkout 失败(罕见:ref 不在镜像 / 镜像损坏)→ 同样回退直连。
		if c.fallback != nil {
			_ = os.RemoveAll(destDir)
			return c.fallback.Clone(ctx, repoURL, username, token, branch, commit, destDir)
		}
		return nil, cerr
	}
	return resolved, nil
}

// checkoutFromMirror 从本地 bare 镜像克隆出工作区(本地 FS,无需 auth/网络)。
func (c *Cache) checkoutFromMirror(ctx context.Context, mirror, branch, commit, destDir string) (*build.CloneResolved, error) {
	cctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	branch = strings.TrimSpace(branch)
	commit = strings.TrimSpace(commit)

	opts := &gogit.CloneOptions{URL: mirror, Tags: gogit.NoTags}
	if commit == "" {
		opts.Depth = 1
		opts.SingleBranch = true
		if branch != "" {
			opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		}
	}
	repo, err := gogit.PlainCloneContext(cctx, destDir, false, opts)
	if err != nil {
		return nil, err
	}

	if commit != "" {
		wt, werr := repo.Worktree()
		if werr != nil {
			return nil, werr
		}
		if cerr := wt.Checkout(&gogit.CheckoutOptions{Hash: plumbing.NewHash(commit), Force: true}); cerr != nil {
			return nil, cerr
		}
	}
	return build.ResolveRepositoryHead(repo)
}

// refsNetworkURL routes registered local repositories directly, even with the
// shared cache disabled. The boolean distinguishes a private transport.
func (c *Cache) refsNetworkURL(repoURL string) (string, bool, error) {
	if !c.allowInsecure && gitauth.IsLocalGitAddress(repoURL) {
		resolved, err := gitauth.ResolveRepoURL(repoURL)
		if err != nil {
			return "", false, build.ErrRepoBlocked
		}
		return resolved, true, nil
	}
	if c.root == "" {
		return "", false, ErrCacheDisabled
	}
	return repoURL, false, nil
}

// openMirror opens a refreshed mirror and holds its lock through the read.
func (c *Cache) openMirror(ctx context.Context, repoURL, username, token string) (*gogit.Repository, func(), error) {
	lock := c.repoLock(repoURL)
	lock.Lock()
	mirror, err := c.ensureMirror(ctx, repoURL, username, token)
	if err != nil {
		lock.Unlock()
		return nil, nil, err
	}
	repo, err := gogit.PlainOpen(mirror)
	if err != nil {
		lock.Unlock()
		return nil, nil, err
	}
	return repo, lock.Unlock, nil
}

// openLocalHistory holds private objects only in a request-local directory.
func openLocalHistory(ctx context.Context, repoURL, networkURL, username, token string) (*gogit.Repository, func(), error) {
	auth, err := gitauth.AuthMethod(repoURL, username, token)
	if err != nil {
		return nil, nil, errors.New("repocache: invalid git credential")
	}
	dir, err := os.MkdirTemp("", "pipewright-gitea-history-*")
	if err != nil {
		return nil, nil, errors.New("repocache: temporary repository unavailable")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	repo, err := gogit.PlainCloneContext(ctx, dir, true, &gogit.CloneOptions{URL: networkURL, Auth: auth, Mirror: true})
	if err != nil {
		cleanup()
		return nil, nil, build.ErrCloneFailed
	}
	return repo, cleanup, nil
}

// ListRefs lists branches and tags using the mirror or authenticated local ref discovery.
func (c *Cache) ListRefs(ctx context.Context, repoURL, username, token string) (*Refs, error) {
	url, local, err := c.refsNetworkURL(repoURL)
	if err != nil {
		return nil, err
	}
	var refs []*plumbing.Reference
	if local {
		auth, err := gitauth.AuthMethod(repoURL, username, token)
		if err != nil {
			return nil, errors.New("repocache: invalid git credential")
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		remote := gogit.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{url}})
		refs, err = remote.ListContext(ctx, &gogit.ListOptions{Auth: auth})
		if err != nil {
			return nil, build.ErrCloneFailed
		}
	} else {
		repo, unlock, err := c.openMirror(ctx, repoURL, username, token)
		if err != nil {
			return nil, err
		}
		defer unlock()
		if iter, err := repo.References(); err == nil {
			_ = iter.ForEach(func(ref *plumbing.Reference) error {
				refs = append(refs, ref)
				return nil
			})
		}
	}
	out := &Refs{}
	for _, ref := range refs {
		name := ref.Name()
		if name.IsBranch() {
			out.Branches = append(out.Branches, Ref{Name: name.Short(), Commit: ref.Hash().String()})
		} else if name.IsTag() && !strings.HasSuffix(string(name), "^{}") {
			out.Tags = append(out.Tags, Ref{Name: name.Short(), Commit: ref.Hash().String(), IsTag: true})
		}
	}
	sort.Slice(out.Branches, func(i, j int) bool { return out.Branches[i].Name < out.Branches[j].Name })
	sort.Slice(out.Tags, func(i, j int) bool { return out.Tags[i].Name < out.Tags[j].Name })
	return out, nil
}

// Commit 是一条提交摘要(供前端 commit 下拉:sha + 首行说明 + 作者 + 时刻)。
type Commit struct {
	SHA     string // 完整 sha
	Short   string // 7 位短 sha
	Subject string // 提交说明首行
	Author  string // 作者名
	When    time.Time
}

// ListCommits 列出某 ref(分支名/tag/commit sha)上最近 limit 条提交(镜像增量更新后从本地 git log 读)。
// ref 空 → 用镜像 HEAD;limit<=0 或过大 → 夹到 [1,200]。供前端选 commit 提供数据。
func (c *Cache) ListCommits(ctx context.Context, repoURL, username, token, ref string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 200 {
		limit = 200
	}
	url, local, err := c.refsNetworkURL(repoURL)
	if err != nil {
		return nil, err
	}
	var repo *gogit.Repository
	var cleanup func()
	if local {
		repo, cleanup, err = openLocalHistory(ctx, repoURL, url, username, token)
	} else {
		repo, cleanup, err = c.openMirror(ctx, repoURL, username, token)
	}
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// 解析起点 hash:ref 是分支/tag 名 → 解析其引用;否则当 commit sha;空 → HEAD。
	var from plumbing.Hash
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		h, herr := repo.Head()
		if herr != nil {
			return nil, herr
		}
		from = h.Hash()
	default:
		if h, rerr := repo.ResolveRevision(plumbing.Revision(ref)); rerr == nil && h != nil {
			from = *h
		} else {
			from = plumbing.NewHash(ref)
		}
	}

	iter, lerr := repo.Log(&gogit.LogOptions{From: from})
	if lerr != nil {
		return nil, lerr
	}
	defer iter.Close()

	out := make([]Commit, 0, limit)
	_ = iter.ForEach(func(co *object.Commit) error {
		sha := co.Hash.String()
		out = append(out, Commit{
			SHA:     sha,
			Short:   shortSHA(sha),
			Subject: firstLine(co.Message),
			Author:  co.Author.Name,
			When:    co.Author.When,
		})
		if len(out) >= limit {
			return errStopIter
		}
		return nil
	})
	return out, nil
}

// errStopIter 是提前结束 commit 遍历的哨兵(达到 limit)。
var errStopIter = errors.New("stop")

// firstLine 取多行提交说明的首行(去空白)。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// shortSHA 取 commit 的 7 位短 sha(与 build.Cloner 同语义)。
func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}
