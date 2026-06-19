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
	"encoding/pem"
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

	// UpstreamKindContainer 表示上游为目标主机上的 docker 容器(默认 / 向后兼容):
	// UpstreamContainer = 容器名,Caddy 把它接入共享网络并按容器名路由。空串归一化为本值。
	UpstreamKindContainer = "container"
	// UpstreamKindAddress 表示上游为任意地址(host:port):UpstreamContainer 存 HOST
	// (IP / FQDN / host.docker.internal),Caddy 直接 reverse_proxy 到 host:port,不接网络。
	UpstreamKindAddress = "address"

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
	// ErrInvalidUpstreamKind 表示上游类型非法(非 container | address)。
	ErrInvalidUpstreamKind = errors.New("proxy: invalid upstream kind")
	// ErrInvalidUpstreamHost 表示 address 上游的 HOST 非法(非 IP / FQDN / host.docker.internal)。
	ErrInvalidUpstreamHost = errors.New("proxy: invalid upstream host")

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

	// ErrWildcardNeedsDNS 表示通配符域名(*.example.com)未绑 DNS 提供商(HTTP-01 无法签泛域名,
	// 必须 DNS-01,故必须先绑一个 DNS 提供商)。
	ErrWildcardNeedsDNS = errors.New("proxy: 通配符域名(*.example.com)必须绑定 DNS 提供商以走 DNS-01 签发")
	// ErrInvalidDNSProvider 表示引用的 DNS 提供商不存在或类型非法。
	ErrInvalidDNSProvider = errors.New("proxy: invalid dns provider reference")
	// ErrInvalidPathRule 表示路径路由规则非法(路径未以 / 起 / 含危险字符 / 上游容器/端口非法)。
	ErrInvalidPathRule = errors.New("proxy: invalid path rule")

	// ErrInvalidUpstream 表示额外上游(负载均衡)条目非法(容器名/端口非法)。
	ErrInvalidUpstream = errors.New("proxy: invalid additional upstream")
	// ErrInvalidLBPolicy 表示负载均衡策略非白名单枚举。
	ErrInvalidLBPolicy = errors.New("proxy: invalid load balancing policy")
	// ErrInvalidHealthURI 表示健康检查路径非法(未以 / 起 / 含危险字符)。
	ErrInvalidHealthURI = errors.New("proxy: invalid health_uri")
	// ErrInvalidHealthInterval 表示健康检查间隔无法被 time.ParseDuration 解析。
	ErrInvalidHealthInterval = errors.New("proxy: invalid health_interval")
	// ErrInvalidTCPPassthrough 表示 TCP 透传配置非法(监听/上游端口越界 / 上游容器名非法)。
	ErrInvalidTCPPassthrough = errors.New("proxy: invalid tcp passthrough config")
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

// PathRule 是一条路径路由规则(R3 E3.5;渲染为 Caddy `handle <path> { reverse_proxy ... }`)。冻结契约。
// 例:Path=/api/* → 反代到 api_c:8080;未命中任何 PathRule 的请求落到默认 handle(主上游)。
type PathRule struct {
	Path              string `json:"path"`              // 须以 / 起;安全字符集(含 * glob)
	UpstreamContainer string `json:"upstreamContainer"` // 该路径段的上游容器名
	UpstreamPort      int    `json:"upstreamPort"`      // 该路径段的上游端口
}

// Upstream 是一条额外上游(R4 E4.2 负载均衡;渲染为 reverse_proxy 行内追加的 container:port)。冻结契约。
// 主上游仍是 Route.UpstreamContainer:UpstreamPort;这里列的是「主上游之外」的后端,与主上游一起
// 进同一个 reverse_proxy(由 LBPolicy 在它们之间分流)。
type Upstream struct {
	Container string `json:"container"` // 上游容器名(同主上游容器名校验)
	Port      int    `json:"port"`      // 上游端口(1..65535)
}

// 负载均衡策略枚举(白名单;DB 存小写串,渲染进 lb_policy)。
const (
	// LBPolicyFirst 表示「按顺序取第一个可用上游」(Caddy 默认;等价不设 lb_policy)。
	LBPolicyFirst = "first"
	// LBPolicyRoundRobin 表示轮询。
	LBPolicyRoundRobin = "round_robin"
	// LBPolicyLeastConn 表示最少连接。
	LBPolicyLeastConn = "least_conn"
	// LBPolicyRandom 表示随机。
	LBPolicyRandom = "random"
)

