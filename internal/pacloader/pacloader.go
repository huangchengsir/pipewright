// Package pacloader 实现「流水线即代码」(Pipeline-as-code)运行时覆盖(FR-8-12)。
//
// 背景:项目的流水线配置原本只来自库内 pipeline.Service(UI 编辑、落库)。本包提供一个
// **装饰器** dagrun.SpecLoader(Loader),包住库内 loader:运行时若项目仓库根含合法的
// `.pipewright.yml`,就用它驱动本次运行;否则一律回退库内配置。
//
// ── 不破坏运行的铁律 ─────────────────────────────────────────────────────────
// 任何环节出问题(项目查不到、克隆/拉取失败、文件不存在、YAML 解析/校验失败、token
// 取不到)都**静默回退**到被包住的库内 loader——运行绝不因 YAML 问题而中断。仅在 debug
// 级别记一行原因(不含任何密钥)。
//
// ── 已知约束:无分支(KNOWN CONSTRAINT,显式处理)──────────────────────────────
// dagrun.SpecLoader.Get 只拿到 projectID,**没有本次运行的分支/ref**。因此本装饰器从
// **项目的默认分支**(项目记录里的 DefaultBranch)拉取 `.pipewright.yml`。
// 「按运行分支覆盖」(feature 分支上改 YAML 即对该分支运行生效)需要把运行分支贯穿进
// SpecLoader 签名 / dagrun 内核——风险较大,**本期推迟(DEFERRED)**,不动 SpecLoader
// 签名,也不碰 dagrun 执行内核。默认分支解析是本期可交付范围。
//
// ── 安全 ─────────────────────────────────────────────────────────────────────
// token 仅在进程内取用即弃,绝不进日志/错误/返回值。底层拉取错误不外泄(可能含 URL 凭据)。
package pacloader

import (
	"context"
	"log"
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/pipelineyaml"
)

// DefaultFile 是仓库根「流水线即代码」文件名。
const DefaultFile = ".pipewright.yml"

// SpecLoader 是被装饰的库内加载器契约(与 dagrun.SpecLoader 同形,避免反向 import dagrun)。
type SpecLoader interface {
	Get(ctx context.Context, projectID string) (*pipeline.Config, error)
}

// ProjectInfo 是覆盖装饰器拉取 `.pipewright.yml` 所需的项目最小视图。
type ProjectInfo struct {
	RepoURL       string
	CredentialID  string
	DefaultBranch string
}

// ProjectLookup 按 projectID 取仓库地址/凭据/默认分支(由 project.Service 适配)。
type ProjectLookup interface {
	Lookup(ctx context.Context, projectID string) (ProjectInfo, error)
}

// TokenRevealer 解密并返回凭据明文(由 vault.Vault.Reveal 适配)。
// 取不到时返回错误,装饰器据此回退(空 token 也可继续尝试公开仓库)。
type TokenRevealer interface {
	Reveal(credentialID string) (string, error)
}

// BlobFetcher 按 ref 读仓库单文件内容(由 httpapi.SourceReader 适配)。
// degraded=true 表示克隆失败的降级响应(非「真实空文件」),装饰器据此回退。
type BlobFetcher interface {
	FetchBlob(ctx context.Context, repoURL, token, ref, file string) (content string, degraded bool, err error)
}

// Loader 是 PAC 运行时覆盖装饰器:实现 dagrun.SpecLoader。
type Loader struct {
	inner    SpecLoader
	projects ProjectLookup
	tokens   TokenRevealer
	blobs    BlobFetcher
}

// New 构造装饰器。inner 必填(回退目标);projects/blobs 任一为 nil 时退化为纯透传(仅回退)。
// tokens 可为 nil(无保险库时按公开仓库以空 token 尝试)。
func New(inner SpecLoader, projects ProjectLookup, tokens TokenRevealer, blobs BlobFetcher) *Loader {
	return &Loader{inner: inner, projects: projects, tokens: tokens, blobs: blobs}
}

// Get 实现 dagrun.SpecLoader:尝试用仓库默认分支的 `.pipewright.yml` 覆盖;
// 任何问题都回退到 inner(库内配置)。绝不把 YAML 问题冒泡给调用方。
func (l *Loader) Get(ctx context.Context, projectID string) (*pipeline.Config, error) {
	if cfg, ok := l.tryOverride(ctx, projectID); ok {
		return cfg, nil
	}
	return l.inner.Get(ctx, projectID)
}

// tryOverride 尝试从仓库默认分支拉取并解析 `.pipewright.yml`;成功返回 (cfg,true)。
// 任何环节失败返回 (nil,false)(调用方回退)。失败只记 debug 原因(无密钥)。
func (l *Loader) tryOverride(ctx context.Context, projectID string) (*pipeline.Config, bool) {
	if l.projects == nil || l.blobs == nil {
		return nil, false
	}

	info, err := l.projects.Lookup(ctx, projectID)
	if err != nil {
		debugf("项目 %s 查询失败,回退库内配置", projectID)
		return nil, false
	}
	if strings.TrimSpace(info.RepoURL) == "" {
		return nil, false
	}

	// 取仓库凭据明文(进程内取用即弃;取不到不致命 → 空 token 尝试公开仓库)。
	token := ""
	if l.tokens != nil && strings.TrimSpace(info.CredentialID) != "" {
		if t, terr := l.tokens.Reveal(info.CredentialID); terr == nil {
			token = t
		}
	}

	content, degraded, ferr := l.blobs.FetchBlob(ctx, info.RepoURL, token, info.DefaultBranch, DefaultFile)
	if ferr != nil {
		// 文件不存在 / 克隆失败 → 回退(正常路径:多数项目没有 .pipewright.yml)。
		debugf("项目 %s 未读到 %s(回退库内配置)", projectID, DefaultFile)
		return nil, false
	}
	if degraded {
		debugf("项目 %s 仓库克隆降级,无法读 %s(回退库内配置)", projectID, DefaultFile)
		return nil, false
	}
	if strings.TrimSpace(content) == "" {
		return nil, false
	}

	cfg, perr := pipelineyaml.Parse([]byte(content))
	if perr != nil {
		// YAML 非法 → 回退(绝不让坏 YAML 中断运行)。错误不含密钥但仍只记 debug。
		debugf("项目 %s 的 %s 解析/校验失败,回退库内配置", projectID, DefaultFile)
		return nil, false
	}
	debugf("项目 %s 命中仓库 %s(默认分支 %s),用其驱动本次运行", projectID, DefaultFile, info.DefaultBranch)
	return &cfg, true
}

// debugf 记录回退原因(诊断用)。绝不打印 token / 仓库 URL 凭据。
func debugf(format string, args ...any) {
	log.Printf("[pac] "+format, args...)
}
