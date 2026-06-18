package dnsprovider

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/huangchengsir/pipewright/internal/vault"
)

// vaultReader 抽象「按 id 取凭据明文(不刷新 last_used_at)+ 校验存在」的最小能力(注入便于单测)。
// 复用 vault.Vault 的 Reveal/Exists;本包只读取 DNS token 明文,绝不入库/日志/响应。
type vaultReader interface {
	Reveal(id string) (string, error)
	Exists(id string) (bool, error)
}

// RouteCreator 抽象「创建一条 DNS-01 反代路由」的能力(由上层用 proxy.Service 适配注入,避免本包
// import proxy 形成环)。实现须落库一条带 DNS 提供商引用的路由并完成 Caddy 编排,返回 routeID。
type RouteCreator interface {
	// CreateDNS01Route 创建一条 DNS-01 反代路由(域名 + DNS 提供商引用 + 上游)。返回新路由 id。
	CreateDNS01Route(ctx context.Context, in CreateDNS01RouteInput) (routeID string, err error)
}

// CreateDNS01RouteInput 是创建 DNS-01 反代路由的入参(冻结契约,proxy 适配器消费)。
type CreateDNS01RouteInput struct {
	ServerID          string
	Domain            string
	UpstreamContainer string
	UpstreamPort      int
	DNSProviderID     string
}

// dialFactory 据提供商类型 + token 造一个 DNSClient(注入便于单测;生产用 prodDialFactory)。
type dialFactory func(providerType, token string) DNSClient

// prodDialFactory 是生产工厂:真实 net/http 客户端(Cloudflare 真连,其余桩)。
func prodDialFactory(providerType, token string) DNSClient {
	return newDNSClient(providerType, token, nil, "")
}

// service 是 store + vault + routeCreator + dialFactory 支撑的 Service 实现。
type service struct {
	store   *Store
	vault   vaultReader
	routes  RouteCreator
	dial    dialFactory
	randGen func(n int) (string, error) // 随机子域名后缀生成器(注入便于单测;默认 crypto/rand)
}

// New 构造 Service。
//   - db:参数化 SQL 触库。
//   - v:凭据保险库(取 DNS token 明文);nil/未配置时 Verify/Allocate 返回 ErrVaultUnconfigured。
//   - routes:DNS-01 反代路由创建器(proxy 适配器);nil 时 AllocateSubdomain 返回 ErrAllocate。
//
// 不在此做任何重活(无 init 副作用)。
func New(db *sql.DB, v vault.Vault, routes RouteCreator) Service {
	var vr vaultReader
	if v != nil {
		vr = v
	}
	return &service{
		store:   NewStore(db),
		vault:   vr,
		routes:  routes,
		dial:    prodDialFactory,
		randGen: randSuffix,
	}
}

func (s *service) List(ctx context.Context) ([]Provider, error) { return s.store.list(ctx) }

func (s *service) Get(ctx context.Context, id string) (*Provider, error) { return s.store.get(ctx, id) }

func (s *service) Create(ctx context.Context, in CreateInput) (*Provider, error) {
	in.Type = strings.ToLower(strings.TrimSpace(in.Type))
	in.Name = strings.TrimSpace(in.Name)
	in.CredentialID = strings.TrimSpace(in.CredentialID)
	in.BaseDomain = strings.ToLower(strings.TrimSpace(in.BaseDomain))
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	// 校验凭据存在(避免悬挂引用);vault 未配置时仍允许登记(凭据存在性在 Verify/Allocate 时再校）。
	if s.vault != nil {
		ok, err := s.vault.Exists(in.CredentialID)
		if err != nil {
			if !errors.Is(err, vault.ErrVaultUnconfigured) {
				return nil, fmt.Errorf("dnsprovider: check credential: %w", err)
			}
		} else if !ok {
			return nil, ErrCredentialNotFound
		}
	}
	p := newProvider(in)
	if err := s.store.insert(ctx, p); err != nil {
		return nil, err
	}
	return s.store.get(ctx, p.ID)
}

func (s *service) Delete(ctx context.Context, id string) error { return s.store.del(ctx, id) }

