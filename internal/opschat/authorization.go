package opschat

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// Only controller persistence may read its fixed session after revocation to
// record interrupted/unknown results. This key grants no remote or model access.
type completionSessionKey struct{}

// Set only by Cancel; never inherited by new remote/model work.
type cancellationWriteKey struct{}

func (s *Service) authorizationContext(ctx context.Context, q querier) context.Context {
	if q == s.db {
		return ctx
	}
	if tx, ok := q.(configstate.DBTX); ok {
		return configstate.WithTxContext(ctx, s.db, tx)
	}
	// Read decorators must retain the same transaction for authorization. Opening
	// a second connection can deadlock single-connection databases and observes
	// a different snapshot. The adapter cannot introduce writes.
	return configstate.WithTxContext(ctx, s.db, authorizationReader{q})
}

type authorizationReader struct{ querier }

func (authorizationReader) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, configstate.ErrReadOnly
}
func (s *Service) authorizeSession(ctx context.Context, q querier, id, action string) error {
	if fixed, ok := ctx.Value(completionSessionKey{}).(string); ok && fixed == id && action == "opschat.read" {
		return nil
	}
	return tenancy.Check(s.authorizationContext(ctx, q), s.db, action, "ops_chat", id)
}
func (s *Service) sessionPredicate(ctx context.Context, q querier, column string) (authz.Predicate, error) {
	return tenancy.QueryPredicate(s.authorizationContext(ctx, q), s.db, "opschat.read", column)
}
func (s *Service) contextKey(ctx context.Context, key string) (string, error) {
	p, on, err := tenancy.CurrentPrincipal(ctx, s.db)
	if err != nil {
		return "", err
	}
	if !on {
		return key, nil
	}
	raw, _ := json.Marshal([]string{p.ID, p.TenantID, key})
	sum := sha256.Sum256(raw)
	return "u:" + hex.EncodeToString(sum[:]), nil
}
func (s *Service) checkTargets(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := tenancy.Check(ctx, s.db, "server.inspect", "server", id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) requestKey(ctx context.Context, request string) (string, error) {
	p, on, err := tenancy.CurrentPrincipal(ctx, s.db)
	if err != nil {
		return "", err
	}
	if !on {
		return request, nil
	}
	raw, _ := json.Marshal([]string{p.ID, p.TenantID, request})
	return uuid.NewSHA1(uuid.NameSpaceOID, raw).String(), nil
}

func (s *Service) workerContext(ctx context.Context, id, action string) context.Context {
	return tenancy.WithActionBoundary(ctx, func(current context.Context) error { return s.authorizeSession(current, s.db, id, action) })
}
func (s *Service) authorizeRemote(ctx context.Context, c *callRecord) error {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	if err := s.authorizeSession(ctx, s.db, c.Body.View.SessionID, "opschat.submit"); err != nil {
		return err
	}
	server := c.Body.Snapshot.Server
	if err := tenancy.Check(ctx, s.db, "server.use", "server", server.ID); err != nil {
		return err
	}
	if err := tenancy.Check(ctx, s.db, "credential.use", "credential", server.CredentialID); err != nil {
		return err
	}
	action := ""
	if strings.HasPrefix(c.Body.View.ToolID, "docker_") {
		action = "container.inspect"
	}
	if c.Body.View.ToolID == "docker_action" {
		action = "container.operate"
	}
	if c.Body.View.ToolID == "docker_exec" {
		action = "container.terminal"
	}
	if action != "" {
		if err := tenancy.Check(ctx, s.db, action, "server", server.ID); err != nil {
			return err
		}
	}
	if mutation(c.Body.View.ToolID) {
		return s.authorizeSession(ctx, s.db, c.Body.View.SessionID, "opschat.confirm")
	}
	return nil
}

// AuthorizeRead is the live lease check used before emitting each SSE batch.
func (s *Service) AuthorizeRead(ctx context.Context, id string) error {
	return s.authorizeSession(ctx, s.db, id, "opschat.read")
}

// AuthorizeCatalog permits only users who can create a private conversation or
// read at least one of their own conversations. The catalog contains no secrets.
func (s *Service) AuthorizeCatalog(ctx context.Context) error {
	if err := tenancy.CheckNew(ctx, s.db, "opschat.create", "ops_chat", "new-resource"); err == nil {
		return nil
	}
	predicate, err := s.sessionPredicate(ctx, s.db, "chat.id")
	if err != nil {
		return err
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ops_chat_sessions chat WHERE "+predicate.SQL, predicate.Args...).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return tenancy.ErrForbidden
	}
	return nil
}
