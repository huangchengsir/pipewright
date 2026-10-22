package configdraft

import (
	"context"
	"encoding/json"
	"time"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

func (s *Service) Snapshot(ctx context.Context, tokenID string, in SnapshotRequest) (*Snapshot, error) {
	if !contains(Sections, in.Section) || in.ProjectID == "" {
		return nil, ErrInvalid
	}
	if in.BeginSnapshot {
		if in.Cursor != "" || in.ExpectedBaseRevision != "" || in.ExpectedPolicyVersion != "" {
			return nil, ErrInvalid
		}
	} else if in.ExpectedBaseRevision == "" || in.ExpectedPolicyVersion == "" {
		return nil, ErrInvalid
	}
	size, e := pageSize(in.PageSize)
	if e != nil {
		return nil, e
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	var out *Snapshot
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		t, e := s.locks(ctx, q, tokenID, g)
		if e != nil {
			return e
		}
		if !allowed(t, in.ProjectID, "read", in.Section, "") {
			return ErrForbidden
		}
		d, e := s.load(ctx, q, in.ProjectID)
		if e != nil {
			return e
		}
		if !in.BeginSnapshot && (revision(d.rev) != in.ExpectedBaseRevision || d.policy != in.ExpectedPolicyVersion) {
			return ErrStale
		}
		out = s.snapshotMetadata(t, d, in.Section)
		items, e := s.sectionItems(t, d, g, in.Section)
		if e != nil {
			return e
		}
		c := pageCursor{Token: tokenID, Project: in.ProjectID, Section: in.Section, Revision: out.BaseRevision, Policy: out.PolicyVersion, Authz: t.AuthzRevision}
		offset, e := s.offset(in.Cursor, c)
		if e != nil {
			return e
		}
		if offset > len(items) {
			return ErrInvalid
		}
		end := offset + size
		if end > len(items) {
			end = len(items)
		}
		out.Items = items[offset:end]
		if end < len(items) {
			c.Offset = end
			out.NextCursor = s.cursor(c)
		}
		if e = boundedOutput(out); e != nil {
			return e
		}
		return s.audit(ctx, q, tokenID, "mcp_config_read", in.ProjectID, "", []string{in.Section})
	})
	if e != nil {
		return nil, e
	}
	if e = s.fresh(ctx, tokenID, g); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) snapshotMetadata(t *auth.MCPToken, d *document, section string) *Snapshot {
	ops := []string{}
	if d.applicability == "supported" {
		for _, op := range OperationTypes {
			if allowed(t, d.projectID, "read", sectionFor(op), op) && allowed(t, d.projectID, "propose", sectionFor(op), op) {
				ops = append(ops, op)
			}
		}
	}
	return &Snapshot{ProjectID: d.projectID, Section: section, BaseRevision: revision(d.rev), PolicyVersion: d.policy, AuthzRevision: t.AuthzRevision, RunnerMode: d.runner, ExecutorKind: d.executor, SourceMode: d.source, ExecutionApplicability: d.applicability, EditableOperations: ops, ObservedAt: s.opts.Now().UTC().Format(time.RFC3339Nano), Items: []map[string]any{}, Missing: !d.stored || d.applicability == "missing", UntrustedData: true}
}
func (s *Service) sectionItems(t *auth.MCPToken, d *document, g guard, section string) ([]map[string]any, error) {
	items := []map[string]any{}
	switch section {
	case "project":
		items = append(items, map[string]any{"name": g.safe(d.name), "defaultBranch": g.safe(d.branch), "hiddenFieldsPreserved": true})
	case "source_and_impact":
		items = append(items, map[string]any{"sourceMode": d.source, "executionApplicability": d.applicability, "automaticTriggersMayUseChanges": true, "networkChecksPerformed": false, "scriptsExported": false})
	default:
		for _, st := range d.spec.Stages {
			if !g.clean(st.ID) {
				return nil, ErrSecret
			}
			if section == "pipeline" {
				items = append(items, map[string]any{"stageId": st.ID, "name": g.safe(st.Name), "kind": safeKind(st.Kind), "gate": st.Gate, "topologyReadOnly": true, "hiddenFieldsPreserved": true})
			}
			for _, j := range st.Jobs {
				if !g.clean(j.ID) {
					return nil, ErrSecret
				}
				kind := build.ConfigJobKind(j.Type)
				x := map[string]any{"stageId": st.ID, "jobId": j.ID, "name": g.safe(j.Name), "kind": kind, "hiddenFieldsPreserved": true}
				if section == "pipeline" {
					items = append(items, x)
					continue
				}
				if section == "step_limits" && kind == "script" {
					for _, k := range []string{"timeoutSeconds", "retries", "cpu", "memory"} {
						if v, ok := j.Config[k]; ok {
							x[k] = safeScalar(g, v)
						}
					}
					items = append(items, x)
				}
				if section == "build" && kind == "build_image" {
					for _, k := range []string{"buildModel", "toolchainLanguage", "toolchainVersion", "dockerfilePath", "context", "artifactType"} {
						if v, ok := j.Config[k]; ok {
							x[k] = safeScalar(g, v)
						}
					}
					x["artifactTypeReadOnly"] = true
					x["buildModelReadOnly"] = true
					items = append(items, x)
				}
			}
		}
	}
	return items, nil
}
func safeScalar(g guard, v any) any {
	switch x := v.(type) {
	case string:
		return g.safe(x)
	case json.Number:
		if n, e := x.Int64(); e == nil {
			return n
		}
		return "unsupported"
	case float64:
		return x
	case int:
		return x
	case bool:
		return x
	case nil:
		return nil
	}
	return "unsupported"
}
func safeKind(v string) string {
	switch v {
	case "source", "build", "deploy", "notify", "custom":
		return v
	}
	return "unsupported"
}
func boundedOutput(v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return ErrUnavailable
	}
	if len(b) > 30<<10 {
		return ErrLarge
	}
	return nil
}

