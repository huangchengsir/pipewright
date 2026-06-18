package proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

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
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO proxy_routes
		   (id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ServerID, r.Domain, r.UpstreamContainer, r.UpstreamPort, r.TLSMode, enabled, r.CertStatus, r.CertDetail, created, updated,
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
		`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, created_at, updated_at
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
			`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, created_at, updated_at
			 FROM proxy_routes ORDER BY created_at DESC, id`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, created_at, updated_at
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
		`SELECT id, server_id, domain, upstream_container, upstream_port, tls_mode, enabled, cert_status, cert_detail, created_at, updated_at
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
		createdStr string
		updatedStr string
	)
	if err := sc.Scan(
		&r.ID, &r.ServerID, &r.Domain, &r.UpstreamContainer, &r.UpstreamPort,
		&r.TLSMode, &enabled, &r.CertStatus, &certDetail, &createdStr, &updatedStr,
	); err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	r.CertDetail = certDetail.String
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