// TCPConfig 是一条 TCP 透传配置(R4 E4.3;经 Caddy 自构建镜像内的 caddy-l4 渲染进 layer4 全局块)。冻结契约。
// TCP 路由不是 HTTP 站点块:它在 Caddyfile 全局选项里声明一个 layer4 监听端口 → 代理到上游容器:端口。
type TCPConfig struct {
	ListenPort        int    `json:"listenPort"`        // Caddy 在反代主机上监听的 TCP 端口(1..65535)
	UpstreamContainer string `json:"upstreamContainer"` // 上游容器名(同主上游容器名校验)
	UpstreamPort      int    `json:"upstreamPort"`      // 上游端口(1..65535)
}

// RouteConfig 是一条路由的「高级配置」(R2;持久化为 config 列 JSON)。冻结契约。
// 注意:BasicAuthHash 的 JSON tag 为 `-`,绝不出现在 API DTO;但**持久化**到 DB 时必须保留它,
// 故 store 层用独立的存储表示(storedConfig)序列化,不能直接复用本结构体写库。
type RouteConfig struct {
	// UpstreamKind 是主上游的类型判别字段(放 config JSON,免迁移):
	//   - "container"(默认 / 空串归一化):Route.UpstreamContainer 为容器名,接入共享网络按名路由。
	//   - "address":Route.UpstreamContainer 存 HOST(IP/FQDN/host.docker.internal),直接反代,不接网络。
	// 仅作用于「主上游」;额外上游(Upstreams[])与路径/TCP 透传仍为 container 语义。
	UpstreamKind    string     `json:"upstreamKind,omitempty"`  // "" => "container"(向后兼容)
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
	DNSProviderID   string     `json:"dnsProviderId,omitempty"` // R3:绑定的 DNS 提供商(走 DNS-01;通配符必需)。空 = HTTP-01
	PathRules       []PathRule `json:"pathRules,omitempty"`     // R3 E3.5:路径路由(/api→A、/→B)

	// R4 E4.2:多上游负载均衡 + 健康检查故障转移。
	Upstreams      []Upstream `json:"upstreams,omitempty"`      // 主上游之外的额外后端(与主上游一起进同一 reverse_proxy)
	LBPolicy       string     `json:"lbPolicy,omitempty"`       // round_robin|least_conn|random|first;空/first = 不渲染 lb_policy
	HealthURI      string     `json:"healthUri,omitempty"`      // 主动健康检查路径(如 /healthz);空 = 不启用
	HealthInterval string     `json:"healthInterval,omitempty"` // 健康检查间隔(time.ParseDuration,如 10s);空 = Caddy 默认

	// R4 E4.3:WS / gRPC / TCP 透传。
	WebSocket      bool       `json:"websocket,omitempty"`      // WS 在 Caddy reverse_proxy 中天然透传;此 flag 仅为前端确认/文档化(no-op 渲染)
	GRPC           bool       `json:"grpc,omitempty"`           // gRPC:渲染 reverse_proxy { transport http { versions h2c 2 } }
	TCPPassthrough *TCPConfig `json:"tcpPassthrough,omitempty"` // TCP 透传(caddy-l4);非 HTTP 站点块,渲染进 layer4 全局块
}

// RouteWithServer 是一条路由 + 其所属目标主机展示名(证书总览大盘用)。
type RouteWithServer struct {
	Route
	ServerName string
}

// CaddyStatus 是某主机上反代(pipewright-caddy)运行环境的探测快照(冻结契约)。
// 供前端在「绑定首个域名(会隐式起 Caddy 容器)」前展示知情同意,以及提供「移除反代环境」入口。
type CaddyStatus struct {
	ServerID   string // 目标主机 id
	Installed  bool   // pipewright-caddy 容器是否存在(docker inspect 成功)
	Running    bool   // 容器是否正在运行
	Image      string // 容器镜像(docker inspect 读)
	Ports      string // 发布端口摘要,如 "80,443"(探测到的;读不到给 "",前端按 80/443 兜底文案)
	RouteCount int    // 该主机当前路由数(DB count;帮前端提示移除影响)
}

