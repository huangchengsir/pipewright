package configdraft

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"gopkg.in/yaml.v3"
	"strings"
)

// Config drafts persist only a randomized, project/node-bound ciphertext reference.
// No YAML, values, scripts, fixed variables or probe literals enter operations_json.
func (s *Service) sealKubernetes(d *document, ops []Operation) ([]Operation, error) {
	out := append([]Operation(nil), ops...)
	for i, o := range out {
		if sectionFor(o.Type) != "kubernetes" {
			continue
		}
		if s.opts.Kubernetes == nil || o.Kubernetes == nil {
			return nil, ErrUnavailable
		}
		if o.Kubernetes.ConfigRef != "" {
			if _, e := s.opts.Kubernetes.ReleaseDraftConfig(d.ctx, d.projectID, o.JobID, o.Kubernetes.ConfigRef); e != nil {
				return nil, ErrForbidden
			}
			continue
		}
		cfg := map[string]any{}
		if o.Type == "kubernetes.update" {
			_, j, e := node(d, o.StageID, o.JobID)
			if e != nil {
				return nil, e
			}
			if j.Type != pipeline.KubernetesJobType || !pipeline.IsKubernetesReleaseConfig(j.Config) {
				return nil, ErrUnsupported
			}
			for k, v := range j.Config {
				cfg[k] = v
			}
		}
		for k, v := range o.Kubernetes.Config {
			cfg[k] = v
		}
		if o.Type == "kubernetes.update" {
			_, j, _ := node(d, o.StageID, o.JobID)
			// Markers are meaningful only with the exact current node's private payload.
			if ref, ok := o.Kubernetes.Config["privateRef"]; ok && ref != j.Config["privateRef"] {
				return nil, ErrForbidden
			}
		}
		safe, e := s.opts.Kubernetes.PrepareReleaseDraftConfig(d.ctx, d.projectID, o.JobID, cfg)
		if e != nil {
			return nil, ErrInvalid
		}
		ref, ok := safe["privateRef"].(string)
		if !ok || ref == "" {
			return nil, ErrUnavailable
		}
		out[i].Kubernetes = &KubernetesFields{ConfigRef: ref}
	}
	return out, nil
}

func (s *Service) patchKubernetes(d *document, o Operation, g guard) (Diff, error) {
	fail := Diff{}
	if s.opts.Kubernetes == nil || o.Kubernetes == nil || o.Kubernetes.ConfigRef == "" || len(o.Kubernetes.Config) > 0 {
		return fail, ErrInvalid
	}
	cfg, e := s.opts.Kubernetes.ReleaseDraftConfig(d.ctx, d.projectID, o.JobID, o.Kubernetes.ConfigRef)
	if e != nil {
		return fail, ErrForbidden
	}
	if _, e = pipeline.ParseKubernetesReleaseConfig(cfg); e != nil {
		return fail, ErrInvalid
	}
	var before map[string]any
	beforeName, afterName := "", ""
	if o.Type == "kubernetes.add" {
		st, _, e := node(d, o.StageID, "")
		if e != nil {
			return fail, e
		}
		var typed pipeline.Stage
		b, _ := json.Marshal(st)
		if json.Unmarshal(b, &typed) != nil {
			return fail, ErrUnsupported
		}
		if typed.Kind != pipeline.KindDeploy || len(typed.Jobs) != 0 || typed.AllowFailure || len(typed.Matrix) != 0 || len(typed.Post) != 0 || len(typed.Services) != 0 {
			return fail, ErrTopology
		}
		for _, stage := range d.spec.Stages {
			if stage.ID == o.JobID {
				return fail, ErrInvalid
			}
			for _, j := range stage.Jobs {
				if j.ID == o.JobID {
					return fail, ErrInvalid
				}
			}
		}
		if o.Value == nil {
			return fail, ErrInvalid
		}
		afterName = *o.Value
		st["jobs"] = []any{map[string]any{"id": o.JobID, "name": *o.Value, "type": pipeline.KubernetesJobType, "config": cfg}}
	} else {
		n, j, e := node(d, o.StageID, o.JobID)
		if e != nil {
			return fail, e
		}
		if j.Type != pipeline.KubernetesJobType || !pipeline.IsKubernetesReleaseConfig(j.Config) {
			return fail, ErrUnsupported
		}
		before = j.Config
		beforeName = j.Name
		afterName = j.Name
		if o.Value != nil {
			afterName = *o.Value
		}
		n["config"] = cfg
		if o.Value != nil {
			n["name"] = *o.Value
		}
	}
	summary := func(cfg map[string]any, name string) string {
		if cfg == nil {
			return ""
		}
		b, _ := json.Marshal(map[string]any{"name": g.safe(name), "configuration": kubernetesSummary(cfg, g)})
		return string(b)
	}
	previous, next := summary(before, beforeName), summary(cfg, afterName)
	if previous == next {
		return Diff{}, nil
	}
	return Diff{Type: o.Type, StageID: o.StageID, JobID: o.JobID, Field: "kubernetes", Before: previous, After: next, Impact: "kubernetes"}, nil
}