func (s *service) Verify(ctx context.Context, id string) error {
	p, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	client, err := s.clientFor(p)
	if err != nil {
		return err
	}
	if err := client.VerifyZone(ctx, p.BaseDomain); err != nil {
		return err
	}
	return nil
}

// ProviderType 返回某提供商类型(不取 token);不存在 → ok=false。
func (s *service) ProviderType(ctx context.Context, providerID string) (string, bool, error) {
	p, err := s.store.get(ctx, providerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return p.Type, true, nil
}

// ResolveToken 取某提供商 (类型, token 明文)(供 proxy apply 渲染 DNS-01)。token 用完即弃。
func (s *service) ResolveToken(ctx context.Context, providerID string) (string, string, bool, error) {
	p, err := s.store.get(ctx, providerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	if s.vault == nil {
		return "", "", false, ErrVaultUnconfigured
	}
	token, err := s.vault.Reveal(p.CredentialID)
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrVaultUnconfigured):
			return "", "", false, ErrVaultUnconfigured
		case errors.Is(err, vault.ErrNotFound):
			return "", "", false, ErrCredentialNotFound
		default:
			return "", "", false, ErrVerifyFailed
		}
	}
	return p.Type, token, true, nil
}

// clientFor 取该提供商凭据明文 → 造对应 DNSClient。token 明文仅进程内,调用方用完即弃。
func (s *service) clientFor(p *Provider) (DNSClient, error) {
	if s.vault == nil {
		return nil, ErrVaultUnconfigured
	}
	token, err := s.vault.Reveal(p.CredentialID)
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrVaultUnconfigured):
			return nil, ErrVaultUnconfigured
		case errors.Is(err, vault.ErrNotFound):
			return nil, ErrCredentialNotFound
		default:
			// 解密等内部错误:不泄漏细节。
			return nil, ErrVerifyFailed
		}
	}
	client := s.dial(p.Type, token)
	// 显式清本地 token 引用(client 已持有副本用于建连)。
	token = "" //nolint:ineffassign // 安全归零意图
	_ = token
	return client, nil
}

// AllocateSubdomain 瞬时分配子域名(E3.3 + E3.4):
//  1. 取提供商 + 校验入参(上游容器/端口、宿主机 IP)。
//  2. crypto/rand 挑可读后缀,组成 app-<6char>.<baseDomain>;若与已建路由域名冲突则重试(有限次)。
//  3. 经 DNSClient 建 A 记录指向 hostIP(Cloudflare 真实;其余桩返回未实现)。
//  4. 经 RouteCreator 创建一条 DNS-01 反代路由(证书即刻可签)。
func (s *service) AllocateSubdomain(ctx context.Context, in AllocateInput) (*RouteRef, error) {
	if s.routes == nil {
		return nil, fmt.Errorf("%w:路由创建器未装配", ErrAllocate)
	}
	p, err := s.store.get(ctx, in.ProviderID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.ServerID) == "" {
		return nil, ErrAllocate
	}
	if strings.TrimSpace(in.UpstreamContainer) == "" {
		return nil, ErrAllocate
	}
	if in.UpstreamPort < 1 || in.UpstreamPort > 65535 {
		return nil, ErrAllocate
	}
	if !validIPv4(in.HostIP) {
		// 宿主机 IP 必须是合法 IPv4(由上层据 server.Host 解析得出,非用户自由文本)。
		return nil, fmt.Errorf("%w:宿主机 IP 非法", ErrAllocate)
	}

	client, err := s.clientFor(p)
	if err != nil {
		return nil, err
	}

	// 有限次随机挑子域名,避开与已建路由域名冲突(由 RouteCreator 的 domain 唯一约束兜底)。
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		suffix, gerr := s.randGen(6)
		if gerr != nil {
			return nil, fmt.Errorf("%w:随机子域名生成失败", ErrAllocate)
		}
		domain := "app-" + suffix + "." + p.BaseDomain

		// 建 A 记录指向宿主机 IP(幂等)。Cloudflare 真实;DNSPod/alidns 桩返回未实现 → 直接上抛。
		if err := client.EnsureARecord(ctx, p.BaseDomain, domain, in.HostIP); err != nil {
			if errors.Is(err, ErrProviderNotImplemented) {
				return nil, err
			}
			lastErr = err
			continue
		}

		// 创建 DNS-01 反代路由。domain 唯一冲突(极小概率随机撞车)→ 重试下一个后缀。
		routeID, rerr := s.routes.CreateDNS01Route(ctx, CreateDNS01RouteInput{
			ServerID:          in.ServerID,
			Domain:            domain,
			UpstreamContainer: in.UpstreamContainer,
			UpstreamPort:      in.UpstreamPort,
			DNSProviderID:     p.ID,
		})
		if rerr != nil {
			if isDomainCollision(rerr) {
				lastErr = rerr
				continue
			}
			return nil, rerr
		}
		return &RouteRef{RouteID: routeID, Domain: domain, ProviderID: p.ID}, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w:多次尝试仍失败", ErrAllocate)
	}
	return nil, ErrAllocate
}

