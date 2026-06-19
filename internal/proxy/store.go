package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// storedConfig 是 RouteConfig 的**存储表示**:与领域 RouteConfig 字段一一对应,但 BasicAuthHash
// 用普通 JSON tag 序列化(领域结构体的 `json:"-"` 是为了不外泄给 API DTO,故落库不能复用它)。
// 落库时 RouteConfig → storedConfig(含哈希)→ JSON;读库反向。
type storedConfig struct {
	UpstreamKind    string     `json:"upstreamKind,omitempty"`
	Aliases         []string   `json:"aliases,omitempty"`
	ForceHTTPS      bool       `json:"forceHttps"`
	HSTS            bool       `json:"hsts"`
	SecurityHeaders bool       `json:"securityHeaders"`
	Compression     bool       `json:"compression"`
	BasicAuthUser   string     `json:"basicAuthUser,omitempty"`
	BasicAuthHash   string     `json:"basicAuthHash,omitempty"`
	IPAllow         []string   `json:"ipAllow,omitempty"`
	IPDeny          []string   `json:"ipDeny,omitempty"`
	Redirects       []Redirect `json:"redirects,omitempty"`
	DNSProviderID   string     `json:"dnsProviderId,omitempty"`
	PathRules       []PathRule `json:"pathRules,omitempty"`
	Upstreams       []Upstream `json:"upstreams,omitempty"`
	LBPolicy        string     `json:"lbPolicy,omitempty"`
	HealthURI       string     `json:"healthUri,omitempty"`
	HealthInterval  string     `json:"healthInterval,omitempty"`
	WebSocket       bool       `json:"websocket,omitempty"`
	GRPC            bool       `json:"grpc,omitempty"`
	TCPPassthrough  *TCPConfig `json:"tcpPassthrough,omitempty"`
}

// marshalConfig 把领域 RouteConfig 序列化为 DB 存储 JSON(含 bcrypt 哈希)。零值配置 → 空串(向后兼容)。
func marshalConfig(c RouteConfig) (string, error) {
	if isZeroConfig(c) {
		return "", nil
	}
	b, err := json.Marshal(storedConfig{
		UpstreamKind:    c.UpstreamKind,
		Aliases:         c.Aliases,
		ForceHTTPS:      c.ForceHTTPS,
		HSTS:            c.HSTS,
		SecurityHeaders: c.SecurityHeaders,
		Compression:     c.Compression,
		BasicAuthUser:   c.BasicAuthUser,
		BasicAuthHash:   c.BasicAuthHash,
		IPAllow:         c.IPAllow,
		IPDeny:          c.IPDeny,
		Redirects:       c.Redirects,
		DNSProviderID:   c.DNSProviderID,
		PathRules:       c.PathRules,
		Upstreams:       c.Upstreams,
		LBPolicy:        c.LBPolicy,
		HealthURI:       c.HealthURI,
		HealthInterval:  c.HealthInterval,
		WebSocket:       c.WebSocket,
		GRPC:            c.GRPC,
		TCPPassthrough:  c.TCPPassthrough,
	})
	if err != nil {
		return "", fmt.Errorf("proxy: marshal config: %w", err)
	}
	return string(b), nil
}

// unmarshalConfig 把 DB 存储 JSON 反序列化为领域 RouteConfig(含 bcrypt 哈希)。空串 → 零值配置。
func unmarshalConfig(s string) (RouteConfig, error) {
	if s == "" {
		return RouteConfig{}, nil
	}
	var sc storedConfig
	if err := json.Unmarshal([]byte(s), &sc); err != nil {
		return RouteConfig{}, fmt.Errorf("proxy: unmarshal config: %w", err)
	}
	return RouteConfig{
		UpstreamKind:    sc.UpstreamKind,
		Aliases:         sc.Aliases,
		ForceHTTPS:      sc.ForceHTTPS,
		HSTS:            sc.HSTS,
		SecurityHeaders: sc.SecurityHeaders,
		Compression:     sc.Compression,
		BasicAuthUser:   sc.BasicAuthUser,
		BasicAuthHash:   sc.BasicAuthHash,
		IPAllow:         sc.IPAllow,
		IPDeny:          sc.IPDeny,
		Redirects:       sc.Redirects,
		DNSProviderID:   sc.DNSProviderID,
		PathRules:       sc.PathRules,
		Upstreams:       sc.Upstreams,
		LBPolicy:        sc.LBPolicy,
		HealthURI:       sc.HealthURI,
		HealthInterval:  sc.HealthInterval,
		WebSocket:       sc.WebSocket,
		GRPC:            sc.GRPC,
		TCPPassthrough:  sc.TCPPassthrough,
	}, nil
}

// isZeroConfig 判定配置是否为零值(全默认),用于决定是否落空串。
func isZeroConfig(c RouteConfig) bool {
	// container 上游(含空串归一化)视为默认,不阻止落空串(保持 R1 容器路由的向后兼容字节)。
	// 仅 address 上游(非默认)使配置非零,触发 config JSON 落库。
	kindZero := c.UpstreamKind == "" || c.UpstreamKind == UpstreamKindContainer
	return kindZero && len(c.Aliases) == 0 && !c.ForceHTTPS && !c.HSTS && !c.SecurityHeaders &&
		!c.Compression && c.BasicAuthUser == "" && c.BasicAuthHash == "" &&
		len(c.IPAllow) == 0 && len(c.IPDeny) == 0 && len(c.Redirects) == 0 &&
		c.DNSProviderID == "" && len(c.PathRules) == 0 &&
		len(c.Upstreams) == 0 && c.LBPolicy == "" && c.HealthURI == "" &&
		c.HealthInterval == "" && !c.WebSocket && !c.GRPC && c.TCPPassthrough == nil
}