// Metadata is deliberately an allowlist: the MCP projection never parses or
// exports inline manifests, scripts, environment values, values or probe inputs.
func kubernetesSummary(cfg map[string]any, g guard) string {
	if cfg == nil {
		return ""
	}
	out := map[string]any{}
	for _, key := range []string{"version", "mode", "connectionId", "credentialId", "context", "namespace", "executor", "workload", "progressive", "timeoutSeconds", "observeSeconds", "privateRef"} {
		if v, ok := cfg[key]; ok {
			out[key] = v
		}
	}
	object := func(v any) map[string]any {
		if raw, ok := v.(string); ok {
			var m map[string]any
			_ = json.Unmarshal([]byte(raw), &m)
			return m
		}
		m, _ := v.(map[string]any)
		return m
	}
	source := func(v any) map[string]any {
		m := object(v)
		safe := map[string]any{}
		for _, key := range []string{"kind", "path", "artifact"} {
			if x, ok := m[key]; ok {
				safe[key] = x
			}
		}
		if m["kind"] == "inline" {
			safe["contentWithheld"] = true
		}
		return safe
	}
	if cfg["source"] != nil {
		out["source"] = source(cfg["source"])
	}
	if m := object(cfg["helm"]); m != nil {
		out["helm"] = map[string]any{"release": m["release"], "hooks": m["hooks"], "values": source(m["values"])}
	}
	for _, key := range []string{"variables", "checks"} {
		v := cfg[key]
		if raw, ok := v.(string); ok {
			_ = json.Unmarshal([]byte(raw), &v)
		}
		rows, _ := v.([]any)
		safe := []any{}
		for _, row := range rows {
			m := object(row)
			x := map[string]any{}
			keys := []string{"name", "type", "source", "ref"}
			if key == "checks" {
				keys = []string{"kind", "name", "namespace", "resource", "restart", "protocol", "waitSeconds"}
			}
			for _, k := range keys {
				if val, ok := m[k]; ok {
					x[k] = val
				}
			}
			safe = append(safe, x)
		}
		if len(safe) > 0 {
			out[key] = safe
		}
	}
	b, _ := json.Marshal(out)
	return g.scrub(string(b))
}

func (s *Service) kubernetesReview(ctx context.Context, pid string, ops []Operation, g guard) ([]KubernetesReview, error) {
	out := []KubernetesReview{}
	for _, o := range ops {
		if sectionFor(o.Type) != "kubernetes" {
			continue
		}
		cfg, e := s.opts.Kubernetes.ReleaseReviewConfig(ctx, pid, o.JobID, o.Kubernetes.ConfigRef)
		if e != nil {
			return nil, ErrForbidden
		}
		b, e := yaml.Marshal(cfg)
		if e != nil {
			return nil, ErrUnavailable
		}
		review := KubernetesReview{StageID: o.StageID, JobID: o.JobID, After: g.scrub(string(b))}
		if o.Type == "kubernetes.update" {
			d, e := s.load(ctx, configstate.Queryer(ctx, s.db), pid)
			if e != nil {
				return nil, e
			}
			_, j, e := node(d, o.StageID, o.JobID)
			if e != nil {
				return nil, e
			}
			if ref, ok := j.Config["privateRef"].(string); ok {
				old, e := s.opts.Kubernetes.ReleaseReviewConfig(ctx, pid, o.JobID, ref)
				if e != nil {
					return nil, e
				}
				oldYAML, e := yaml.Marshal(old)
				if e != nil {
					return nil, ErrUnavailable
				}
				review.Before = g.scrub(string(oldYAML))
			}
		}
		out = append(out, review)
	}
	b, _ := json.Marshal(out)
	if len(b) > 128<<10 || strings.ContainsRune(string(b), 0) {
		return nil, ErrLarge
	}
	return out, nil
}

// Metadata only: publishing this requirement neither grants it nor contacts a
// cluster. Actual permission/capability checks remain in release preparation.
func daemonSetProofOptions() map[string]any {
	return map[string]any{"version": 1, "anchorKind": "DaemonSet", "requiredAction": "related-controllerrevisions", "helperCapability": "daemonset-controllerrevision-read-v1", "nativeResource": "apps/v1/controllerrevisions", "ownerBound": true, "revisionDataPrivate": true, "nodeReadsRequired": false, "oldGrantsExpanded": false}
}
