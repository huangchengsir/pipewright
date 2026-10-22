package vault

import (
	"context"

	"github.com/huangchengsir/pipewright/internal/configstate"
)

// ExistsTx checks a credential reference using the caller's transaction without
// revealing a secret or opening another database connection.
func ExistsTx(ctx context.Context, q configstate.DBTX, id string) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM credentials WHERE id = ?`, id).Scan(&count)
	return count > 0, err
}