// CreateInput 是创建路由的入参。
type CreateInput struct {
	ServerID          string
	Domain            string
	UpstreamContainer string
	UpstreamPort      int
	// UpstreamKind ∈ "container"(默认/空) | "address"。container → UpstreamContainer 为容器名;
	// address → UpstreamContainer 为 HOST(IP/FQDN/host.docker.internal)。
	UpstreamKind string
	// DNSProviderID(R3)非空 → 该路由走 DNS-01(通配符域名必需)。空 = HTTP-01。
	DNSProviderID string
}

// UpdateInput 是更新路由(R2)的入参。冻结契约。
type UpdateInput struct {
	UpstreamContainer string      // 空 = 保持不变
	UpstreamPort      int         // 0 = 保持不变
	UpstreamKind      string      // ""/"container" | "address";归一化进 Config.UpstreamKind
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
	// CaddyStatus 探测某主机上的反代容器(pipewright-caddy)环境:是否已安装/运行/镜像/端口 + 该主机路由数。
	// 容器不存在不报错(Installed:false);仅传输/SSH 层错误才返回 error(由 httpapi 映射 502/503)。
	CaddyStatus(ctx context.Context, serverID string) (*CaddyStatus, error)
	// PrepareCaddy 显式部署反代环境(ensureCaddy)而无需先绑域名,返回部署后的 CaddyStatus。
	// 空 serverID → ErrEmptyServerID;80/443 冲突等人话错误由 ensureCaddy 透出。
	PrepareCaddy(ctx context.Context, serverID string) (*CaddyStatus, error)
	// RemoveCaddy 停止并删除某主机上的反代容器(pipewright-caddy);保留命名卷(避免重签触发 LE 限速)。
	// 幂等:容器已不存在也成功。
	RemoveCaddy(ctx context.Context, serverID string) error
}

// certProber 抽象「对 域名:443 做 TLS 握手并回报证书状态」的能力(注入便于单测,不触真网络)。
type certProber interface {
	Probe(ctx context.Context, domain string) ProbeResult
}

// DNSResolver 抽象「按 DNS 提供商 id 取 (类型, token 明文)」的能力(R3:DNS-01 渲染需要)。
// 由上层用 dnsprovider + vault 适配注入,避免 proxy import dnsprovider 形成环。
// token 仅在 apply 渲染时取一次,注入 0600 临时 Caddyfile,绝不日志/回库/回 API。
type DNSResolver interface {
	// Resolve 返回 providerType(cloudflare|dnspod|alidns)与该提供商凭据明文 token。
	// 提供商不存在 → ok=false;vault 未配置/凭据缺失 → err。
	Resolve(ctx context.Context, providerID string) (providerType, token string, ok bool, err error)
	// ProviderType 仅返回类型(供校验 DNS 提供商引用存在/合法,不取 token)。
	ProviderType(ctx context.Context, providerID string) (providerType string, ok bool, err error)
}

// ProbeResult 是一次证书探测结果。
type ProbeResult struct {
	// Status ∈ issued | pending | failed。
	Status string
	// Detail 是人话详情(到期时间 / 失败原因);绝不含敏感信息。
	Detail string
}

// service 是 store + target + prober (+ 可选 dnsResolver) 支撑的 Service 实现。
type service struct {
	store  *Store
	tg     target.Service
	prober certProber
	dns    DNSResolver // R3:解析 DNS 提供商 → (类型, token);nil 时不支持 DNS-01/通配符
}

// New 构造 Service。tg 复用已装配的 target.Service(SSH + docker);prober 默认走真实 TLS 握手。
// 不在此做任何重活(无 init 副作用)。
func New(db *sql.DB, tg target.Service) Service {
	return &service{store: NewStore(db), tg: tg, prober: tlsProber{}}
}

// SetDNSResolver 注入 DNS 提供商解析器(R3:DNS-01 通配符 + 子域名)。
// 拆成 setter 而非改 New 签名,以保持 R1/R2 的 New 冻结契约不变(main.go 装配时晚绑)。
func (s *service) SetDNSResolver(r DNSResolver) { s.dns = r }

// DNSConfigurable 让上层在装配后注入 DNS 解析器(避免 import 环 + 保持 New 签名稳定)。
type DNSConfigurable interface {
	SetDNSResolver(r DNSResolver)
}

func (s *service) List(ctx context.Context, serverID string) ([]Route, error) {
	return s.store.list(ctx, serverID)
}

