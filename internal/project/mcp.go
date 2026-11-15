package project

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// ListMCPProjects intersects the token's fixed IDs and current owner scope before
// count/pagination. It projects no credential or repository metadata.
func (s *service) ListMCPProjects(ctx context.Context, ids []string, page, pageSize int) ([]Project, int, error) {
	out := []Project{}
	if len(ids) == 0 || len(ids) > 32 || page < 1 || page > 32 || pageSize < 1 || pageSize > 32 {
		return out, 0, ErrNotFound
	}
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "project.read", "p.id")
	if err != nil {
		return nil, 0, err
	}
	where := "p.id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ") AND (" + predicate.SQL + ")"
	args := []any{}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, predicate.Args...)
	q := configstate.Queryer(ctx, s.db)
	var total int
	if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects p WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageSQL, pageArgs := store.Page(store.DialectOf(s.db), pageSize, (page-1)*pageSize)
	args = append(args, pageArgs...)
	rows, err := q.QueryContext(ctx, "SELECT p.id,p.name,p.default_branch FROM projects p WHERE "+where+" ORDER BY p.id"+pageSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Project
		if err = rows.Scan(&item.ID, &item.Name, &item.DefaultBranch); err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}
