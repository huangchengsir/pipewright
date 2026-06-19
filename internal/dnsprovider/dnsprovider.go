// Package dnsprovider 是「DNS 提供商集成层」(R3 E3.1–E3.4)的领域层。
//
// 一个 Provider 按引用绑定一条 vault 凭据(API token / key):DB 中只存 credential_id,
// 绝无任何明文 token(与 target.Server 同纪律)。需要 token 时经 vault.Reveal 在进程内取明文,
// 用完即弃,绝不入库 / 日志 / 响应 / 错误体。
//
// 能力:
//   - DNSClient 抽象「为某域建/改 A 记录」「校验某 zone 可管理」(注入便于单测,不触真网络)。
//     Cloudflare / DNSPod / 阿里云 DNS 三家均经 net/http 直连各自 API 真实实现(无 SDK):
//     Cloudflare 用 v4 REST(Bearer token);DNSPod 用经典 dnsapi.cn form-POST(login_token=id,token);
//     阿里云用 RPC v1(HMAC-SHA1 签名,凭据 accessKeyId,accessKeySecret)。
//     三家的 DNS-01 通配符证书签发也都能用(走 Caddy 镜像内的 DNS 插件,与自动建 A 记录正交)。
//   - AllocateSubdomain:瞬时挑一个可读子域名 app-<6char>.<baseDomain> → 自动建 A 记录指向宿主机
//     公网 IP → 创建一条 DNS-01 的反代路由(证书即刻可签)。
//
// AC-SEC:写入解析的 IP 是宿主机的(由调用方传入,非用户自由文本);zone/name 服务端严格校验;
// 命令 / API 入参严格归一,杜绝注入。
package dnsprovider

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// 提供商类型枚举(DB 存小写字串;JSON 同名)。
const (
	// TypeCloudflare 是 Cloudflare DNS(自动 A 记录已真实实现)。
	TypeCloudflare = "cloudflare"
	// TypeDNSPod 是腾讯云 DNSPod(自动 A 记录已真实实现;凭据格式 "id,token")。
	TypeDNSPod = "dnspod"
	// TypeAliDNS 是阿里云 DNS(自动 A 记录已真实实现;凭据格式 "accessKeyId,accessKeySecret")。
	TypeAliDNS = "alidns"
)

// 领域错误(人话由 httpapi 层映射状态码;错误体绝不含敏感信息)。
var (
	// ErrNotFound 表示 DNS 提供商不存在。
	ErrNotFound = errors.New("dnsprovider: provider not found")
	// ErrInvalidType 表示提供商类型枚举非法。
	ErrInvalidType = errors.New("dnsprovider: invalid provider type")
	// ErrEmptyName 表示展示名为空。
	ErrEmptyName = errors.New("dnsprovider: name must not be empty")
	// ErrEmptyCredentialID 表示未选择凭据。
	ErrEmptyCredentialID = errors.New("dnsprovider: credential id must not be empty")
	// ErrInvalidBaseDomain 表示根域格式非法。
	ErrInvalidBaseDomain = errors.New("dnsprovider: invalid base domain")
	// ErrCredentialNotFound 表示引用的凭据不存在。
	ErrCredentialNotFound = errors.New("dnsprovider: referenced credential not found")
	// ErrVaultUnconfigured 表示保险库未配置 master key,无法取 DNS token。
	ErrVaultUnconfigured = errors.New("dnsprovider: vault unconfigured")

	// ErrProviderNotImplemented 表示该提供商的某能力(如自动建 A 记录)尚未实现。
	// 注意:DNS-01 通配符证书签发对三种提供商都可用(走 Caddy 镜像内 DNS 插件),不受此影响。
	ErrProviderNotImplemented = errors.New("dnsprovider: provider capability not implemented")
	// ErrInvalidCredential 表示保险库里存的凭据格式不符合该提供商约定(如 DNSPod 须 "id,token"、
	// 阿里云须 "accessKeyId,accessKeySecret")。错误体绝不含凭据任何片段。httpapi 层可映射 400。
	ErrInvalidCredential = errors.New("dnsprovider: invalid provider credential format")
	// ErrVerifyFailed 表示 zone 校验失败(token 无效 / 无该 zone 权限 / API 不可达)。
	ErrVerifyFailed = errors.New("dnsprovider: zone verification failed")
	// ErrEnsureRecord 表示建/改 A 记录失败。
	ErrEnsureRecord = errors.New("dnsprovider: ensure A record failed")
	// ErrAllocate 表示子域名分配失败(多次随机仍冲突 / 建记录失败 / 建路由失败)。
	ErrAllocate = errors.New("dnsprovider: allocate subdomain failed")
)

