package productrelease

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/configstate"
)

// HoldsRun keeps the exact admitted run available for a pending reconciliation.
// The idempotency record binds the run to the claimed batch position even when
// a crash happened before the acknowledgment recorded its RunID in the item.
func HoldsRun(ctx context.Context, q configstate.DBTX, id string) (bool, error) {
	var scope, key, fingerprint string
	err := q.QueryRowContext(ctx, `SELECT scope,request_id,fingerprint FROM mcp_run_requests WHERE run_id=? AND scope LIKE 'product-release:%'`, id).Scan(&scope, &key, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	batchID, matched := strings.CutPrefix(scope, "product-release:")
	slot, hasSlot := strings.CutPrefix(key, "item:")
	pos, err := strconv.Atoi(slot)
	if !matched || !hasSlot || !validID(batchID) || err != nil || pos < 0 || strconv.Itoa(pos) != slot {
		return false, nil
	}
	b, err := scan(q.QueryRowContext(ctx, `SELECT `+columns+` FROM product_release_batches WHERE id=?`, batchID))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pos >= len(b.Items) || ItemDigest(b.Plan.Items[pos]) != fingerprint {
		return false, nil
	}
	item := b.Items[pos]
	if item.RunID != "" && item.RunID != id {
		return false, nil
	}
	switch b.State {
	case "pending", "running", "waiting_approval", "needs_reconciliation":
		switch item.State {
		case "dispatching", "running", "waiting_approval", "unknown", "succeeded":
			return true, nil
		}
	}
	return false, nil
}