func (s *service) Create(ctx context.Context, in CreateInput) (*Route, error) {
	in.ServerID = strings.TrimSpace(in.ServerID)
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.UpstreamContainer = strings.TrimSpace(in.UpstreamContainer)
	in.UpstreamKind = normalizeUpstreamKind(in.UpstreamKind)
	in.DNSProviderID = strings.TrimSpace(in.DNSProviderID)
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	// 若引用了 DNS 提供商,校验其存在 + 类型合法(经注入的 resolver,不取 token)。
	if in.DNSProviderID != "" {
		if err := s.validateDNSProviderRef(ctx, in.DNSProviderID); err != nil {
			return nil, err
		}
	}

	r := newRoute(in)
	// 1) 先落库(domain 唯一冲突 → ErrDomainTaken,先于触网,避免无谓 docker 操作)。
	if err := s.store.insert(ctx, r); err != nil {
		return nil, err
	}
	// 2) 编排:保证 Caddy 就绪 → (仅 container 上游)接入共享网络 → 渲染 + 下发 + reload。
	//    address 上游为任意 host:port,Caddy 直接反代,无需也不可 docker network connect。
	//    编排失败回滚刚插入的路由(避免留下「库里有、反代上没有」的孤儿)。
	if err := s.ensureAndApply(ctx, r.ServerID, upstreamForConnect(in.UpstreamKind, in.UpstreamContainer)); err != nil {
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
	// 启用时确保上游已接入网络(仅 container 上游;address 上游不接网络);停用时无需(渲染时自然摘除)。
	if on {
		if err := s.ensureAndApply(ctx, r.ServerID, upstreamForConnect(r.Config.UpstreamKind, r.UpstreamContainer)); err != nil {
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
	// 优先经 SSH 直接读 Caddy 容器里已签发的证书文件来判定状态:这反映 Caddy 的真实证书态,
	// 不受「Caddy 不在 443(被宿主 nginx 占用而映射到别的端口)/ 公网 DNS 未指向 / 探测方有本地代理」
	// 等部署形态影响——公网 TLS 握手探测在这些场景会误报 pending/failed,而证书其实早已签发。
	// 读不到(无 SSH / 容器无此域名证书文件 / 传输层错误)才回退到公网 443 握手探测。
	if res, ok := s.probeCaddyCertViaSSH(ctx, r.ServerID, r.Domain); ok {
		if err := s.store.setCertStatus(ctx, id, res.Status, res.Detail); err != nil {
			return nil, err
		}
		return s.store.get(ctx, id)
	}
	res := s.prober.Probe(ctx, r.Domain)
	if err := s.store.setCertStatus(ctx, id, res.Status, res.Detail); err != nil {
		return nil, err
	}
	return s.store.get(ctx, id)
}

// probeCaddyCertViaSSH 经 target(SSH)进 pipewright-caddy 容器读该域名已签发的证书文件(仅证书公钥
// 部分,绝不碰私钥),解析有效期/签发机构判定状态。ok=false 表示「读不到、判不了」,调用方据此回退公网探测。
//   - 证书在有效期 → issued(detail 含签发机构 + 到期日)。
//   - 证书文件在但不在有效期(临时证/尚未生效)→ pending。
//   - 无 tg / serverID 空 / SSH 传输层错误 / 无该域名证书文件 → ok=false(回退公网探测)。
func (s *service) probeCaddyCertViaSSH(ctx context.Context, serverID, domain string) (ProbeResult, bool) {
	if s.tg == nil || strings.TrimSpace(serverID) == "" {
		return ProbeResult{}, false
	}
	// Caddy 把通配符 *.example.com 的证书存为目录/文件名 wildcard_.example.com。
	name := domain
	if strings.HasPrefix(name, "*.") {
		name = "wildcard_." + name[2:]
	}
	// 跨各 CA 目录 glob 该域名的叶子证书;仅 cat 公钥证书(.crt),绝不读 .key 私钥。
	certGlob := "/data/caddy/certificates/*/" + name + "/" + name + ".crt"
	out, err := s.tg.Exec(ctx, serverID, []string{
		"docker", "exec", caddyContainer, "sh", "-c", "cat " + certGlob + " 2>/dev/null",
	})
	if err != nil || out == nil {
		return ProbeResult{}, false // 传输层错误(SSH 不可达等)→ 回退
	}
	leaf := parseFirstCertPEM(out.Stdout)
	if leaf == nil {
		return ProbeResult{}, false // 还没有证书文件(可能仍在签发中)→ 回退公网探测
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return ProbeResult{Status: CertStatusPending, Detail: "证书不在有效期(可能仍在签发中)"}, true
	}
	issuer := strings.TrimSpace(leaf.Issuer.CommonName)
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	detail := fmt.Sprintf("证书有效,到期 %s", leaf.NotAfter.UTC().Format("2006-01-02"))
	if issuer != "" {
		detail = fmt.Sprintf("已由 %s 签发,到期 %s", issuer, leaf.NotAfter.UTC().Format("2006-01-02"))
	}
	return ProbeResult{Status: CertStatusIssued, Detail: detail}, true
}

// parseFirstCertPEM 从 PEM 文本中解析第一张 CERTIFICATE。无有效证书返回 nil。
func parseFirstCertPEM(pemText string) *x509.Certificate {
	rest := []byte(pemText)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return nil
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(blk.Bytes); err == nil {
			return cert
		}
	}
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

	// 2) 归一化高级配置(域名小写去空白 / 去重 / 状态码默认 / 上游类型归一)。
	cfg := normalizeConfig(in.Config)
	// 上游类型:入参 UpstreamKind 优先(显式),否则沿用 config 里的(normalizeConfig 已归一化)。
	if k := strings.ToLower(strings.TrimSpace(in.UpstreamKind)); k != "" {
		cfg.UpstreamKind = k
	}

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

	// 4) 严格校验(注入面全在此关闭:别名 FQDN / CIDR / 重定向路径 / basic auth 用户名 / 路径路由)。
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateUpstream(cfg.UpstreamKind, container, port); err != nil {
		return nil, err
	}
	// R3:通配符主域名必须始终绑定 DNS 提供商(不能在 Update 里把它摘掉)。
	if isWildcardDomain(r.Domain) && cfg.DNSProviderID == "" {
		return nil, ErrWildcardNeedsDNS
	}
	// R3:若引用了 DNS 提供商,校验其存在 + 类型合法(经注入的 resolver,不取 token)。
	if cfg.DNSProviderID != "" {
		if err := s.validateDNSProviderRef(ctx, cfg.DNSProviderID); err != nil {
			return nil, err
		}
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

// CaddyStatus 探测某主机反代容器环境 + 统计该主机路由数。
// 容器缺失非错误(Installed:false);传输/SSH 错误才返回 error。端口为 best-effort,读不到给 ""。
func (s *service) CaddyStatus(ctx context.Context, serverID string) (*CaddyStatus, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return nil, ErrEmptyServerID
	}
	st, err := inspectCaddy(ctx, s.tg, serverID)
	if err != nil {
		return nil, err
	}
	st.ServerID = serverID
	// 路由数:DB count(best-effort;list 失败不掩盖容器探测结果,计 0)。
	if routes, lerr := s.store.list(ctx, serverID); lerr == nil {
		st.RouteCount = len(routes)
	}
	return st, nil
}

// RemoveCaddy 停止并删除某主机反代容器(保留命名卷)。幂等:已不存在也成功。
// 注意:库中既有路由不删,但在 Caddy 重新就绪(下次绑域名)前不会再被反代;前端会在调用前提醒用户。
func (s *service) RemoveCaddy(ctx context.Context, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return ErrEmptyServerID
	}
	return removeCaddy(ctx, s.tg, serverID)
}

// ensureAndApply 是 Create/启用 路径的编排:保证 Caddy 就绪 → (container 上游才)接入上游 → apply。
// container 为空串表示「address 上游」,跳过 docker network connect(Caddy 直接反代任意 host:port)。
func (s *service) ensureAndApply(ctx context.Context, serverID, container string) error {
	if err := ensureCaddy(ctx, s.tg, serverID); err != nil {
		return err
	}
	if container != "" {
		if err := connectUpstream(ctx, s.tg, serverID, container); err != nil {
			return err
		}
	}
	return s.apply(ctx, serverID)
}

// PrepareCaddy 显式部署反代环境(ensureCaddy)而无需先绑域名,返回部署后的 CaddyStatus。
func (s *service) PrepareCaddy(ctx context.Context, serverID string) (*CaddyStatus, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return nil, ErrEmptyServerID
	}
	if err := ensureCaddy(ctx, s.tg, serverID); err != nil {
		return nil, err
	}
	return s.CaddyStatus(ctx, serverID)
}

// upstreamForConnect 返回需接入共享网络的容器名:container 上游返回容器名;address 上游返回 ""
// (调用方据此跳过 docker network connect)。
func upstreamForConnect(kind, container string) string {
	if kind == UpstreamKindAddress {
		return ""
	}
	return container
}

// normalizeUpstreamKind 把空串归一化为 "container"(向后兼容);其余原样返回交校验判定。
func normalizeUpstreamKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return UpstreamKindContainer
	}
	return kind
}