// AllocateFQDN 为一个**指定的** FQDN 建 A 记录指向 hostIP → 创建一条 DNS-01 反代路由(R4 E4.1)。
// 与 AllocateSubdomain 共享校验/建记录/建路由逻辑,但子域名由调用方给定(不随机挑),用于
// 幂等可预测的预览域名 pr-<n>-<proj>.base。
//   - in.Subdomain 必须非空且落在提供商 base_domain 下(后缀 .base 校验);否则 ErrAllocate。
//   - 域名已占用(同 PR 重复分配且未先回收旧路由)→ 透传 proxy 的 domain-taken 错误,供上层处置。
func (s *service) AllocateFQDN(ctx context.Context, in AllocateInput) (*RouteRef, error) {
	if s.routes == nil {
		return nil, fmt.Errorf("%w:路由创建器未装配", ErrAllocate)
	}
	p, err := s.store.get(ctx, in.ProviderID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.ServerID) == "" || strings.TrimSpace(in.UpstreamContainer) == "" {
		return nil, ErrAllocate
	}
	if in.UpstreamPort < 1 || in.UpstreamPort > 65535 {
		return nil, ErrAllocate
	}
	if !validIPv4(in.HostIP) {
		return nil, fmt.Errorf("%w:宿主机 IP 非法", ErrAllocate)
	}
	domain := strings.ToLower(strings.TrimSpace(in.Subdomain))
	// 必须落在该提供商的根域下(杜绝越权为任意域签证书 / 建记录)。
	if domain == "" || domain == p.BaseDomain || !strings.HasSuffix(domain, "."+p.BaseDomain) {
		return nil, fmt.Errorf("%w:子域名须落在提供商根域下", ErrAllocate)
	}

	client, cerr := s.clientFor(p)
	if cerr != nil {
		return nil, cerr
	}
	// 建 A 记录指向宿主机 IP(幂等)。DNSPod/alidns 桩返回未实现 → 直接上抛。
	if err := client.EnsureARecord(ctx, p.BaseDomain, domain, in.HostIP); err != nil {
		return nil, err
	}
	routeID, rerr := s.routes.CreateDNS01Route(ctx, CreateDNS01RouteInput{
		ServerID:          in.ServerID,
		Domain:            domain,
		UpstreamContainer: in.UpstreamContainer,
		UpstreamPort:      in.UpstreamPort,
		DNSProviderID:     p.ID,
	})
	if rerr != nil {
		return nil, rerr
	}
	return &RouteRef{RouteID: routeID, Domain: domain, ProviderID: p.ID}, nil
}

// isDomainCollision 判定路由创建错误是否为「域名已占用」(供子域名重试)。
// 用文本匹配(避免 import proxy 形成环;proxy.ErrDomainTaken 的文本含 "domain already in use")。
func isDomainCollision(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "domain already in use")
}

// randAlphabet 是可读子域名后缀字符集(去掉易混淆的 0/o/1/l/i,纯小写+数字)。
const randAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// randSuffix 经 crypto/rand 生成长度为 n 的可读后缀(无 math/rand、无 time 依赖,测试稳定)。
func randSuffix(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, x := range b {
		out[i] = randAlphabet[int(x)%len(randAlphabet)]
	}
	return string(out), nil
}

// validIPv4 报告 s 是否为合法 IPv4 地址(子域名 A 记录指向宿主机 IPv4)。
func validIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}
