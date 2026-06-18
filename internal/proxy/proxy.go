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
	"golang.org/x/crypto/bcrypt"
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

	// ErrInvalidAlias 表示别名域名格式非法(与主域名同一 FQDN 校验)。
	ErrInvalidAlias = errors.New("proxy: invalid alias domain")
	// ErrInvalidCIDR 表示 IP 白名单/黑名单中某项不是合法 CIDR / IP。
	ErrInvalidCIDR = errors.New("proxy: invalid CIDR in ip allow/deny list")
	// ErrInvalidRedirect 表示重定向规则非法(路径含危险字符 / 状态码非 301|302|307|308)。
	ErrInvalidRedirect = errors.New("proxy: invalid redirect rule")
	// ErrInvalidBasicAuthUser 表示 Basic Auth 用户名含非法字符(防破坏 Caddyfile)。
	ErrInvalidBasicAuthUser = errors.New("proxy: invalid basic auth user")
	// ErrBcrypt 表示 Basic Auth 口令哈希失败。
	ErrBcrypt = errors.New("proxy: hash basic auth password failed")
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
	Config            RouteConfig // R2:高级配置(别名 / 访问控制 / 头 / 重定向);R1 老路由为零值。
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Redirect 是一条自定义重定向规则(渲染为 Caddy `redir from to status`)。冻结契约。
type Redirect struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status int    `json:"status"` // 301|302|307|308;默认 308
}

// RouteConfig 是一条路由的「高级配置」(R2;持久化为 config 列 JSON)。冻结契约。
// 注意:BasicAuthHash 的 JSON tag 为 `-`,绝不出现在 API DTO;但**持久化**到 DB 时必须保留它,
// 故 store 层用独立的存储表示(storedConfig)序列化,不能直接复用本结构体写库。
type RouteConfig struct {
	Aliases         []string   `json:"aliases,omitempty"`       // 同站点块额外服务的 FQDN(如 www)
	ForceHTTPS      bool       `json:"forceHttps"`              // MVP 文档化 no-op:Caddy 对带域名站点默认即强制 HTTP→HTTPS,故此开关恒为「已强制」语义;字段保留供前端展示/未来 auto_https off 扩展
	HSTS            bool       `json:"hsts"`                    //
	SecurityHeaders bool       `json:"securityHeaders"`         //
	Compression     bool       `json:"compression"`             //
	BasicAuthUser   string     `json:"basicAuthUser,omitempty"` //
	BasicAuthHash   string     `json:"-"`                       // bcrypt;绝不序列化给客户端
	IPAllow         []string   `json:"ipAllow,omitempty"`       // CIDR(仅这些可访问)
	IPDeny          []string   `json:"ipDeny,omitempty"`        // CIDR(这些被拒)
	Redirects       []Redirect `json:"redirects,omitempty"`     //
}

// RouteWithServer 是一条路由 + 其所属目标主机展示名(证书总览大盘用)。
type RouteWithServer struct {
	Route
	ServerName string
}

// CreateInput 是创建路由的入参。
type CreateInput struct {
	ServerID          string
	Domain            string
	UpstreamContainer string
	UpstreamPort      int
}

// UpdateInput 是更新路由(R2)的入参。冻结契约。
type UpdateInput struct {
	UpstreamContainer string      // 空 = 保持不变
	UpstreamPort      int         // 0 = 保持不变
	Config            RouteConfig //
	BasicAuthPassword string      // 明文;非空 → bcrypt 哈希入 Config.BasicAuthHash;若 BasicAuthUser=="" → 清空认证
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
	// Update 校验入参 → 持久化高级配置(含 bcrypt 口令)→ apply(reload)。
	Update(ctx context.Context, id string, in UpdateInput) (*Route, error)
	// Overview 返回全部路由 + 所属主机展示名(跨主机证书总览大盘用)。
	Overview(ctx context.Context) ([]RouteWithServer, error)
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

// Update 校验入参 → 归一化 + bcrypt 口令 → 持久化高级配置 → apply(reload)。
// upstream 容器/端口为空/0 时保持原值;BasicAuthUser=="" 时清空认证(忽略口令)。
func (s *service) Update(ctx context.Context, id string, in UpdateInput) (*Route, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return nil, err
	}

	// 1) upstream 增量更新(空 = 保持)。
	container := r.UpstreamContainer
	if c := strings.TrimSpace(in.UpstreamContainer); c != "" {
		container = c
	}
	port := r.UpstreamPort
	if in.UpstreamPort != 0 {
		port = in.UpstreamPort
	}

	// 2) 归一化高级配置(域名小写去空白 / 去重 / 状态码默认)。
	cfg := normalizeConfig(in.Config)

	// 3) Basic Auth:用户名为空 → 清认证;否则若给了明文口令则 bcrypt 哈希,未给则沿用旧哈希。
	if cfg.BasicAuthUser == "" {
		cfg.BasicAuthUser = ""
		cfg.BasicAuthHash = ""
	} else if pw := in.BasicAuthPassword; pw != "" {
		hash, herr := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if herr != nil {
			return nil, fmt.Errorf("%w:%v", ErrBcrypt, herr)
		}
		cfg.BasicAuthHash = string(hash)
	} else {
		cfg.BasicAuthHash = r.Config.BasicAuthHash // 沿用旧哈希(仅改其它配置时不强制重设口令)
	}

	// 4) 严格校验(注入面全在此关闭:别名 FQDN / CIDR / 重定向路径 / basic auth 用户名)。
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if !containerRe.MatchString(container) {
		return nil, ErrInvalidContainer
	}
	if port < 1 || port > 65535 {
		return nil, ErrInvalidPort
	}

	// 5) 持久化(config JSON 含 bcrypt 哈希)。
	if err := s.store.updateConfig(ctx, id, container, port, cfg); err != nil {
		return nil, err
	}

	// 6) 重渲染并下发(只对 enabled 路由生效;停用路由改配置不触网 apply 也无妨,仍持久化)。
	if r.Enabled {
		if err := s.apply(ctx, r.ServerID); err != nil {
			return nil, err
		}
	}
	return s.store.get(ctx, id)
}

