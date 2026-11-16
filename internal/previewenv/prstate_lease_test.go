package previewenv

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"testing"
)

type leaseResolver struct {
	revoked  bool
	releases int
	acquired int
}

func (r *leaseResolver) Resolve(context.Context, string) (string, string, error) {
	panic("production lease must not fall back")
}
func (r *leaseResolver) Acquire(ctx context.Context, _ string) (context.Context, string, string, func(), error) {
	r.acquired++
	return tenancy.WithActionBoundary(ctx, func(context.Context) error {
		if r.revoked {
			return tenancy.ErrForbidden
		}
		return nil
	}), "https://github.com/acme/app", "test-fixture-token", func() { r.releases++ }, nil
}
func TestPRCheckerReleasesLeaseAndDiscardsRevokedResult(t *testing.T) {
	for _, timing := range []string{"before", "during", "none"} {
		t.Run(timing, func(t *testing.T) {
			r := &leaseResolver{revoked: timing == "before"}
			calls := 0
			checker := newCheckerWithServer(t, r, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if timing == "during" {
					r.revoked = true
				}
				_, _ = w.Write([]byte(`{"state":"closed"}`))
			})
			got, e := checker.State(context.Background(), "p1", 1)
			want := PRStateUnknown
			if timing == "none" {
				want = PRStateClosed
			}
			if e != nil || got != want || r.acquired != 1 || r.releases != 1 {
				t.Fatal(got, e, r.acquired, r.releases)
			}
			if timing == "before" && calls != 0 {
				t.Fatal("request sent after rejection")
			}
		})
	}
}
