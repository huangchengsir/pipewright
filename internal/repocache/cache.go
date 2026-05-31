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
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/gitauth"
)

// fetchTimeout 是单次镜像 clone/fetch 的硬超时(防大仓库黑洞拖死构建)。
const fetchTimeout = 10 * time.Minute

// networkCloner 抽象「直连网络克隆」回退(*build.Cloner 即满足)。
type networkCloner interface {
	Clone(ctx context.Context, repoURL, token, branch, commit, destDir string) (*build.CloneResolved, error)
}

// Cache 是持久本地仓库镜像缓存。每仓库一把锁;失败回退 fallback。
type Cache struct {
	root     string
	fallback networkCloner
	locks    sync.Map // repoKey → *sync.Mutex
	// allowInsecure 仅供测试:为 true 时跳过 SSRF scheme/host 校验(放行本地夹具仓库)。
	// 生产构造(New)绝不设置;镜像仅对 build.IsRepoURLAllowed 通过的 URL 进行(同款 SSRF 收口)。
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
func (c *Cache) ensureMirror(ctx context.Context, repoURL, token string) (string, error) {
	// SSRF 收口:生产仅镜像 http/https 且非元数据/回环/链路本地的仓库(复用 build 同款校验)。
	// 不通过 → 返回错误,由 Clone 回退直连(build.Cloner 同样会拒,净效果=拦截)。
	if !c.allowInsecure && !build.IsRepoURLAllowed(repoURL) {
		return "", errors.New("repocache: repo url not allowed")
	}
	mirror := c.mirrorPath(repoURL)
	auth := gitauth.BasicAuth(repoURL, token)
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
func (c *Cache) Clone(ctx context.Context, repoURL, token, branch, commit, destDir string) (*build.CloneResolved, error) {
	lock := c.repoLock(repoURL)
	lock.Lock()
	defer lock.Unlock()

	mirror, err := c.ensureMirror(ctx, repoURL, token)
	if err != nil {
		// 镜像不可用(首次拉取失败 / 损坏)→ 回退直连网络克隆,绝不让构建挂。
		if c.fallback != nil {
			return c.fallback.Clone(ctx, repoURL, token, branch, commit, destDir)
		}
		return nil, err
	}

	// 从**本地镜像**克隆到工作区(走本地 FS,不触网,秒级)。
	resolved, cerr := c.checkoutFromMirror(ctx, mirror, branch, commit, destDir)
	if cerr != nil {
		// 本地 checkout 失败(罕见:ref 不在镜像 / 镜像损坏)→ 同样回退直连。
		if c.fallback != nil {
			_ = os.RemoveAll(destDir)
			return c.fallback.Clone(ctx, repoURL, token, branch, commit, destDir)
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

	resolved := &build.CloneResolved{}
	if commit != "" {
		wt, werr := repo.Worktree()
		if werr != nil {
			return nil, werr
		}
		if cerr := wt.Checkout(&gogit.CheckoutOptions{Hash: plumbing.NewHash(commit), Force: true}); cerr != nil {
			return nil, cerr
		}
		resolved.CommitShort = shortSHA(commit)
	} else if head, herr := repo.Head(); herr == nil {
		resolved.CommitShort = shortSHA(head.Hash().String())
	}
	return resolved, nil
}

// ListRefs 列出某仓库的分支与 tag(确保镜像最新后从本地镜像读;供前端下拉,不每次触网克隆)。
func (c *Cache) ListRefs(ctx context.Context, repoURL, token string) (*Refs, error) {
	lock := c.repoLock(repoURL)
	lock.Lock()
	defer lock.Unlock()

	mirror, err := c.ensureMirror(ctx, repoURL, token)
	if err != nil {
		return nil, err
	}
	repo, oerr := gogit.PlainOpen(mirror)
	if oerr != nil {
		return nil, oerr
	}
	out := &Refs{}
	if bIter, berr := repo.Branches(); berr == nil {
		_ = bIter.ForEach(func(r *plumbing.Reference) error {
			out.Branches = append(out.Branches, Ref{Name: r.Name().Short(), Commit: r.Hash().String()})
			return nil
		})
	}
	if tIter, terr := repo.Tags(); terr == nil {
		_ = tIter.ForEach(func(r *plumbing.Reference) error {
			out.Tags = append(out.Tags, Ref{Name: r.Name().Short(), Commit: r.Hash().String(), IsTag: true})
			return nil
		})
	}
	sort.Slice(out.Branches, func(i, j int) bool { return out.Branches[i].Name < out.Branches[j].Name })
	sort.Slice(out.Tags, func(i, j int) bool { return out.Tags[i].Name < out.Tags[j].Name })
	return out, nil
}

// shortSHA 取 commit 的 7 位短 sha(与 build.Cloner 同语义)。
func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}
