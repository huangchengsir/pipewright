// Package proxy 是「自动 HTTPS + 域名反向代理」(R1)的领域层。
//
// 核心理念:反代「在哪有容器就在哪」——每个目标主机一个 Caddy 容器。Pipewright 是中枢,
// 不自己当反代,而是复用现成的 target.Service(SSH + docker,与容器管理同一套零侵入手法)在
// 目标主机上编排 Caddy:从 DB 渲染 Caddyfile → docker cp 进容器 → caddy reload(优雅热加载)。
// Caddy 见到带域名的站点块即自动经 Let's Encrypt(HTTP-01)签发并续期证书,零额外指令。
//
// 设计纪律:
//   - 路由 CRUD 经参数化 SQL;domain 全局唯一(同域名只能绑一条路由)。
//   - 一切 docker 命令经 target.Exec 以 array 形式执行(AC-SEC-02 不拼 shell,无注入面)。
//   - 失败给**人话**原因(域名非法 / DNS 未指向 / 80 不可达 / LE 失败),绝不静默吞,
//     LE 不无限重试(避免触发速率限制),修好后用户手动「刷新」再签。
//   - 证书状态经 TLS 握手探测域名:443 回读(贴近「用户真能不能访问」),探测器经接口注入便于单测。
package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/target"
)

// tls_mode / cert_status 枚举(DB 存小写字串;JSON 同名)。
const (
	tlsModeAuto = "auto"

	// CertStatusPending 表示证书尚未签发成功(刚建 / 仍在 LE 流程 / 临时证书)。
	CertStatusPending = "pending"
	// CertStatusIssued 表示已握手到受信任的有效证书。
	CertStatusIssued = "issued"
	// CertStatusFailed 表示探测失败(拒连 / 超时 / DNS 未指向 / 80 不可达)。
	CertStatusFailed = "failed"
)

// 领域错误(人话由 httpapi 层映射状态码;错误体绝不含敏感信息)。
var (
	// ErrNotFound 表示路由不存在。
	ErrNotFound = errors.New("proxy: route not found")
	// ErrDomainTaken 表示该域名已被其它路由占用(domain 唯一)。
	ErrDomainTaken = errors.New("proxy: domain already in use")
	// ErrInvalidDomain 表示域名格式非法。
	ErrInvalidDomain = errors.New("proxy: invalid domain")
	// ErrEmptyServerID 表示未指定目标主机。
	ErrEmptyServerID = errors.New("proxy: server id must not be empty")
	// ErrEmptyContainer 表示未指定上游容器。
	ErrEmptyContainer = errors.New("proxy: upstream container must not be empty")
	// ErrInvalidContainer 表示上游容器名含非法字符(防 flag/shell 注入)。
	ErrInvalidContainer = errors.New("proxy: invalid upstream container")
	// ErrInvalidPort 表示上游端口非法(非 1..65535)。
	ErrInvalidPort = errors.New("proxy: upstream port must be in 1..65535")

	// ErrPortConflict 表示目标主机 80/443 被占用,无法起 Caddy(LE 校验需 80)。
	ErrPortConflict = errors.New("proxy: 80/443 端口被占用,无法启动反代(Let's Encrypt 校验需要 80)")
	// ErrCaddyStart 表示 Caddy 容器启动失败。
	ErrCaddyStart = errors.New("proxy: 启动反代容器失败")
	// ErrUpstreamConnect 表示把上游容器接入共享网络失败。
	ErrUpstreamConnect = errors.New("proxy: 上游容器接入反代网络失败")
	// ErrApply 表示渲染/下发/reload Caddy 配置失败。
	ErrApply = errors.New("proxy: 应用反代配置失败")
)

