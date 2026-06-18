package dnsprovider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Store 持久化 DNS 提供商(参数化 SQL,两方言一致)。绝不读/写任何 token 明文(只存 credential_id)。
type Store struct {
	db *sql.DB
}

// NewStore 构造 DNS 提供商持久层。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// insert 落库一条新提供商。
func (s *Store) insert(ctx context.Context, p *Provider) error {
	created := p.CreatedAt.UTC().Format(time.RFC3339)
	updated := p.UpdatedAt.UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO dns_providers (id, type, name, credential_id, base_domain, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Type, p.Name, p.CredentialID, p.BaseDomain, created, updated,
	)
	if err != nil {
		return fmt.Errorf("dnsprovider: insert: %w", err)
	}
	return nil
}

// get 读取单条提供商;不存在 → ErrNotFound。
func (s *Store) get(ctx context.Context, id string) (*Provider, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, type, name, credential_id, base_domain, created_at, updated_at
		 FROM dns_providers WHERE id = ?`, id)
	p, err := scanProvider(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// list 返回全部提供商(创建时间倒序)。
func (s *Store) list(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, name, credential_id, base_domain, created_at, updated_at
		 FROM dns_providers ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("dnsprovider: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Provider, 0)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dnsprovider: iterate: %w", err)
	}
	return out, nil
}

// del 删除提供商;不存在 → ErrNotFound。
func (s *Store) del(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dns_providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("dnsprovider: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// newProvider 据入参构造一条待插入提供商(id/时间戳在此填好)。
func newProvider(in CreateInput) *Provider {
	now := time.Now().UTC()
	return &Provider{
		ID:           uuid.NewString(),
		Type:         in.Type,
		Name:         in.Name,
		CredentialID: in.CredentialID,
		BaseDomain:   in.BaseDomain,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

// scanProvider 把一行扫描为 Provider(永不读任何密文/明文列)。
func scanProvider(sc scanner) (*Provider, error) {
	var (
		p          Provider
		createdStr string
		updatedStr string
	)
	if err := sc.Scan(&p.ID, &p.Type, &p.Name, &p.CredentialID, &p.BaseDomain, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("dnsprovider: parse created_at: %w", err)
	}
	updated, err := time.Parse(time.RFC3339, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("dnsprovider: parse updated_at: %w", err)
	}
	p.CreatedAt = created
	p.UpdatedAt = updated
	return &p, nil
}
