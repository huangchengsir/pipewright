package build

// These projections deliberately reuse the runtime parsers. They perform no I/O
// and must never be returned by an API: commands can contain private material.
import (
	"errors"
	"reflect"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dag"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

var ErrConfigTemplateCoupled = errors.New("template_coupled")
var ErrConfigUnsupportedField = errors.New("unsupported_execution_source")

type configArtifactProjection struct {
	Explicit     bool
	Paths, Names []string
}

func configArtifactPaths(j pipeline.Job) (configArtifactProjection, error) {
	d, explicit, err := artifactDeclarations(j.Config)
	if err != nil {
		return configArtifactProjection{}, ErrConfigTemplateCoupled
	}
	ctx := templateContext(j.Config)
	if !explicit {
		for _, p := range splitCommands(renderTemplate(cfgString(j.Config, "artifactPath"), ctx)) {
			d = append(d, artifactDeclaration{Path: p})
		}
	}
	p := configArtifactProjection{Explicit: explicit}
	for _, v := range d {
		p.Paths = append(p.Paths, renderTemplate(v.Path, ctx))
		p.Names = append(p.Names, v.Name)
	}
	return p, nil
}

// CheckConfigJobEdit compares only the runtime projection of the selected job.
// Allowed build-container changes are distinct from protected script images.
func CheckConfigJobEdit(before, after pipeline.Job, defaults pipeline.Cache, kind string) error {
	if before.ID != after.ID || before.Type != after.Type || !reflect.DeepEqual(before.Needs, after.Needs) {
		return ErrConfigTemplateCoupled
	}
	if !reflect.DeepEqual(parseCacheConfig(before, defaults), parseCacheConfig(after, defaults)) {
		return ErrConfigTemplateCoupled
	}
	if kind == "step.set_limits" {
		if !isScriptJob(before.Type) {
			return ErrConfigUnsupportedField
		}
		a, e := scriptStepFromJob(before)
		if e != nil {
			return ErrConfigTemplateCoupled
		}
		b, e := scriptStepFromJob(after)
		if e != nil {
			return ErrConfigTemplateCoupled
		}
		a.Name = ""
		b.Name = ""
		a.TimeoutSeconds = 0
		b.TimeoutSeconds = 0
		a.Retries = 0
		b.Retries = 0
		a.Resource.CPU = ""
		b.Resource.CPU = ""
		a.Resource.Memory = ""
		b.Resource.Memory = ""
		if !reflect.DeepEqual(a, b) {
			return ErrConfigTemplateCoupled
		}
		ap, e := configArtifactPaths(before)
		if e != nil {
			return e
		}
		bp, e := configArtifactPaths(after)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(ap, bp) {
			return ErrConfigTemplateCoupled
		}
		return nil
	}
	if kind != "build.set" || !isBuildImageJob(before.Type) {
		return ErrConfigUnsupportedField
	}
	artifact := cfgString(before.Config, "artifactType")
	if artifact == "" {
		artifact = pipeline.ArtifactImage
	}
	if cfgString(before.Config, "artifactType") != cfgString(after.Config, "artifactType") {
		return ErrConfigTemplateCoupled
	}
	// The runtime selects the build implementation by artifact type. Model and
	// the fields of the other implementation are not consumed and stay read-only.
	if cfgString(before.Config, "buildModel") != cfgString(after.Config, "buildModel") {
		return ErrConfigUnsupportedField
	}
	keys := []string{"toolchainLanguage", "toolchainVersion"}
	if artifact == pipeline.ArtifactJAR || artifact == pipeline.ArtifactDist {
		keys = []string{"dockerfilePath", "context"}
	} else if artifact != pipeline.ArtifactImage {
		return ErrConfigUnsupportedField
	}
	for _, k := range keys {
		if cfgString(before.Config, k) != cfgString(after.Config, k) {
			return ErrConfigUnsupportedField
		}
	}
	return nil
}

// ConfigTopologySignature captures the actual executor dispatch and inferred
// dependencies, not display names. It contains no commands or resource values.
// Callers compare it in memory; the signature is not an API value.
func ConfigTopologySignature(spec pipeline.Spec) (any, error) {
	stages := dagrun.ExpandMatrix(spec.Stages)
	g, err := dagrun.BuildGraph(stages)
	if err != nil {
		return nil, ErrConfigTemplateCoupled
	}
	type stageShape struct {
		ID          string
		Needs       []string
		JobDAG      bool
		Jobs        []any
		PushSources map[string]map[string]bool
	}
	out := []stageShape{}
	anyNeeds := false
	for _, s := range stages {
		if len(s.Needs) > 0 {
			anyNeeds = true
		}
	}
	for i, s := range stages {
		x := stageShape{ID: s.ID, Needs: s.Needs, JobDAG: stageHasJobNeeds(s.Jobs) || (len(s.Jobs) > 1 && allJobsHaveIDs(s.Jobs)), PushSources: map[string]map[string]bool{}}
		if !anyNeeds && i > 0 {
			x.Needs = []string{stages[i-1].ID}
		}
		var nodes []dag.Node
		for _, j := range s.Jobs {
			needs := j.Needs
			if strings.TrimSpace(j.Type) == "push_image" {
				sources := pushImageSourceJobs(s, j)
				x.PushSources[j.ID] = sources
				if len(needs) == 0 && len(sources) == 1 {
					for id := range sources {
						needs = []string{id}
					}
				}
			}
			x.Jobs = append(x.Jobs, struct {
				ID, Type string
				Needs    []string
			}{j.ID, j.Type, needs})
			nodes = append(nodes, dag.Node{ID: j.ID, Needs: needs})
		}
		if x.JobDAG {
			if _, e := dag.New(nodes); e != nil {
				return nil, ErrConfigTemplateCoupled
			}
		}
		out = append(out, x)
	}
	_ = g
	return out, nil
}

// ConfigJobKind exposes controlled executor classifications only.
func ConfigJobKind(t string) string {
	if isScriptJob(t) {
		return "script"
	}
	if isBuildImageJob(t) {
		return "build_image"
	}
	switch strings.TrimSpace(t) {
	case "git_source", "push_image", "deploy_ssh", "deploy_frontend", "notify":
		return strings.TrimSpace(t)
	}
	return "unsupported"
}

func configBuildConfig(j pipeline.Job, vars []pipeline.BuildVar) pipeline.BuildConfig {
	c := pipeline.BuildConfig{Model: cfgString(j.Config, "buildModel"), DockerfilePath: cfgString(j.Config, "dockerfilePath"), Context: cfgString(j.Config, "context"), Toolchain: pipeline.Toolchain{Language: cfgString(j.Config, "toolchainLanguage"), Version: cfgString(j.Config, "toolchainVersion")}, ArtifactType: cfgString(j.Config, "artifactType"), Vars: vars}
	if c.ArtifactType == "" {
		c.ArtifactType = pipeline.ArtifactImage
	}
	if c.Model == "" {
		c.Model = pipeline.BuildModelDockerfile
	}
	return c
}

// ConfigExecutionIssues validates only fields actually used by the DAG executor.
func ConfigExecutionIssues(spec pipeline.Spec) []pipeline.Issue {
	var issues []pipeline.Issue
	for _, st := range spec.Stages {
		for _, j := range st.Jobs {
			switch {
			case isScriptJob(j.Type):
				p, e := scriptStepFromJob(j)
				if e != nil {
					issues = append(issues, pipeline.Issue{Severity: pipeline.SeverityError, Code: "script_incomplete"})
				} else {
					issues = append(issues, pipeline.ValidateExecutionScript(p)...)
				}
			case isBuildImageJob(j.Type):
				issues = append(issues, pipeline.ValidateExecutionBuild(configBuildConfig(j, nil))...)
			}
		}
	}
	return issues
}
