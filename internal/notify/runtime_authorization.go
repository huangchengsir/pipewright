package notify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

type anomalySourceKey struct{}

// WithAnomalySource is a controller adapter, not an HTTP/MCP input contract.
func WithAnomalySource(ctx context.Context, serverID string) context.Context {
	return context.WithValue(ctx, anomalySourceKey{}, serverID)
}

type deliveryKey struct{}
type routedDelivery struct {
	channel, version string
	approval         context.Context
	source           func(context.Context) error
}

func channelVersion(ch *Channel, sealed []byte) string {
	data, _ := json.Marshal(struct {
		ID, Type string
		Enabled  bool
		Config   Config
		Secret   []byte
	}{ch.ID, ch.Type, ch.Enabled, ch.Config, sealed})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s *service) routeVersion(ctx context.Context, q configstate.DBTX, id string) (string, string, error) {
	var project sql.NullString
	var event, channel string
	var enabled int
	if err := q.QueryRowContext(ctx, `SELECT project_id,event,channel_id,enabled FROM notification_routes WHERE id=?`, id).Scan(&project, &event, &channel, &enabled); err != nil {
		return "", "", err
	}
	if enabled != 1 {
		return "", "", tenancy.ErrForbidden
	}
	ch, sealed, err := s.load(configstate.WithTxContext(ctx, s.db, q), channel)
	if err != nil || !ch.Enabled {
		return "", "", tenancy.ErrForbidden
	}
	data, _ := json.Marshal([]string{id, project.String, event, channel, channelVersion(ch, sealed)})
	sum := sha256.Sum256(data)
	return channel, hex.EncodeToString(sum[:]), nil
}
func (s *service) approveRoute(ctx context.Context, id string) error {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return err
	}
	_, digest, err := s.routeVersion(ctx, configstate.Queryer(ctx, s.db), id)
	if errors.Is(err, tenancy.ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		return tenancy.RevokeAutomation(ctx, s.db, "notification", id)
	}
	if err != nil {
		return err
	}
	return tenancy.ApproveAutomation(ctx, s.db, "notification", id, "", digest, []tenancy.AutomationPermission{{Action: "system.manage", ResourceID: "instance"}})
}
func (s *service) approveChannelRoutes(ctx context.Context, id string) error {
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT id FROM notification_routes WHERE channel_id=?`, id)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.approveRoute(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *service) eventSource(ctx context.Context, project, event string, vars TemplateVars) error {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	if event == EventAnomalyDetected {
		if server, ok := ctx.Value(anomalySourceKey{}).(string); ok && server != "" {
			return tenancy.Check(ctx, s.db, "anomaly.read", "server", server)
		}
	}
	if project != "" && vars.RunID != "" {
		var actual string
		if err := configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT project_id FROM pipeline_runs WHERE id=?`, vars.RunID).Scan(&actual); err != nil || actual != project {
			return tenancy.ErrForbidden
		}
		return tenancy.Check(ctx, s.db, "run.read", "run", vars.RunID)
	}
	return tenancy.RequireInstanceAdministrator(ctx, s.db)
}
func (s *service) routedContext(ctx context.Context, project, event, channel string, vars TemplateVars) (context.Context, error) {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return ctx, err
	}
	if err = s.eventSource(ctx, project, event, vars); err != nil {
		return nil, err
	}
	scope := project
	if scope != "" && !s.projectHasRoute(ctx, scope, event) {
		scope = ""
	}
	query := `SELECT id FROM notification_routes WHERE event=? AND channel_id=? AND enabled=1 AND project_id IS NULL`
	args := []any{event, channel}
	if scope != "" {
		query = `SELECT id FROM notification_routes WHERE event=? AND channel_id=? AND enabled=1 AND project_id=?`
		args = append(args, scope)
	}
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx, query+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		_, digest, e := s.routeVersion(ctx, configstate.Queryer(ctx, s.db), id)
		if e != nil {
			continue
		}
		approval, e := tenancy.AutomationContext(context.Background(), s.db, "notification", id, digest, func(c context.Context, q configstate.DBTX) (string, error) {
			_, value, e := s.routeVersion(c, q, id)
			return value, e
		})
		if e != nil {
			continue
		}
		ch, sealed, e := s.load(ctx, channel)
		if e != nil {
			return nil, e
		}
		return context.WithValue(ctx, deliveryKey{}, routedDelivery{channel: channel, version: channelVersion(ch, sealed), approval: approval, source: func(c context.Context) error { return s.eventSource(c, project, event, vars) }}), nil
	}
	return nil, tenancy.ErrForbidden
}
func (s *service) authorizeDelivery(ctx context.Context, ch *Channel, sealed []byte) error {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return err
	}
	if err = tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	if cap, ok := ctx.Value(deliveryKey{}).(routedDelivery); ok {
		if cap.channel != ch.ID || cap.version != channelVersion(ch, sealed) || cap.source == nil {
			return tenancy.ErrForbidden
		}
		if err = cap.source(ctx); err != nil {
			return err
		}
		approval := configstate.WithTxContext(cap.approval, s.db, configstate.Queryer(ctx, s.db))
		if err = tenancy.VerifyExecution(approval); err != nil {
			return err
		}
		return tenancy.RequireInstanceAdministrator(approval, s.db)
	}
	return tenancy.Check(ctx, s.db, "notification.send", "notification_channel", ch.ID)
}

