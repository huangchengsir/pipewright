package runretry

import (
	"context"
	"database/sql"
	"errors"
)

// Identity is a public provenance projection. Reading it never decrypts config or outputs.
type Identity struct {
	RunID         string
	LogicalRunID  string
	SourceRunID   string
	ExecutionKind string
	Initiator     string
	Revision      int64
}

func (s *Service) Identity(ctx context.Context, id string) (Identity, error) {
	var out Identity
	err := s.q(ctx).QueryRowContext(ctx, "SELECT run_id,root_id,parent_id,execution_kind,initiator,revision FROM run_retry_attempts WHERE run_id=?", id).Scan(&out.RunID, &out.LogicalRunID, &out.SourceRunID, &out.ExecutionKind, &out.Initiator, &out.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnavailable
	}
	return out, err
}
