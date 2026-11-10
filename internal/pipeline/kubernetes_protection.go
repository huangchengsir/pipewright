package pipeline

import (
	"context"
	"encoding/json"
)

// ReleaseConfigProtector keeps private Kubernetes payloads outside ordinary
// spec_json/spec_yaml and MCP drafts. References are scoped to project/job IDs.
type ReleaseConfigProtector interface {
	ProtectReleaseConfig(context.Context, string, string, map[string]any) (map[string]any, error)
	ResolveReleaseConfig(context.Context, string, string, map[string]any) (map[string]any, error)
}
type ServiceOption func(*service)

func WithReleaseConfigProtector(p ReleaseConfigProtector) ServiceOption {
	return func(s *service) { s.releaseProtector = p }
}
func (s *service) protectReleaseConfigs(ctx context.Context, project string, spec *Spec) error {
	for i := range spec.Stages {
		for j := range spec.Stages[i].Jobs {
			job := &spec.Stages[i].Jobs[j]
			if job.Type != KubernetesJobType || !IsKubernetesReleaseConfig(job.Config) {
				continue
			}
			if s.releaseProtector == nil {
				return ErrProtectedCredential
			}
			protected, e := s.releaseProtector.ProtectReleaseConfig(ctx, project, job.ID, job.Config)
			if e != nil {
				return e
			}
			job.Config = protected
		}
	}
	return nil
}

// ResolveReleaseConfigs is used only by trusted execution/capture adapters, never
// by public configuration snapshots. PAC inline values need no instance reference
// and remain inside the encrypted runtime capture.
func ResolveReleaseConfigs(ctx context.Context, project string, in Spec, p ReleaseConfigProtector) (Spec, error) {
	b, e := json.Marshal(in)
	if e != nil {
		return Spec{}, ErrKubernetesConfig
	}
	var out Spec
	if json.Unmarshal(b, &out) != nil {
		return out, ErrKubernetesConfig
	}
	for i := range out.Stages {
		for j := range out.Stages[i].Jobs {
			job := &out.Stages[i].Jobs[j]
			if job.Type != KubernetesJobType || !IsKubernetesReleaseConfig(job.Config) {
				continue
			}
			if p == nil {
				return out, ErrProtectedCredential
			}
			job.Config, e = p.ResolveReleaseConfig(ctx, project, job.ID, job.Config)
			if e != nil {
				return out, e
			}
		}
	}
	return out, nil
}