// ResolveChannel exposes only the name and ID needed by a frozen notification
// node, with SQL scope before matching. Recipient configuration is never returned.
func (s *service) ResolveChannel(ctx context.Context, ref string) (string, string, error) {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "notification.read", "c.id")
	if err != nil {
		return "", "", err
	}
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT c.id,c.name FROM notification_channels c WHERE (c.id=? OR c.name=?) AND (`+predicate.SQL+`) ORDER BY c.id`, append([]any{ref, ref}, predicate.Args...)...)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var id, name string
	count := 0
	for rows.Next() {
		var nextID, nextName string
		if err = rows.Scan(&nextID, &nextName); err != nil {
			return "", "", err
		}
		if nextID == ref {
			return nextID, nextName, nil
		}
		id, name = nextID, nextName
		count++
	}
	if err = rows.Err(); err != nil {
		return "", "", err
	}
	if count != 1 {
		return "", "", ErrNotFound
	}
	return id, name, nil
}

// DeliveryVersion is a controller-only digest of the exact destination and
// encrypted secret. Its value is safe to freeze but is not an access token.
func DeliveryVersion(ctx context.Context, db *sql.DB, id string) (string, error) {
	if err := tenancy.Check(ctx, db, "notification.send", "notification_channel", id); err != nil {
		return "", err
	}
	ch, sealed, err := (&service{db: db}).load(ctx, id)
	if err != nil {
		return "", err
	}
	return channelVersion(ch, sealed), nil
}
func ResolveAllowedChannel(ctx context.Context, db *sql.DB, ref string) (string, string, error) {
	return (&service{db: db}).ResolveChannel(ctx, ref)
}

type ChannelOption struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

func (s *service) ListChannelOptions(ctx context.Context) ([]ChannelOption, error) {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, "notification.read", "c.id")
	if err != nil {
		return nil, err
	}
	rows, err := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT c.id,c.name,c.type,c.enabled FROM notification_channels c WHERE `+predicate.SQL+` ORDER BY c.name,c.id`, predicate.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChannelOption{}
	for rows.Next() {
		var option ChannelOption
		var enabled int
		if err = rows.Scan(&option.ID, &option.Name, &option.Type, &enabled); err != nil {
			return nil, err
		}
		option.Enabled = enabled == 1
		out = append(out, option)
	}
	return out, rows.Err()
}
