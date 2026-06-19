// Package previewenv 是「Per-PR 预览环境」(R4 E4.1 · 差异化王牌)的领域层。
//
// 核心理念:某 PR 的运行**成功部署**时,自动为它分配一个一次性预览域名
// pr-<n>-<proj>.<base>(复用 R3 的 dnsprovider.AllocateSubdomain:DNS-01 证书 + 反代路由),
// 让评审者点开链接就能看到这条 PR 的真实部署效果;PR 关闭/合并后回收(删路由 + 标记 reclaimed)。
//
// 设计纪律:
//   - **优雅降级铁律**:项目未开启预览 / 未配 DNS 提供商 / 分配失败 → 整个 provision 静默 no-op,
//     **绝不**让预览功能的任何失败影响(更别说阻断)部署本身。Provision 全程 recover-safe。
//   - 幂等:同一 (projectID, prNumber) 重新部署 → 更新同一行(刷新路由),不重复建环境。
//   - 凭据零外泄:DNS token 全程走 R3 vault 路径,本包不碰 token;DTO/审计/日志绝无密钥。
//   - 子域名由**已校验**的 base domain 派生(allocator 内再校验上游容器/端口/宿主机 IP)。
package previewenv

import (
	"context"
	"errors"
	"time"
)

// 预览环境状态枚举(DB 存小写串;JSON 同值)。
const (
	// StatusActive 表示预览环境在用(路由存活,可访问)。
	StatusActive = "active"
	// StatusReclaimed 表示已回收(PR 关闭/合并后删路由并标记)。
	StatusReclaimed = "reclaimed"
)

// 领域错误(错误体绝不含敏感信息)。
var (
	// ErrNotFound 表示预览环境不存在。
	ErrNotFound = errors.New("previewenv: not found")
	// ErrInvalidProject 表示未指定项目。
	ErrInvalidProject = errors.New("previewenv: project id must not be empty")
	// ErrInvalidPR 表示 PR 号非法(须 ≥1)。
	ErrInvalidPR = errors.New("previewenv: pr number must be positive")
	// ErrConfigMissingProvider 表示开启预览却未指定 DNS 提供商。
	ErrConfigMissingProvider = errors.New("previewenv: dns provider required when preview enabled")
	// ErrConfigInvalidBaseDomain 表示开启预览却未给合法根域。
	ErrConfigInvalidBaseDomain = errors.New("previewenv: invalid base domain")
)

// PreviewEnv 是一条 PR 预览环境的领域模型(冻结契约)。
type PreviewEnv struct {
	ID          string
	ProjectID   string
	PipelineID  string
	PRNumber    int
	Branch      string
	ServerID    string
	RouteID     string
	Subdomain   string
	Status      string // active | reclaimed
	CreatedAt   time.Time
	ReclaimedAt *time.Time // nil = 未回收
}

// Config 是项目级预览配置(每项目一行;冻结契约)。
//   - Enabled:是否对该项目启用 PR 预览(默认 false → provision no-op)。
//   - DNSProviderID:分配预览子域名走的 DNS 提供商(R3)。
//   - BaseDomain:在其下分配预览子域名(如 preview.example.com)。
type Config struct {
	ProjectID     string
	Enabled       bool
	DNSProviderID string
	BaseDomain    string
}

// Service 定义预览环境领域对外接口(冻结契约;httpapi 消费)。
type Service interface {
	// List 返回某项目的全部预览环境(创建时间倒序)。projectID 为空 → 全部。
	List(ctx context.Context, projectID string) ([]PreviewEnv, error)
	// Get 返回单个预览环境;不存在 → ErrNotFound。
	Get(ctx context.Context, id string) (*PreviewEnv, error)
	// GetConfig 返回某项目的预览配置(无配置 → 零值 Config{ProjectID, Enabled:false})。
	GetConfig(ctx context.Context, projectID string) (*Config, error)
	// SetConfig upsert 某项目的预览配置(校验:开启时须给 DNS 提供商 + 合法根域)。
	SetConfig(ctx context.Context, in Config) (*Config, error)
	// Reclaim 回收某项目某 PR 的预览环境:删反代路由 + 标记 reclaimed +(best-effort)删 DNS 记录。
	// 无 active 环境 → ErrNotFound(调用方据情况静默)。
	Reclaim(ctx context.Context, projectID string, prNumber int) error
}
