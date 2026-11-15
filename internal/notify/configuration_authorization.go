package notify

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// Configuration is global: team grants must not expose recipient addresses or
// delegate global channel secrets. Delivery principals are checked separately.
func (s *service) AuthorizeConfiguration(ctx context.Context) error {
	return tenancy.RequireInstanceAdministrator(ctx, s.db)
}
func (s *service) authorizeProject(ctx context.Context, id, action string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return tenancy.Check(ctx, s.db, action, "project", id)
}
func configWrite[T any](ctx context.Context, s *service, fn func(context.Context) (T, error)) (T, error) {
	var out T
	err := configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockAuthorization(txctx, s.db); err != nil {
			return err
		}
		if err := s.AuthorizeConfiguration(txctx); err != nil {
			return err
		}
		var err error
		out, err = fn(txctx)
		return err
	})
	return out, err
}
func (s *service) Create(ctx context.Context, in CreateInput) (*Channel, error) {
	return configWrite(ctx, s, func(c context.Context) (*Channel, error) {
		ch, e := s.create(c, in)
		if e != nil {
			return nil, e
		}
		if e = tenancy.RecordOwnership(c, s.db, "notification_channel", ch.ID, ""); e != nil {
			return nil, e
		}
		return ch, nil
	})
}
func (s *service) Update(ctx context.Context, id string, in UpdateInput) (*Channel, error) {
	return configWrite(ctx, s, func(c context.Context) (*Channel, error) {
		ch, e := s.update(c, id, in)
		if e != nil {
			return nil, e
		}
		if e = s.approveChannelRoutes(c, id); e != nil {
			return nil, e
		}
		return ch, nil
	})
}
func (s *service) Delete(ctx context.Context, id string) error {
	_, err := configWrite(ctx, s, func(c context.Context) (struct{}, error) { return struct{}{}, s.delete(c, id) })
	return err
}
func (s *service) SetNotifyLanguage(ctx context.Context, lang string) error {
	_, err := configWrite(ctx, s, func(c context.Context) (struct{}, error) { return struct{}{}, s.setNotifyLanguage(c, lang) })
	return err
}
func (s *service) CreateRoute(ctx context.Context, in CreateRouteInput) (*Route, error) {
	return configWrite(ctx, s, func(c context.Context) (*Route, error) {
		if err := s.authorizeProject(c, in.ProjectID, "pipeline.edit"); err != nil {
			return nil, err
		}
		route, e := s.createRoute(c, in)
		if e != nil {
			return nil, e
		}
		if e = s.approveRoute(c, route.ID); e != nil {
			return nil, e
		}
		return route, nil
	})
}
func (s *service) DeleteRoute(ctx context.Context, id string) error {
	_, err := configWrite(ctx, s, func(c context.Context) (struct{}, error) {
		var project sql.NullString
		if err := configstate.Queryer(c, s.db).QueryRowContext(c, `SELECT project_id FROM notification_routes WHERE id=?`, id).Scan(&project); err != nil {
			if err == sql.ErrNoRows {
				return struct{}{}, ErrRouteNotFound
			}
			return struct{}{}, err
		}
		if err := s.authorizeProject(c, project.String, "pipeline.edit"); err != nil {
			return struct{}{}, err
		}
		if e := tenancy.RevokeAutomation(c, s.db, "notification", id); e != nil {
			return struct{}{}, e
		}
		return struct{}{}, s.deleteRoute(c, id)
	})
	return err
}
func (s *service) CreateTemplate(ctx context.Context, in CreateTemplateInput) (*Template, error) {
	return configWrite(ctx, s, func(c context.Context) (*Template, error) {
		if err := s.authorizeProject(c, in.ProjectID, "pipeline.edit"); err != nil {
			return nil, err
		}
		return s.createTemplate(c, in)
	})
}
func (s *service) UpdateTemplate(ctx context.Context, id string, in UpdateTemplateInput) (*Template, error) {
	return configWrite(ctx, s, func(c context.Context) (*Template, error) {
		old, err := s.loadTemplate(c, id)
		if err != nil {
			return nil, err
		}
		if err = s.authorizeProject(c, old.ProjectID, "pipeline.edit"); err != nil {
			return nil, err
		}
		return s.updateTemplate(c, id, in)
	})
}
func (s *service) DeleteTemplate(ctx context.Context, id string) error {
	_, err := configWrite(ctx, s, func(c context.Context) (struct{}, error) {
		old, err := s.loadTemplate(c, id)
		if err != nil {
			return struct{}{}, err
		}
		if err = s.authorizeProject(c, old.ProjectID, "pipeline.edit"); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.deleteTemplate(c, id)
	})
	return err
}
