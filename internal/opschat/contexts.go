package opschat

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

func (s *Service) validateContext(ctx context.Context, key string) error {
	switch key {
	case "", "containers", "overview", "servers":
		return nil
	}
	if !strings.HasPrefix(key, "terminal:") {
		return ErrInvalid
	}
	id := strings.TrimPrefix(key, "terminal:")
	u, err := uuid.Parse(id)
	if err != nil || u.String() != id {
		return ErrInvalid
	}
	if s.executor == nil {
		return ErrUnavailable
	}
	snap, err := s.executor.Capture(ctx, id)
	if err != nil || snap.Server.ID != id {
		return ErrInvalid
	}
	return nil
}
func (s *Service) bindContext(ctx context.Context, tx *sql.Tx, key, id string) error {
	if key == "" {
		_, err := tx.ExecContext(ctx, "UPDATE ops_chat_state SET active_session=? WHERE id=1", id)
		return databaseError(err)
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO ops_chat_contexts(context_key,session_id) VALUES(?,?) "+store.UpsertSuffix(store.DialectOf(s.db), []string{"context_key"}, []string{"session_id"}), key, id)
	return databaseError(err)
}
func (s *Service) ActivateContext(ctx context.Context, id, key string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.validateContext(ctx, key); err != nil {
		return err
	}
	return s.transaction(ctx, true, func(tx *sql.Tx) error {
		if _, err := s.session(ctx, tx, id); err != nil {
			return err
		}
		return s.bindContext(ctx, tx, key, id)
	})
}