// Provider 是一个 DNS 提供商接入的领域模型(冻结契约)。绝不含 token 明文,只持 CredentialID 引用。
type Provider struct {
	ID           string
	Type         string // cloudflare | dnspod | alidns
	Name         string
	CredentialID string // 指向 vault 凭据(API token / key);明文绝不入此结构体
	BaseDomain   string // 该提供商托管的根域(如 example.com)
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateInput 是登记 DNS 提供商的入参。
type CreateInput struct {
	Type         string
	Name         string
	CredentialID string
	BaseDomain   string
}

// DNSClient 抽象一个具体提供商的 DNS 写/校验能力(注入便于单测,不触真网络)。
// EnsureARecord 建或就地 upsert 一条 A 记录(name → ip);VerifyZone 校验 token 可管理该 zone。
type DNSClient interface {
	// EnsureARecord 为 zone(根域)下的 name(FQDN)建/改 A 记录指向 ip(幂等 upsert)。
	EnsureARecord(ctx context.Context, zone, name, ip string) error
	// VerifyZone 校验当前凭据可管理 zone(根域),失败返回人话错误(绝不含 token)。
	VerifyZone(ctx context.Context, zone string) error
}

// Service 定义 DNS 提供商领域对外接口(冻结契约;httpapi 消费)。
type Service interface {
	// List 返回全部 DNS 提供商(创建时间倒序;无 token)。
	List(ctx context.Context) ([]Provider, error)
	// Get 返回单个提供商(无 token);不存在 → ErrNotFound。
	Get(ctx context.Context, id string) (*Provider, error)
	// Create 校验入参(含凭据存在性)→ 落库;返回提供商视图(无 token)。
	Create(ctx context.Context, in CreateInput) (*Provider, error)
	// Delete 删除提供商;不存在 → ErrNotFound。
	Delete(ctx context.Context, id string) error
	// Verify 取该提供商凭据明文 → 经 DNSClient 校验 zone 可管理。token 用完即弃,绝不外泄。
	Verify(ctx context.Context, id string) error
	// AllocateSubdomain 瞬时分配子域名(E3.3 + E3.4):挑 app-<6char>.<baseDomain> → 建 A 记录
	// 指向 hostIP → 创建一条 DNS-01 反代路由(证书即刻可签)。返回新建的反代路由。
	AllocateSubdomain(ctx context.Context, in AllocateInput) (*RouteRef, error)

	// AllocateFQDN 为一个**指定的** FQDN(in.Subdomain,须落在提供商 base_domain 下)建 A 记录指向
	// hostIP → 创建一条 DNS-01 反代路由(R4 E4.1 预览环境用:子域名确定而非随机)。返回新建路由引用。
	// in.Subdomain 缺省/不在 base_domain 下 → ErrAllocate。
	AllocateFQDN(ctx context.Context, in AllocateInput) (*RouteRef, error)

	// ProviderType 返回某提供商的类型(供 proxy 校验 DNS-01 引用,不取 token)。
	// 不存在 → ok=false。供 proxy.DNSResolver 适配。
	ProviderType(ctx context.Context, providerID string) (providerType string, ok bool, err error)

	// ResolveToken 取某提供商的 (类型, token 明文)(供 proxy 在 apply 时渲染 DNS-01)。
	// token 仅供调用方即时注入 0600 临时 Caddyfile,绝不日志/回库/回 API。
	// 不存在 → ok=false;vault 未配/凭据缺失 → err。供 proxy.DNSResolver 适配。
	ResolveToken(ctx context.Context, providerID string) (providerType, token string, ok bool, err error)
}

// AllocateInput 是瞬时子域名分配的入参。
type AllocateInput struct {
	ProviderID        string
	ServerID          string
	UpstreamContainer string
	UpstreamPort      int
	// HostIP 是宿主机公网 IP(由调用方/上层据 server 解析得出,非用户自由文本)。写入 A 记录指向它。
	HostIP string
	// Subdomain 是 AllocateFQDN 用的**指定** FQDN(如 pr-12-abcd.preview.example.com);
	// AllocateSubdomain 不用此字段(它随机挑后缀)。须落在提供商 base_domain 下(后缀匹配校验)。
	Subdomain string
}

// RouteRef 是 AllocateSubdomain 创建出的反代路由引用(避免本包 import proxy 形成环;
// 由上层用其 RouteID 取完整 proxy.Route DTO)。
type RouteRef struct {
	RouteID    string
	Domain     string
	ProviderID string
}

// baseDomainRe 校验根域 FQDN(每段字母数字/连字符,段间点分,顶级域 ≥2 字母)。不接受裸 IP / 端口 / 通配符。
var baseDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// ValidType 报告提供商类型是否为受支持枚举(供 proxy 校验 DNS-01 引用时复用)。
func ValidType(t string) bool {
	switch t {
	case TypeCloudflare, TypeDNSPod, TypeAliDNS:
		return true
	default:
		return false
	}
}

// validateCreate 校验登记入参。
func validateCreate(in CreateInput) error {
	if !ValidType(in.Type) {
		return ErrInvalidType
	}
	if strings.TrimSpace(in.Name) == "" {
		return ErrEmptyName
	}
	if strings.TrimSpace(in.CredentialID) == "" {
		return ErrEmptyCredentialID
	}
	if !ValidBaseDomain(in.BaseDomain) {
		return ErrInvalidBaseDomain
	}
	return nil
}

// ValidBaseDomain 报告 s 是否为合法根域 FQDN。
func ValidBaseDomain(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s != "" && len(s) <= 253 && baseDomainRe.MatchString(s)
}
