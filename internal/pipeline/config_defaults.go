package pipeline

import (
	"context"
	"database/sql"
	"errors"
)

// EnsureStoredDefault is an execution-admission hook, never a configuration
// query. Existing settings are read without masks or credential resolution.
func (s *service) EnsureStoredDefault(ctx context.Context, id string) error {
	_, err := s.load(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.createDefault(ctx, id)
	}
	return err
}
func (s *settingsService) EnsureStoredDefault(ctx context.Context, id string) error {
	_, err := s.loadStored(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.createDefault(ctx, id)
	}
	return err
}
