package configdraft

import (
	"bytes"
	"encoding/json"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

var cpuSyntax = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,3})?$`)
var memorySyntax = regexp.MustCompile(`^([0-9]+)([kKmMgG]?)$`)
var versionSyntax = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (s *Service) normalize(in []Operation, g guard) ([]Operation, error) {
	if len(in) < 1 || len(in) > 32 {
		return nil, ErrInvalid
	}
	b, e := json.Marshal(in)
	if e != nil || len(b) > 16384 {
		return nil, Error("budget_exceeded")
	}
	var ops []Operation
	if json.Unmarshal(b, &ops) != nil {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	leaves := 0
	for i := range ops {
		o := &ops[i]
		if !contains(OperationTypes, o.Type) {
			return nil, ErrInvalid
		}
		sec := sectionFor(o.Type)
		_ = sec
		if strings.HasPrefix(o.Type, "project.") {
			if o.StageID != "" || o.JobID != "" {
				return nil, ErrInvalid
			}
		} else if o.StageID == "" || !validText(o.StageID, 128) || !g.clean(o.StageID) {
			return nil, ErrInvalid
		}
		if o.Type == "stage.rename" {
			if o.JobID != "" {
				return nil, ErrInvalid
			}
		} else if !strings.HasPrefix(o.Type, "project.") && (o.JobID == "" || !validText(o.JobID, 128) || !g.clean(o.JobID)) {
			return nil, ErrInvalid
		}
		var fields []string
		switch o.Type {
		case "project.set_name", "stage.rename", "job.rename", "project.set_default_branch":
			if o.Value == nil || o.Limits != nil || o.Build != nil {
				return nil, ErrInvalid
			}
			v := strings.TrimSpace(*o.Value)
			max := 128
			if o.Type == "project.set_default_branch" {
				max = 255
			}
			if !validText(v, max) || !g.clean(v) {
				return nil, ErrInvalid
			}
			if o.Type == "project.set_default_branch" && plumbing.NewBranchReferenceName(v).Validate() != nil {
				return nil, ErrInvalid
			}
			o.Value = &v
			field := "name"
			if o.Type == "project.set_default_branch" {
				field = "defaultBranch"
			}
			fields = []string{field}
		case "step.set_limits":
			if o.Limits == nil || o.Build != nil || o.Value != nil {
				return nil, ErrInvalid
			}
			l := o.Limits
			if l.TimeoutSeconds != nil {
				if *l.TimeoutSeconds < 1 || *l.TimeoutSeconds > 3600 {
					return nil, ErrInvalid
				}
				fields = append(fields, "timeoutSeconds")
			}
			if l.Retries != nil {
				if *l.Retries < 0 || *l.Retries > 3 {
					return nil, ErrInvalid
				}
				fields = append(fields, "retries")
			}
			if l.CPU != nil {
				v := strings.TrimSpace(*l.CPU)
				n, e := strconv.ParseFloat(v, 64)
				if !cpuSyntax.MatchString(v) || e != nil || n <= 0 || n > s.opts.MaxCPU || !g.clean(v) {
					return nil, ErrInvalid
				}
				l.CPU = &v
				fields = append(fields, "cpu")
			}
			if l.Memory != nil {
				v := strings.ToLower(strings.TrimSpace(*l.Memory))
				m := memorySyntax.FindStringSubmatch(v)
				if m == nil {
					return nil, ErrInvalid
				}
				n, e := strconv.ParseInt(m[1], 10, 64)
				if e != nil || n < 1 {
					return nil, ErrInvalid
				}
				factor := int64(1)
				switch m[2] {
				case "k":
					factor = 1 << 10
				case "m":
					factor = 1 << 20
				case "g":
					factor = 1 << 30
				}
				if n > s.opts.MaxMemoryBytes/factor || !g.clean(v) {
					return nil, ErrInvalid
				}
				l.Memory = &v
				fields = append(fields, "memory")
			}
		case "build.set":
			if o.Build == nil || o.Limits != nil || o.Value != nil {
				return nil, ErrInvalid
			}
			z := o.Build
			values := []struct {
				k string
				p **string
			}{{"buildModel", &z.BuildModel}, {"toolchainLanguage", &z.ToolchainLanguage}, {"toolchainVersion", &z.ToolchainVersion}, {"dockerfilePath", &z.DockerfilePath}, {"context", &z.Context}}
			for _, x := range values {
				if *x.p == nil {
					continue
				}
				v := strings.TrimSpace(**x.p)
				if !g.clean(v) {
					return nil, ErrInvalid
				}
				switch x.k {
				case "buildModel":
					if !contains([]string{"dockerfile", "toolchain"}, v) {
						return nil, ErrInvalid
					}
				case "toolchainLanguage":
					if !contains([]string{"maven", "node", "golang", "gradle", "python"}, v) {
						return nil, ErrInvalid
					}
				case "toolchainVersion":
					if !versionSyntax.MatchString(v) {
						return nil, ErrInvalid
					}
				case "dockerfilePath", "context":
					if !relativePath(v) {
						return nil, ErrInvalid
					}
					v = path.Clean(v)
				}
				*x.p = &v
				fields = append(fields, x.k)
			}
		}
		if len(fields) == 0 {
			return nil, ErrInvalid
		}
		for _, f := range fields {
			key := o.StageID + "\x00" + o.JobID + "\x00" + f
			if strings.HasPrefix(o.Type, "project.") {
				key = "project\x00" + f
			}
			if seen[key] {
				return nil, ErrInvalid
			}
			seen[key] = true
			leaves++
			if leaves > 32 {
				return nil, Error("budget_exceeded")
			}
		}
	}
	b, _ = json.Marshal(ops)
	if len(b) > 16384 {
		return nil, Error("budget_exceeded")
	}
	return ops, nil
}
func relativePath(v string) bool {
	if v == "" || len(v) > 255 || strings.ContainsAny(v, "\\:\x00") || strings.HasPrefix(v, "/") || !validText(v, 255) {
		return false
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
func cloneDocument(d *document) (*document, error) {
	c := *d
	c.raw = nil
	b, e := json.Marshal(d.raw)
	if e != nil {
		return nil, e
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&c.raw); e != nil {
		return nil, e
	}
	return &c, nil
}
func decodeSpec(d *document) error {
	b, e := json.Marshal(d.raw)
	if e != nil {
		return e
	}
	if json.Unmarshal(b, &d.spec) != nil {
		return ErrUnsupported
	}
	return nil
}
func node(d *document, stageID, jobID string) (map[string]any, pipeline.Job, error) {
	stages, ok := d.raw["stages"].([]any)
	if !ok {
		return nil, pipeline.Job{}, ErrUnsupported
	}
	for _, v := range stages {
		st, ok := v.(map[string]any)
		if !ok {
			return nil, pipeline.Job{}, ErrUnsupported
		}
		if st["id"] != stageID {
			continue
		}
		if jobID == "" {
			return st, pipeline.Job{}, nil
		}
		jobs, ok := st["jobs"].([]any)
		if !ok {
			return nil, pipeline.Job{}, ErrUnsupported
		}
		for _, jv := range jobs {
			j, ok := jv.(map[string]any)
			if !ok {
				return nil, pipeline.Job{}, ErrUnsupported
			}
			if j["id"] == jobID {
				b, _ := json.Marshal(j)
				var typed pipeline.Job
				if json.Unmarshal(b, &typed) != nil {
					return nil, pipeline.Job{}, ErrUnsupported
				}
				return j, typed, nil
			}
		}
	}
	return nil, pipeline.Job{}, ErrForbidden
}
func (s *Service) patch(d *document, ops []Operation, g guard) (*document, []Diff, error) {
	if d.applicability != "supported" || !d.stored {
		return nil, nil, ErrUnsupported
	}
	c, e := cloneDocument(d)
	if e != nil {
		return nil, nil, e
	}
	diff := []Diff{}
	add := func(o Operation, field string, before, after any, impact string) {
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if bytes.Equal(a, b) {
			return
		}
		diff = append(diff, Diff{Type: o.Type, StageID: o.StageID, JobID: o.JobID, Field: field, Before: safeScalar(g, before), After: safeScalar(g, after), Impact: impact})
	}
	for _, o := range ops {
		switch o.Type {
		case "project.set_name":
			add(o, "name", c.name, *o.Value, "identity")
			c.name = *o.Value
		case "project.set_default_branch":
			add(o, "defaultBranch", c.branch, *o.Value, "source")
			c.branch = *o.Value
		case "stage.rename", "job.rename":
			n, _, e := node(c, o.StageID, o.JobID)
			if e != nil {
				return nil, nil, e
			}
			old := n["name"]
			n["name"] = *o.Value
			add(o, "name", old, *o.Value, "display")
		case "step.set_limits", "build.set":
			n, before, e := node(c, o.StageID, o.JobID)
			if e != nil {
				return nil, nil, e
			}
			cfg, ok := n["config"].(map[string]any)
			if !ok {
				return nil, nil, ErrUnsupported
			}
			set := func(k string, v any) {
				add(o, k, cfg[k], v, map[string]string{"step.set_limits": "limits", "build.set": "build"}[o.Type])
				cfg[k] = v
			}
			if o.Limits != nil {
				l := o.Limits
				if l.TimeoutSeconds != nil {
					set("timeoutSeconds", *l.TimeoutSeconds)
				}
				if l.Retries != nil {
					set("retries", *l.Retries)
				}
				if l.CPU != nil {
					set("cpu", *l.CPU)
				}
				if l.Memory != nil {
					set("memory", *l.Memory)
				}
			}
			if o.Build != nil {
				b := o.Build
				for _, x := range []struct {
					k string
					p *string
				}{{"buildModel", b.BuildModel}, {"toolchainLanguage", b.ToolchainLanguage}, {"toolchainVersion", b.ToolchainVersion}, {"dockerfilePath", b.DockerfilePath}, {"context", b.Context}} {
					if x.p != nil {
						set(x.k, *x.p)
					}
				}
			}
			_, after, e := node(c, o.StageID, o.JobID)
			if e != nil {
				return nil, nil, e
			}
			if e = build.CheckConfigJobEdit(before, after, d.settings.Build.Cache, o.Type); e != nil {
				if e == build.ErrConfigUnsupportedField {
					return nil, nil, ErrUnsupported
				}
				return nil, nil, ErrTemplate
			}
		}
	}
	if e = decodeSpec(c); e != nil {
		return nil, nil, e
	}
	if e = boundedSpec(c.spec); e != nil {
		return nil, nil, e
	}
	// Reuse structural rules without persisting their normalized/trimmed copy:
	// it would discard unknown fields or otherwise change untouched data.
	if _, e = pipeline.NormalizeSpec(c.spec); e != nil {
		return nil, nil, ErrUnsupported
	}
	a, e := build.ConfigTopologySignature(d.spec)
	if e != nil {
		return nil, nil, ErrUnsupported
	}
	b, e := build.ConfigTopologySignature(c.spec)
	if e != nil {
		return nil, nil, ErrUnsupported
	}
	if !reflect.DeepEqual(a, b) {
		return nil, nil, ErrTopology
	}
	// Validate final composition too, so separate edits cannot hide an effect.
	for _, o := range ops {
		if o.Type != "step.set_limits" && o.Type != "build.set" {
			continue
		}
		_, old, e := node(d, o.StageID, o.JobID)
		if e != nil {
			return nil, nil, e
		}
		_, now, e := node(c, o.StageID, o.JobID)
		if e != nil {
			return nil, nil, e
		}
		if e = build.CheckConfigJobEdit(old, now, d.settings.Build.Cache, o.Type); e != nil {
			return nil, nil, ErrTemplate
		}
	}
	if len(diff) > 32 {
		return nil, nil, Error("budget_exceeded")
	}
	return c, diff, nil
}
