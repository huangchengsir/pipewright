package previewenv

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"time"
)

type previewBinding struct {
	Version     int    `json:"version"`
	ProviderID  string `json:"providerId"`
	SourceRunID string `json:"sourceRunId,omitempty"`
}

// Old rows recover their provider from the actual original route, never from
// today's project default. Without reliable binding, reclamation fails closed.
func (s *service) fixedBinding(ctx context.Context, env *PreviewEnv) (previewBinding, error) {
	var binding previewBinding
	if env.bindingJSON != "" {
		if json.Unmarshal([]byte(env.bindingJSON), &binding) != nil || binding.Version != 1 || binding.ProviderID == "" {
			return binding, tenancy.ErrForbidden
		}
		return binding, nil
	}
	var body string
	if err := configstate.Queryer(ctx, s.store.db).QueryRowContext(ctx, `SELECT config FROM proxy_routes WHERE id=? AND server_id=? AND domain=?`, env.RouteID, env.ServerID, env.Subdomain).Scan(&body); err != nil {
		return binding, err
	}
	var route struct {
		ProviderID string `json:"dnsProviderId"`
	}
	if json.Unmarshal([]byte(body), &route) != nil || route.ProviderID == "" {
		return binding, tenancy.ErrForbidden
	}
	binding = previewBinding{Version: 1, ProviderID: route.ProviderID}
	return binding, nil
}

func (s *service) reclaimBound(ctx context.Context, env *PreviewEnv) error {
	binding, err := s.fixedBinding(ctx, env)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(binding)
	validate := func(c context.Context) error {
		if err := tenancy.Check(c, s.store.db, "preview.reclaim", "preview", env.ID); err != nil {
			return err
		}
		current, e := s.store.get(c, env.ID)
		if e != nil {
			return e
		}
		if current.Status != StatusActive || current.ProjectID != env.ProjectID || current.RouteID != env.RouteID || current.ServerID != env.ServerID || current.Subdomain != env.Subdomain {
			return tenancy.ErrConflict
		}
		if current.bindingJSON != "" && current.bindingJSON != string(body) {
			return tenancy.ErrConflict
		}
		return s.authorizeResources(c, env.ProjectID, binding.ProviderID, env.ServerID)
	}
	// Persist recovered legacy bindings before any external deletion, so retries
	// remain tied to the original provider even after its proxy route is removed.
	err = configstate.WithWriteTxContext(ctx, s.store.db, func(c context.Context, q configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, s.store.db); err != nil {
			return err
		}
		if err := validate(c); err != nil {
			return err
		}
		_, e := q.ExecContext(c, `UPDATE preview_envs SET binding_json=? WHERE id=?`, string(body), env.ID)
		return e
	})
	if err != nil {
		return err
	}
	ctx = tenancy.WithActionBoundary(ctx, validate)
	if s.recordDeleter == nil || s.routeDeleter == nil {
		return tenancy.ErrForbidden
	}
	if err = validate(ctx); err != nil {
		return err
	}
	if err = s.recordDeleter.DeleteRecord(ctx, binding.ProviderID, env.Subdomain); err != nil {
		return err
	}
	if err = validate(ctx); err != nil {
		return err
	}
	if err = s.routeDeleter.DeleteRoute(ctx, env.RouteID); err != nil {
		return err
	}
	return configstate.WithWriteTxContext(ctx, s.store.db, func(c context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, s.store.db); err != nil {
			return err
		}
		if err := validate(c); err != nil {
			return err
		}
		return s.store.markReclaimed(c, env.ID, time.Now().UTC())
	})
}