// Store 持久化反代路由(参数化 SQL,两方言一致)。
type Store struct {
	db *sql.DB
}

// NewStore 构造路由持久层。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// insert 落库一条新路由(cert_status 初始 pending)。domain 唯一冲突 → ErrDomainTaken。
func (s *Store) insert(ctx context.Context, r *Route) error {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	created := r.CreatedAt.UTC().Format(time.RFC3339)
	updated := r.UpdatedAt.UTC().Format(time.RFC3339)
	cfg, err := marshalConfig(r.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO proxy_routes
		   (id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ServerID, r.Domain, r.UpstreamContainer, r.UpstreamPort, r.TLSMode, enabled, r.CertStatus, r.CertDetail, cfg, created, updated,
	)
	if err != nil {
		if store.IsUniqueErr(err) {
			return ErrDomainTaken
		}
		return fmt.Errorf("proxy: insert route: %w", err)
	}
	return nil
}

// get 读取单条路由;不存在 → ErrNotFound。
func (s *Store) get(ctx context.Context, id string) (*Route, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, config, created_at, updated_at
		 FROM proxy_routes WHERE id = ?`, id)
	r, err := scanRoute(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

// list 返回某主机的全部路由(创建时间倒序);serverID 为空 → 返回全部。
func (s *Store) list(ctx context.Context, serverID string) ([]Route, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if serverID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, config, created_at, updated_at
			 FROM proxy_routes ORDER BY created_at DESC, id`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, config, created_at, updated_at
			 FROM proxy_routes WHERE server_id = ? ORDER BY created_at DESC, id`, serverID)
	}
	if err != nil {
		return nil, fmt.Errorf("proxy: list routes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Route, 0)
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proxy: iterate routes: %w", err)
	}
	return out, nil
}

// listEnabledForServer 返回某主机所有 enabled 路由(供渲染 Caddyfile),按 domain 升序稳定排序。
func (s *Store) listEnabledForServer(ctx context.Context, serverID string) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, config, created_at, updated_at
		 FROM proxy_routes WHERE server_id = ? AND enabled = 1 ORDER BY domain ASC`, serverID)
	if err != nil {
		return nil, fmt.Errorf("proxy: list enabled routes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Route, 0)
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proxy: iterate enabled routes: %w", err)
	}
	return out, nil
}

// setEnabled 启停某路由;不存在 → ErrNotFound。
func (s *Store) setEnabled(ctx context.Context, id string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET enabled = ?, updated_at = ? WHERE id = ?`, v, now, id)
	if err != nil {
		return fmt.Errorf("proxy: set enabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// setCertStatus 回写证书状态 + 人话详情(RefreshStatus 用);不存在 → ErrNotFound。
func (s *Store) setCertStatus(ctx context.Context, id, status, detail string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET cert_status = ?, cert_detail = ?, updated_at = ? WHERE id = ?`,
		status, detail, now, id)
	if err != nil {
		return fmt.Errorf("proxy: set cert status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// updateConfig 更新某路由的 upstream 容器/端口 + 高级配置 JSON(R2 Update 用);不存在 → ErrNotFound。
func (s *Store) updateConfig(ctx context.Context, id, container string, port int, cfg RouteConfig) error {
	cfgJSON, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proxy_routes SET upstream_container = ?, upstream_port = ?, config = ?, updated_at = ? WHERE id = ?`,
		container, port, cfgJSON, now, id)
	if err != nil {
		return fmt.Errorf("proxy: update config: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// del 删除某路由;不存在 → ErrNotFound。
func (s *Store) del(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM proxy_routes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("proxy: delete route: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// newRoute 据入参构造一条待插入路由(id/时间戳/初始证书态在此填好)。
func newRoute(in CreateInput) *Route {
	now := time.Now().UTC()
	var cfg RouteConfig
	// 主上游类型(container 默认 / address):存进 config JSON(免迁移)。空串归一化为 container。
	cfg.UpstreamKind = in.UpstreamKind
	if cfg.UpstreamKind == "" {
		cfg.UpstreamKind = UpstreamKindContainer
	}
	if in.DNSProviderID != "" {
		cfg.DNSProviderID = in.DNSProviderID
	}
	return &Route{
		ID:                uuid.NewString(),
		ServerID:          in.ServerID,
		Domain:            in.Domain,
		UpstreamContainer: in.UpstreamContainer,
		UpstreamPort:      in.UpstreamPort,
		TLSMode:           tlsModeAuto,
		Enabled:           true,
		CertStatus:        CertStatusPending,
		CertDetail:        "",
		Config:            cfg,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

// scanRoute 把一行扫描为 Route。
func scanRoute(sc scanner) (*Route, error) {
	var (
		r          Route
		enabled    int
		certDetail sql.NullString
		configStr  sql.NullString
		createdStr string
		updatedStr string
	)
	if err := sc.Scan(
		&r.ID, &r.ServerID, &r.Domain, &r.UpstreamContainer, &r.UpstreamPort,
		&r.TLSMode, &enabled, &r.CertStatus, &certDetail, &configStr, &createdStr, &updatedStr,
	); err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	r.CertDetail = certDetail.String
	cfg, err := unmarshalConfig(configStr.String)
	if err != nil {
		return nil, err
	}
	r.Config = cfg
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse updated_at: %w", err)
	}
	r.CreatedAt = created
	r.UpdatedAt = updated
	return &r, nil
}
