package runretry

import "context"

// FrozenSnapshotForRelease is an internal orchestration contract. It never goes
// through an HTTP/MCP projection and never captures today's pipeline config.
func (s *Service) FrozenSnapshotForRelease(ctx context.Context, run string) (Snapshot, error) {
	l, e := s.load(ctx, run)
	if e != nil {
		return Snapshot{}, e
	}
	if l.sourceConflict {
		return Snapshot{}, ErrConflict
	}
	return l.snapshot, nil
}
