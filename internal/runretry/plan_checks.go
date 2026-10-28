package runretry

import (
	"context"
	"errors"
	"time"
)

type planImageChecksKey struct{}

func (s *Service) planCheckContext(ctx context.Context, id string) (context.Context, error) {
	if _, ok := ctx.Value(planImageChecksKey{}).(map[string]bool); ok {
		return ctx, nil
	}
	checks := map[string]bool{}
	l, err := s.load(ctx, id)
	if errors.Is(err, ErrUnavailable) {
		return context.WithValue(ctx, planImageChecksKey{}, checks), nil
	}
	if err != nil {
		return ctx, err
	}
	records, err := s.records(ctx, id)
	if err != nil {
		return ctx, err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, n := range l.graph.Nodes {
		r := records[n.Key]
		if r.status != "success" {
			continue
		}
		o, e := s.outcome(ctx, l, n, r)
		if e != nil {
			continue
		}
		for _, image := range o.Outputs.Images {
			key := digest(image)
			if _, exists := checks[key]; exists {
				continue
			}
			checks[key] = s.imageVerifier != nil && s.imageVerifier(probe, image) == nil
		}
	}
	return context.WithValue(ctx, planImageChecksKey{}, checks), nil
}
func imageChecked(ctx context.Context, image ImageRef) bool {
	checks, _ := ctx.Value(planImageChecksKey{}).(map[string]bool)
	return checks[digest(image)]
}