// Route 是一条反代路由领域模型(冻结契约)。
type Route struct {
	ID                string
	ServerID          string
	Domain            string
	UpstreamContainer string
	UpstreamPort      int
	TLSMode           string // "auto"
	Enabled           bool
	CertStatus        string // "pending" | "issued" | "failed"
	CertDetail        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CreateInput 是创建路由的入参。
type CreateInput struct {
	ServerID          string
	Domain            string
	UpstreamContainer string
	UpstreamPort      int
}

// Service 定义反代路由领域对外接口(冻结契约;httpapi 消费)。
type Service interface {
	// List 返回某主机的全部路由(serverID 为空则全部),创建时间倒序。
	List(ctx context.Context, serverID string) ([]Route, error)
	// Create 校验入参 → 落库 → ensureCaddy → connectUpstream → apply(reload)。
	Create(ctx context.Context, in CreateInput) (*Route, error)
	// Delete 删路由 → apply(reload,把它从反代摘除)。
	Delete(ctx context.Context, id string) error
	// SetEnabled 启停某路由 → apply(reload)。
	SetEnabled(ctx context.Context, id string, on bool) error
	// RefreshStatus 经 TLS 握手探测域名:443 回写 cert_status + cert_detail。
	RefreshStatus(ctx context.Context, id string) (*Route, error)
}

// certProber 抽象「对 域名:443 做 TLS 握手并回报证书状态」的能力(注入便于单测,不触真网络)。
type certProber interface {
	Probe(ctx context.Context, domain string) ProbeResult
}

// ProbeResult 是一次证书探测结果。
type ProbeResult struct {
	// Status ∈ issued | pending | failed。
	Status string
	// Detail 是人话详情(到期时间 / 失败原因);绝不含敏感信息。
	Detail string
}

// service 是 store + target + prober 支撑的 Service 实现。
type service struct {
	store  *Store
	tg     target.Service
	prober certProber
}

// New 构造 Service。tg 复用已装配的 target.Service(SSH + docker);prober 默认走真实 TLS 握手。
// 不在此做任何重活(无 init 副作用)。
func New(db *sql.DB, tg target.Service) Service {
	return &service{store: NewStore(db), tg: tg, prober: tlsProber{}}
}

func (s *service) List(ctx context.Context, serverID string) ([]Route, error) {
	return s.store.list(ctx, serverID)
}

func (s *service) Create(ctx context.Context, in CreateInput) (*Route, error) {
	in.ServerID = strings.TrimSpace(in.ServerID)
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.UpstreamContainer = strings.TrimSpace(in.UpstreamContainer)
	if err := validateCreate(in); err != nil {
		return nil, err
	}

	r := newRoute(in)
	// 1) 先落库(domain 唯一冲突 → ErrDomainTaken,先于触网,避免无谓 docker 操作)。
	if err := s.store.insert(ctx, r); err != nil {
		return nil, err
	}
	// 2) 编排:保证 Caddy 就绪 → 上游接入共享网络 → 渲染 + 下发 + reload。
	//    编排失败回滚刚插入的路由(避免留下「库里有、反代上没有」的孤儿)。
	if err := s.ensureAndApply(ctx, r.ServerID, in.UpstreamContainer); err != nil {
		_ = s.store.del(ctx, r.ID)
		return nil, err
	}
	return s.store.get(ctx, r.ID)
}

func (s *service) Delete(ctx context.Context, id string) error {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.del(ctx, id); err != nil {
		return err
	}
	// 把它从反代摘除:重渲染该主机剩余 enabled 路由并 reload(best-effort:reload 失败仍算删成功,
	// 库已删,下次任何 apply 会收敛)。删卷不动(证书持久,避免重签触发 LE 限速)。
	if applyErr := s.apply(ctx, r.ServerID); applyErr != nil {
		return applyErr
	}
	return nil
}

func (s *service) SetEnabled(ctx context.Context, id string, on bool) error {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.setEnabled(ctx, id, on); err != nil {
		return err
	}
	// 启用时确保上游已接入网络;停用时无需(渲染时自然摘除)。
	if on {
		if err := s.ensureAndApply(ctx, r.ServerID, r.UpstreamContainer); err != nil {
			return err
		}
		return nil
	}
	return s.apply(ctx, r.ServerID)
}

func (s *service) RefreshStatus(ctx context.Context, id string) (*Route, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return nil, err
	}
	res := s.prober.Probe(ctx, r.Domain)
	if err := s.store.setCertStatus(ctx, id, res.Status, res.Detail); err != nil {
		return nil, err
	}
	return s.store.get(ctx, id)
}

// ensureAndApply 是 Create/启用 路径的编排:保证 Caddy 就绪 → 接入上游 → apply。
func (s *service) ensureAndApply(ctx context.Context, serverID, container string) error {
	if err := ensureCaddy(ctx, s.tg, serverID); err != nil {
		return err
	}
	if err := connectUpstream(ctx, s.tg, serverID, container); err != nil {
		return err
	}
	return s.apply(ctx, serverID)
}