// Overview 列全部路由,并按 server_id 关联主机展示名(主机查不到则回退 server_id)。
func (s *service) Overview(ctx context.Context) ([]RouteWithServer, error) {
	routes, err := s.store.list(ctx, "")
	if err != nil {
		return nil, err
	}
	// 取一次主机清单建 id→name 映射(best-effort:取不到则名字回退 server_id,不报错)。
	names := map[string]string{}
	if s.tg != nil {
		if servers, lerr := s.tg.List(ctx); lerr == nil {
			for _, sv := range servers {
				if sv != nil {
					names[sv.ID] = sv.Name
				}
			}
		}
	}
	out := make([]RouteWithServer, 0, len(routes))
	for _, rt := range routes {
		name := names[rt.ServerID]
		if name == "" {
			name = rt.ServerID
		}
		out = append(out, RouteWithServer{Route: rt, ServerName: name})
	}
	return out, nil
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

// redirectPathRe 限定重定向 from/to 的字符集:斜杠路径 / 简单 URL,绝无空白、花括号、引号、换行
// (这些会破坏 Caddyfile 语法或注入指令)。允许字母数字与 URL 常见安全标点。
var redirectPathRe = regexp.MustCompile(`^[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+$`)

// basicAuthUserRe 限定 Basic Auth 用户名字符集(无空白/花括号/引号/换行,防破坏 basic_auth 块)。
var basicAuthUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,128}$`)

// normalizeConfig 归一化高级配置:别名小写去空白去重、CIDR/IP 去空白、用户名去空白、重定向状态码默认 308。
// 不做合法性判定(交 validateConfig);只整形,使渲染稳定、校验可靠。
func normalizeConfig(in RouteConfig) RouteConfig {
	out := in
	out.BasicAuthUser = strings.TrimSpace(in.BasicAuthUser)

	out.Aliases = dedupLower(in.Aliases)
	out.IPAllow = trimNonEmpty(in.IPAllow)
	out.IPDeny = trimNonEmpty(in.IPDeny)

	reds := make([]Redirect, 0, len(in.Redirects))
	for _, rd := range in.Redirects {
		from := strings.TrimSpace(rd.From)
		to := strings.TrimSpace(rd.To)
		if from == "" && to == "" {
			continue
		}
		status := rd.Status
		if status == 0 {
			status = 308
		}
		reds = append(reds, Redirect{From: from, To: to, Status: status})
	}
	if len(reds) == 0 {
		reds = nil
	}
	out.Redirects = reds
	return out
}

// validateConfig 严格校验高级配置(关闭一切 Caddyfile 注入面)。
func validateConfig(cfg RouteConfig) error {
	for _, a := range cfg.Aliases {
		if validateDomain(a) != nil {
			return ErrInvalidAlias
		}
	}
	for _, c := range append(append([]string{}, cfg.IPAllow...), cfg.IPDeny...) {
		if !validCIDROrIP(c) {
			return ErrInvalidCIDR
		}
	}
	for _, rd := range cfg.Redirects {
		if rd.From == "" || rd.To == "" {
			return ErrInvalidRedirect
		}
		if !redirectPathRe.MatchString(rd.From) || !redirectPathRe.MatchString(rd.To) {
			return ErrInvalidRedirect
		}
		switch rd.Status {
		case 301, 302, 307, 308:
		default:
			return ErrInvalidRedirect
		}
	}
	if cfg.BasicAuthUser != "" && !basicAuthUserRe.MatchString(cfg.BasicAuthUser) {
		return ErrInvalidBasicAuthUser
	}
	return nil
}

// validCIDROrIP 判定 s 是合法 CIDR(net.ParseCIDR)或单个 IP(按 /32、/128 处理)。
func validCIDROrIP(s string) bool {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}
	return net.ParseIP(s) != nil
}

// dedupLower 把域名小写去空白并去重(保持首次出现顺序);空项丢弃。
func dedupLower(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// trimNonEmpty 去空白并丢弃空项(不改顺序、不去重)。
func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
