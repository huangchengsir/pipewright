package productrelease

import (
	"context"
	"errors"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// ErrUnsupported distinguishes a pipeline that cannot yet produce a
// verifiable frozen deployment from an invalid or unauthorized request.
var ErrUnsupported = errors.New("productrelease: pipeline lacks verifiable frozen deployment")

type PreviewItem struct {
	ApplicationID string `json:"applicationId"`
	Branch        string `json:"branch,omitempty"`
	CommitSHA     string `json:"commitSha,omitempty"`
	Environment   string `json:"environment"`
}
type TargetOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type EnvironmentOption struct {
	Name      string         `json:"name"`
	TargetIDs []string       `json:"targetIds"`
	Targets   []TargetOption `json:"targets,omitempty"`
}
type ApplicationOption struct {
	ApplicationID string              `json:"applicationId"`
	Name          string              `json:"name"`
	DefaultBranch string              `json:"defaultBranch"`
	Environments  []EnvironmentOption `json:"environments"`
	CanRelease    bool                `json:"canRelease"`
	Reason        string              `json:"reason,omitempty"`
}
type Options struct {
	Applications []ApplicationOption `json:"applications"`
	CanRelease   bool                `json:"canRelease"`
	HasMore      bool                `json:"hasMore"`
}

// Preparation is implemented only by the real controller. Neither the body nor
// a client-supplied authority can install a preview or options implementation.
type Preparation interface {
	Options(context.Context, string, int) (Options, error)
	Preview(context.Context, string, []PreviewItem) (Plan, error)
}

func (s *Service) Options(ctx context.Context, product string, page int) (Options, error) {
	if _, err := s.principal(ctx); err != nil {
		return Options{}, err
	}
	if !validID(product) {
		return Options{}, ErrNotFound
	}
	preparer, ok := s.executor.(Preparation)
	if !ok {
		return Options{}, ErrUnavailable
	}
	if page < 1 || page > 1_000_000 {
		return Options{}, ErrInvalid
	}
	return preparer.Options(ctx, product, page)
}
func (s *Service) Preview(ctx context.Context, product string, items []PreviewItem) (Plan, error) {
	if _, err := s.principal(ctx); err != nil {
		return Plan{}, err
	}
	preparer, ok := s.executor.(Preparation)
	if !ok {
		return Plan{}, ErrUnavailable
	}
	if !validID(product) || len(items) == 0 || len(items) > MaxItems {
		return Plan{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, i := range items {
		if !validID(i.ApplicationID) || seen[i.ApplicationID] || (i.CommitSHA != "" && !validHash(i.CommitSHA, 40)) || len(i.Branch) > 255 || i.Environment == "" || len(i.Environment) > 128 {
			return Plan{}, ErrInvalid
		}
		seen[i.ApplicationID] = true
	}
	out, err := preparer.Preview(ctx, product, items)
	if err != nil {
		return Plan{}, err
	}
	if out.ProductID != product || len(out.Items) != len(items) || validate(out) != nil {
		return Plan{}, ErrConflict
	}
	return out, nil
}

type BatchPage struct {
	Items   []*Batch `json:"items"`
	HasMore bool     `json:"hasMore"`
}

const batchPageSize = 50

// List scopes records to their initiator and checks current application and run
// access on every page. The extra row is used only to decide whether to page.
func (s *Service) List(ctx context.Context, product string, page int) (BatchPage, error) {
	out := BatchPage{Items: []*Batch{}}
	p, err := s.principal(ctx)
	if err != nil {
		return out, err
	}
	if !validID(product) {
		return out, ErrNotFound
	}
	if page < 1 || page > 1_000_000 {
		return out, ErrInvalid
	}
	if err = s.authority.Authorize(ctx, product, nil, false); err != nil {
		return out, err
	}
	suffix, pagination := store.Page(store.DialectOf(s.db), batchPageSize+1, (page-1)*batchPageSize)
	args := append([]any{p.TenantID, p.ActorID, product}, pagination...)
	q := configstate.Queryer(ctx, s.db)
	rows, err := q.QueryContext(ctx, `SELECT `+columns+` FROM product_release_batches WHERE tenant_id=? AND actor_id=? AND product_id=? ORDER BY created_at DESC,id DESC`+suffix, args...)
	if err != nil {
		return out, err
	}
	batches := make([]*Batch, 0, batchPageSize)
	for rows.Next() {
		if len(batches) == batchPageSize {
			out.HasMore = true
			break
		}
		b, err := scan(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		batches = append(batches, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, b := range batches {
		if _, err = s.authorized(ctx, b, false); errors.Is(err, tenancy.ErrForbidden) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return BatchPage{}, err
		}
		out.Items = append(out.Items, b)
	}
	return out, nil
}

type legacySessionKey struct{}

// WithLegacySession is called by the authenticated browser transport only.
func WithLegacySession(ctx context.Context) context.Context {
	return context.WithValue(ctx, legacySessionKey{}, true)
}
func HasLegacySession(ctx context.Context) bool {
	ok, _ := ctx.Value(legacySessionKey{}).(bool)
	return ok
}