// apply 渲染该主机所有 enabled 路由 → 下发 Caddyfile → reload。
func (s *service) apply(ctx context.Context, serverID string) error {
	routes, err := s.store.listEnabledForServer(ctx, serverID)
	if err != nil {
		return err
	}
	return applyCaddyfile(ctx, s.tg, serverID, renderCaddyfile(routes))
}

// --- 校验 -------------------------------------------------------------------

// domainRe 校验 FQDN(每段字母数字/连字符,段间点分,顶级域 ≥2 字母)。不接受裸 IP / 端口。
var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// containerRe 校验上游容器名(docker 容器名/ID 允许字符;首字符非 `-` 防 flag 注入)。
var containerRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validateDomain 校验域名格式(FR-4:非法早拒,前端也挡一道)。
func validateDomain(domain string) error {
	if domain == "" || len(domain) > 253 {
		return ErrInvalidDomain
	}
	if !domainRe.MatchString(domain) {
		return ErrInvalidDomain
	}
	return nil
}

func validateCreate(in CreateInput) error {
	if in.ServerID == "" {
		return ErrEmptyServerID
	}
	if err := validateDomain(in.Domain); err != nil {
		return err
	}
	if in.UpstreamContainer == "" {
		return ErrEmptyContainer
	}
	if !containerRe.MatchString(in.UpstreamContainer) {
		return ErrInvalidContainer
	}
	if in.UpstreamPort < 1 || in.UpstreamPort > 65535 {
		return ErrInvalidPort
	}
	return nil
}

// --- 证书探测器 -------------------------------------------------------------

// tlsProber 是默认探测器:对 域名:443 做真实 TLS 握手,读证书 issuer/NotAfter。
//   - 握手成功且证书在有效期 → issued（detail 含 issuer + 到期）。
//   - 握手成功但证书已过期 / 尚未生效 → pending。
//   - 拒连 / 超时 / 解析失败 → failed（人话原因,区分 DNS 未解析 vs 连不上 443）。
type tlsProber struct{}

const proberTimeout = 8 * time.Second

func (tlsProber) Probe(ctx context.Context, domain string) ProbeResult {
	// 先看域名能否解析(区分「DNS 未指向」与「连不上 443」)。
	if _, err := net.DefaultResolver.LookupHost(ctx, domain); err != nil {
		return ProbeResult{Status: CertStatusFailed, Detail: "域名无法解析:请确认已为该域名添加指向本机的 A 记录"}
	}

	dialer := &net.Dialer{Timeout: proberTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(domain, "443"), &tls.Config{
		ServerName: domain,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		// 区分连不上(拒连/超时)与证书校验失败。证书校验类错误(LE 临时证 / 未签发完)= 仍在
		// 签发中 → pending;拒连/超时 = failed(端口或 DNS 问题)。
		if isCertVerifyErr(err) {
			return ProbeResult{Status: CertStatusPending, Detail: "证书尚未就绪(可能仍在签发中,稍后刷新)"}
		}
		return ProbeResult{Status: CertStatusFailed, Detail: "无法连接 443:请确认 80/443 可从公网访问、DNS 已指向本机"}
	}
	defer func() { _ = conn.Close() }()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return ProbeResult{Status: CertStatusPending, Detail: "未取得证书,稍后刷新"}
	}
	leaf := certs[0]
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return ProbeResult{Status: CertStatusPending, Detail: "证书不在有效期(可能仍在签发中)"}
	}
	issuer := strings.TrimSpace(leaf.Issuer.CommonName)
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	detail := fmt.Sprintf("证书有效,到期 %s", leaf.NotAfter.UTC().Format("2006-01-02"))
	if issuer != "" {
		detail = fmt.Sprintf("已由 %s 签发,到期 %s", issuer, leaf.NotAfter.UTC().Format("2006-01-02"))
	}
	return ProbeResult{Status: CertStatusIssued, Detail: detail}
}

// isCertVerifyErr 判定 TLS 握手错误是否为「证书校验失败」一类(连上了但证书暂不可信:LE 临时证 /
// 自签 / 尚未签发完)。这类报 pending(签发中);拒连/超时等连接级错误报 failed。
func isCertVerifyErr(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	var certVerify *tls.CertificateVerificationError
	return errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &invalidErr) ||
		errors.As(err, &certVerify)
}
