package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/huangchengsir/pipewright/internal/onboarding"
)

type onboardingStatusReader interface {
	Status(ctx context.Context, preferredProjectID string) onboarding.Snapshot
}

// WithOnboarding installs a read-only snapshot provider; no execution services are passed to it.
func WithOnboarding(s onboardingStatusReader) Option {
	return func(o *options) { o.onboarding = s }
}

func makeOnboardingStatusHandler(s onboardingStatusReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			writeError(w, http.StatusServiceUnavailable, "onboarding_unavailable", "Onboarding status is unavailable")
			return
		}
		writeJSON(w, http.StatusOK, s.Status(r.Context(), strings.TrimSpace(r.URL.Query().Get("projectId"))))
	}
}