func (s *Service) OptionsList(ctx context.Context, tokenID string, in OptionsRequest) (*Snapshot, error) {
	if !contains([]string{"operations", "node_targets", "build_enums", "resource_limits"}, in.Kind) || in.ExpectedBaseRevision == "" || in.ExpectedPolicyVersion == "" {
		return nil, ErrInvalid
	}
	size, e := pageSize(in.PageSize)
	if e != nil {
		return nil, e
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	var out *Snapshot
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		t, e := s.locks(ctx, q, tokenID, g)
		if e != nil {
			return e
		}
		if !allowed(t, in.ProjectID, "read", "", "") {
			return ErrForbidden
		}
		d, e := s.load(ctx, q, in.ProjectID)
		if e != nil {
			return e
		}
		if revision(d.rev) != in.ExpectedBaseRevision || d.policy != in.ExpectedPolicyVersion {
			return ErrStale
		}
		out = s.snapshotMetadata(t, d, "options")
		items := []map[string]any{}
		switch in.Kind {
		case "operations":
			for _, op := range OperationTypes {
				if allowed(t, d.projectID, "read", sectionFor(op), op) {
					items = append(items, map[string]any{"type": op, "section": sectionFor(op), "editable": contains(out.EditableOperations, op)})
				}
			}
		case "node_targets":
			for _, st := range d.spec.Stages {
				for _, j := range st.Jobs {
					if !g.clean(st.ID) || !g.clean(j.ID) {
						return ErrSecret
					}
					kind := build.ConfigJobKind(j.Type)
					sec := "pipeline"
					if kind == "script" {
						sec = "step_limits"
					}
					if kind == "build_image" {
						sec = "build"
					}
					if allowed(t, d.projectID, "read", sec, "") {
						items = append(items, map[string]any{"stageId": st.ID, "jobId": j.ID, "kind": kind})
					}
				}
			}
		case "build_enums":
			if !allowed(t, d.projectID, "read", "build", "") {
				return ErrForbidden
			}
			items = append(items, map[string]any{"field": "toolchainLanguage", "values": []string{"maven", "node", "golang", "gradle", "python"}}, map[string]any{"field": "buildModel", "readOnly": true})
		case "resource_limits":
			if !allowed(t, d.projectID, "read", "step_limits", "") {
				return ErrForbidden
			}
			items = append(items, map[string]any{"timeoutSecondsMin": 1, "timeoutSecondsMax": 3600, "retriesMin": 0, "retriesMax": 3, "maxCPU": s.opts.MaxCPU, "maxMemoryBytes": s.opts.MaxMemoryBytes})
		}
		c := pageCursor{Token: tokenID, Project: in.ProjectID, Section: "options:" + in.Kind, Revision: out.BaseRevision, Policy: out.PolicyVersion, Authz: t.AuthzRevision}
		offset, e := s.offset(in.Cursor, c)
		if e != nil {
			return e
		}
		if offset > len(items) {
			return ErrInvalid
		}
		end := offset + size
		if end > len(items) {
			end = len(items)
		}
		out.Items = items[offset:end]
		if end < len(items) {
			c.Offset = end
			out.NextCursor = s.cursor(c)
		}
		if e = boundedOutput(out); e != nil {
			return e
		}
		return s.audit(ctx, q, tokenID, "mcp_config_options", in.ProjectID, "", []string{in.Kind})
	})
	if e != nil {
		return nil, e
	}
	if e = s.fresh(ctx, tokenID, g); e != nil {
		return nil, e
	}
	return out, nil
}