// upstreamHostRe 校验 address 上游的 FQDN(同 domainRe 但此处单列以便阅读);IP 走 net.ParseIP。
var upstreamHostRe = domainRe

// apply 渲染该主机所有 enabled 路由 → 下发 Caddyfile → reload。
// 对绑了 DNS 提供商的路由,在此(且仅在此)经 resolver 取 token 注入渲染(DNS-01);
// token 仅存活于本函数局部 + 写入的 0600 临时文件,绝不日志/回库/回 API。
func (s *service) apply(ctx context.Context, serverID string) error {
	routes, err := s.store.listEnabledForServer(ctx, serverID)
	if err != nil {
		return err
	}
	creds := s.resolveDNSCreds(ctx, routes)
	return applyCaddyfile(ctx, s.tg, serverID, renderCaddyfile(routes, creds))
}

// resolveDNSCreds 为这批路由里引用的每个 DNS 提供商取一次 (类型, token)。
// resolver 未注入 / 取不到(提供商被删 / vault 未配)时跳过该提供商 —— 对应路由退回 HTTP-01 渲染
// (不阻断其它路由 apply;通配符路由会因无 token 而签不出证书,由用户在状态里看到 pending)。
func (s *service) resolveDNSCreds(ctx context.Context, routes []Route) map[string]dnsCred {
	if s.dns == nil {
		return nil
	}
	out := map[string]dnsCred{}
	for _, r := range routes {
		pid := r.Config.DNSProviderID
		if pid == "" {
			continue
		}
		if _, seen := out[pid]; seen {
			continue
		}
		pt, token, ok, err := s.dns.Resolve(ctx, pid)
		if err != nil || !ok || pt == "" || token == "" {
			continue
		}
		out[pid] = dnsCred{Type: pt, Token: token}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- 校验 -------------------------------------------------------------------

// domainRe 校验 FQDN(每段字母数字/连字符,段间点分,顶级域 ≥2 字母)。不接受裸 IP / 端口。
var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// wildcardDomainRe 校验通配符域名 `*.<FQDN>`(仅最左一级通配,其后为合法 FQDN)。
// 仅允许 `*.` 前缀 + 合法 FQDN —— 杜绝 `*foo.x`、`a.*.x`、裸 `*` 等注入/越权形态。
var wildcardDomainRe = regexp.MustCompile(`^\*\.([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// pathRe 校验路径路由的 Path:须以 / 起,字符集为 URL 路径安全字符 + glob `*`(无空白/花括号/引号/换行,
// 防破坏 Caddyfile 或注入指令)。
var pathRe = regexp.MustCompile(`^/[A-Za-z0-9._~:/?#\[\]@!$&'()+,;=%*-]*$`)

// containerRe 校验上游容器名(docker 容器名/ID 允许字符;首字符非 `-` 防 flag 注入)。
var containerRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// healthURIRe 校验健康检查路径(须以 / 起;URL 路径安全字符,无空白/花括号/引号/换行,防破坏 Caddyfile)。
var healthURIRe = regexp.MustCompile(`^/[A-Za-z0-9._~:/?#\[\]@!$&'()+,;=%*-]*$`)

// validLBPolicy 报告负载均衡策略是否为白名单枚举(空串由调用方先行放过 = 不渲染)。
func validLBPolicy(p string) bool {
	switch p {
	case LBPolicyRoundRobin, LBPolicyLeastConn, LBPolicyRandom, LBPolicyFirst:
		return true
	default:
		return false
	}
}

// imageRefRe 校验 docker 镜像引用(registry/path:tag@sha 常见安全字符;首字符非 `-` 防 flag 注入,
// 无空白/shell 元字符)。供经 env 覆盖反代镜像时防注入。
var imageRefRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)

// isWildcardDomain 报告 domain 是否为通配符域名 `*.<FQDN>`。
func isWildcardDomain(domain string) bool { return wildcardDomainRe.MatchString(domain) }

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

// validateRouteDomain 校验主域名:普通 FQDN 始终允许;通配符 `*.x` 仅当 hasDNSProvider 才允许
// (HTTP-01 无法签泛域名,必须 DNS-01 → 必须绑 DNS 提供商)。
func validateRouteDomain(domain string, hasDNSProvider bool) error {
	if isWildcardDomain(domain) {
		if !hasDNSProvider {
			return ErrWildcardNeedsDNS
		}
		if len(domain) > 253 {
			return ErrInvalidDomain
		}
		return nil
	}
	return validateDomain(domain)
}

func validateCreate(in CreateInput) error {
	if in.ServerID == "" {
		return ErrEmptyServerID
	}
	if err := validateRouteDomain(in.Domain, strings.TrimSpace(in.DNSProviderID) != ""); err != nil {
		return err
	}
	if in.UpstreamContainer == "" {
		return ErrEmptyContainer
	}
	if err := validateUpstream(in.UpstreamKind, in.UpstreamContainer, in.UpstreamPort); err != nil {
		return err
	}
	return nil
}

// validateUpstream 校验主上游(kind + container/host + port):
//   - kind=container(含归一化空串):UpstreamContainer 须过 containerRe(docker 容器名安全字符)。
//   - kind=address:UpstreamContainer 持 HOST,须为合法 IPv4/IPv6(net.ParseIP)、合法 FQDN(domainRe)
//     或字面 host.docker.internal —— 关闭 Caddyfile 注入面(无空格/冒号/斜杠/花括号)。
//   - 端口两种 kind 均须 1..65535。
//
// 调用前 kind 应已 normalizeUpstreamKind(空串归一化为 container)。
func validateUpstream(kind, host string, port int) error {
	switch kind {
	case UpstreamKindContainer:
		if !containerRe.MatchString(host) {
			return ErrInvalidContainer
		}
	case UpstreamKindAddress:
		if !validUpstreamHost(host) {
			return ErrInvalidUpstreamHost
		}
	default:
		return ErrInvalidUpstreamKind
	}
	if port < 1 || port > 65535 {
		return ErrInvalidPort
	}
	return nil
}

// validUpstreamHost 报告 host 是否为合法的 address 上游主机:IP / FQDN / 字面 host.docker.internal。
// 拒绝一切含空白、冒号、斜杠、花括号等会破坏 Caddyfile 的字符。
func validUpstreamHost(host string) bool {
	if host == "host.docker.internal" {
		return true
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return upstreamHostRe.MatchString(host)
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
	out.UpstreamKind = normalizeUpstreamKind(in.UpstreamKind)
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

	out.DNSProviderID = strings.TrimSpace(in.DNSProviderID)

	// 路径路由:去空白、丢空项(路径为空的规则);容器名小写不强制(docker 容器名大小写敏感)。
	prs := make([]PathRule, 0, len(in.PathRules))
	for _, pr := range in.PathRules {
		path := strings.TrimSpace(pr.Path)
		container := strings.TrimSpace(pr.UpstreamContainer)
		if path == "" && container == "" {
			continue
		}
		prs = append(prs, PathRule{Path: path, UpstreamContainer: container, UpstreamPort: pr.UpstreamPort})
	}
	if len(prs) == 0 {
		prs = nil
	}
	out.PathRules = prs

	// R4 E4.2:额外上游去空白丢空项;lb_policy / health_uri / health_interval 去空白小写(策略小写)。
	ups := make([]Upstream, 0, len(in.Upstreams))
	for _, u := range in.Upstreams {
		c := strings.TrimSpace(u.Container)
		if c == "" && u.Port == 0 {
			continue
		}
		ups = append(ups, Upstream{Container: c, Port: u.Port})
	}
	if len(ups) == 0 {
		ups = nil
	}
	out.Upstreams = ups
	out.LBPolicy = strings.ToLower(strings.TrimSpace(in.LBPolicy))
	if out.LBPolicy == LBPolicyFirst {
		out.LBPolicy = "" // first = Caddy 默认,不渲染 lb_policy
	}
	out.HealthURI = strings.TrimSpace(in.HealthURI)
	out.HealthInterval = strings.TrimSpace(in.HealthInterval)

	// R4 E4.3:TCP 透传去空白(容器名);WS/gRPC 布尔原样。
	if in.TCPPassthrough != nil {
		tc := *in.TCPPassthrough
		tc.UpstreamContainer = strings.TrimSpace(tc.UpstreamContainer)
		// 全零(无监听端口、无上游)视为未配置 → 置 nil。
		if tc.ListenPort == 0 && tc.UpstreamPort == 0 && tc.UpstreamContainer == "" {
			out.TCPPassthrough = nil
		} else {
			out.TCPPassthrough = &tc
		}
	}
	return out
}

// validateConfig 严格校验高级配置(关闭一切 Caddyfile 注入面)。
func validateConfig(cfg RouteConfig) error {
	for _, a := range cfg.Aliases {
		// 通配符别名(*.example.com)合法,但与主域名同规则:必须走 DNS-01,需绑 DNS 提供商
		// (HTTP-01 签不出泛域名)。前端在挂了 DNS 提供商时才允许填通配符别名,这里做后端兜底。
		if isWildcardDomain(a) {
			if cfg.DNSProviderID == "" {
				return ErrWildcardNeedsDNS
			}
			continue
		}
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
	// 路径路由规则(R3 E3.5):路径须以 / 起 + 安全字符集;上游容器/端口同主上游校验。
	for _, pr := range cfg.PathRules {
		if !pathRe.MatchString(pr.Path) {
			return ErrInvalidPathRule
		}
		if !containerRe.MatchString(pr.UpstreamContainer) {
			return ErrInvalidPathRule
		}
		if pr.UpstreamPort < 1 || pr.UpstreamPort > 65535 {
			return ErrInvalidPathRule
		}
	}
	// R4 E4.2:额外上游(负载均衡)逐条同主上游校验(容器名 + 端口)。
	for _, u := range cfg.Upstreams {
		if !containerRe.MatchString(u.Container) {
			return ErrInvalidUpstream
		}
		if u.Port < 1 || u.Port > 65535 {
			return ErrInvalidUpstream
		}
	}
	// lb_policy 仅当非空时校验白名单(空 = 不渲染,first 已在归一化时被清空)。
	if cfg.LBPolicy != "" && !validLBPolicy(cfg.LBPolicy) {
		return ErrInvalidLBPolicy
	}
	// health_uri 须以 / 起 + 安全字符集(空 = 不启用主动健康检查)。
	if cfg.HealthURI != "" && !healthURIRe.MatchString(cfg.HealthURI) {
		return ErrInvalidHealthURI
	}
	// health_interval 须可被 time.ParseDuration 解析且为正(空 = Caddy 默认)。
	if cfg.HealthInterval != "" {
		d, perr := time.ParseDuration(cfg.HealthInterval)
		if perr != nil || d <= 0 {
			return ErrInvalidHealthInterval
		}
	}
	// R4 E4.3:TCP 透传(监听/上游端口 1..65535;上游容器名同主上游校验)。
	if tc := cfg.TCPPassthrough; tc != nil {
		if tc.ListenPort < 1 || tc.ListenPort > 65535 {
			return ErrInvalidTCPPassthrough
		}
		if tc.UpstreamPort < 1 || tc.UpstreamPort > 65535 {
			return ErrInvalidTCPPassthrough
		}
		if !containerRe.MatchString(tc.UpstreamContainer) {
			return ErrInvalidTCPPassthrough
		}
	}
	return nil
}

// validateDNSProviderRef 校验 DNS 提供商引用存在且类型合法(经注入的 resolver,不取 token)。
// 未注入 resolver → 视为「DNS 功能不可用」,引用即非法。
func (s *service) validateDNSProviderRef(ctx context.Context, providerID string) error {
	if s.dns == nil {
		return ErrInvalidDNSProvider
	}
	pt, ok, err := s.dns.ProviderType(ctx, providerID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidDNSProvider
	}
	switch pt {
	case "cloudflare", "dnspod", "alidns":
		return nil
	default:
		return ErrInvalidDNSProvider
	}
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
