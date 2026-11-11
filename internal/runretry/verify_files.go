package runretry

import (
	"context"
	"database/sql"
)

// VerifyStoredFiles reads retained encrypted checkpoints without creating a run,
// refreshing ownership or changing reference pins. Migration supplies a read-only
// CAS verifier; private checkpoint payloads never cross this interface.
func (s *Service) VerifyStoredFiles(ctx context.Context, verify func(context.Context, string, int64) error) error {
	rows, e := s.db.QueryContext(ctx, "SELECT n.attempt_id,n.node_key,a.root_id,n.ciphertext FROM run_retry_nodes n LEFT JOIN run_retry_attempts a ON a.run_id=n.attempt_id WHERE n.ciphertext IS NOT NULL")
	if e != nil {
		return ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var attempt, node string
		var root sql.NullString
		var cipher []byte
		if rows.Scan(&attempt, &node, &root, &cipher) != nil || !root.Valid {
			return ErrUnavailable
		}
		var outcome Outcome
		if s.open(cipher, "checkpoint", root.String, attempt+":"+node, &outcome) != nil {
			return ErrUnavailable
		}
		clear(outcome.Outputs.Env)
		for _, file := range outcome.Outputs.Files {
			if verify == nil || file.Key != file.SHA256 || verify(ctx, file.Key, file.Size) != nil {
				return ErrUnavailable
			}
		}
	}
	if rows.Err() != nil {
		return ErrUnavailable
	}
	return nil
}
