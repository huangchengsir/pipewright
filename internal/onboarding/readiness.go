package onboarding

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/trigger"
)

func (s *Service) prepare(ctx context.Context, p projectRecord, cfg *pipeline.Config, out *Snapshot) {
	out.Pipeline.State = "unconfirmed"
	add := func(code, scope string) {
		for _, issue := range out.Pipeline.Issues {
			if issue.Code == code && issue.Scope == scope {
				return
			}
		}
		out.Pipeline.Issues = append(out.Pipeline.Issues, Issue{Code: code, Scope: scope})
	}
	addValidation := func(issues []pipeline.Issue) {
		for _, issue := range issues {
			if issue.Severity == pipeline.SeverityError {
				add(issue.Code, issue.Scope)
			}
		}
	}
	unknown := func(code, scope string) { out.Pipeline.State = Unknown; add(code, scope) }
	credential := func(id string) bool {
		if strings.TrimSpace(id) != "" && !s.capabilities.VaultConfigured {
			add("vault_unconfigured", pipeline.ScopeEnvs)
		}
		ok, err := s.exists(ctx, "credentials", id)
		if err != nil {
			unknown("credentials_unavailable", pipeline.ScopeEnvs)
			return false
		}
		return ok
	}
	vars := func(values []pipeline.BuildVar) []pipeline.ValidationVar {
		result := make([]pipeline.ValidationVar, 0, len(values))
		for _, v := range values {
			item := pipeline.ValidationVar{Key: v.Key, Secret: v.Secret, CredentialID: v.CredentialID}
			if v.Secret {
				item.CredentialExists = credential(v.CredentialID)
			}
			result = append(result, item)
		}
		return result
	}
	server := func(id, scope string) {
		var credentialID string
		err := s.db.QueryRowContext(ctx, `SELECT credential_id FROM servers WHERE id = ?`, strings.TrimSpace(id)).Scan(&credentialID)
		if errors.Is(err, sql.ErrNoRows) {
			add("server_missing", scope)
			return
		}
		if err != nil {
			unknown("servers_unavailable", scope)
			return
		}
		if !credential(credentialID) && out.Pipeline.State != Unknown {
			add("credential_missing", pipeline.ScopeEnvs)
		}
	}
	if _, err := pipeline.NormalizeSpec(cfg.Spec); err != nil {
		add("pipeline_invalid", pipeline.ScopeCanvas)
	}
	stages, err := applicableStages(cfg.Spec, p.branch)
	if err != nil {
		add("pipeline_invalid", pipeline.ScopeCanvas)
	}
	remote := false
	if !s.capabilities.Legacy && s.capabilities.Runtime != RuntimeStub {
		var serverID string
		err := s.db.QueryRowContext(ctx, `SELECT runner_server_id FROM project_runners WHERE project_id = ?`, p.ID).Scan(&serverID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			out.Runtime = RuntimeUnknown
			unknown("runner_unavailable", pipeline.ScopeVars)
		}
		if strings.TrimSpace(serverID) != "" {
			remote = true
			out.Runtime = RuntimeUnknown
			server(serverID, pipeline.ScopeVars)
		}
	}
	settings := pipeline.DefaultSettings()
	needSettings, usesBuildVars, usesImage := s.capabilities.Legacy, false, false
	needsSource := s.capabilities.Legacy
	for _, stage := range stages {
		for _, job := range stage.Jobs {
			if isScript(job.Type) {
				needsSource = true
				if !remote {
					needSettings = true
					usesBuildVars = true
				}
			}
			if strings.TrimSpace(job.Type) == "build_image" && !remote && (configString(job.Config, "artifactType") == "" || configString(job.Config, "artifactType") == pipeline.ArtifactImage) {
				needSettings = true
				usesImage = true
			}
			if strings.TrimSpace(job.Type) == "build_image" && !remote {
				needsSource = true
			}
		}
		for _, post := range stage.Post {
			if !remote && pipeline.PostConditionMatches(post.Condition, false) {
				needsSource = true
			}
		}
	}
	if needSettings {
		loaded, err := pipeline.LoadExistingSettings(ctx, s.db, p.ID)
		if err == nil {
			settings = loaded
		} else if !errors.Is(err, sql.ErrNoRows) {
			unknown("settings_unavailable", pipeline.ScopeVars)
		}
	}
	// The execution paths consume build variables only in legacy and local script
	// jobs. Environment variables are not consumed by these executors.
	in := pipeline.ValidationInput{Stages: cfg.Spec.Stages, ProjectCredentialOK: true}
	tasks := 0
	uncertainTask := false
	if s.capabilities.Legacy {
		tasks = 1
		usesBuildVars = true
		usesImage = settings.Build.ArtifactType == pipeline.ArtifactImage
		addValidation(pipeline.ValidateExecutionBuild(settings.Build))
		for _, step := range settings.Steps {
			addValidation(pipeline.ValidateExecutionScript(step))
			stepInput := pipeline.ValidationInput{Stages: cfg.Spec.Stages, ProjectCredentialOK: true, BuildVars: vars(step.Env)}
			for _, issue := range pipeline.Validate(stepInput) {
				if issue.Severity == pipeline.SeverityError {
					add(issue.Code, pipeline.ScopeEnvs)
				}
			}
		}
	} else {
		for _, stage := range stages {
			for _, job := range stage.Jobs {
				typ := strings.TrimSpace(job.Type)
				if typ == "git_source" || typ == "push_image" {
					continue
				}
				if remote && !isScript(typ) {
					add("task_execution_unavailable", pipeline.ScopeCanvas)
					continue
				}
				switch {
				case isScript(typ):
					tasks++
					image, commands := configString(job.Config, "image"), configString(job.Config, "commands")
					if template := configString(job.Config, "commandTemplate"); template != "" {
						commands = template
					}
					if strings.Contains(image, "{{") || strings.Contains(commands, "{{") {
						uncertainTask = true
					}
					addValidation(pipeline.ValidateExecutionScript(pipeline.PipelineStep{Image: image, Commands: strings.Split(commands, "\n")}))
				case typ == "build_image":
					tasks++
					build := pipeline.BuildConfig{Model: configString(job.Config, "buildModel"), ArtifactType: configString(job.Config, "artifactType"), DockerfilePath: configString(job.Config, "dockerfilePath"), Toolchain: pipeline.Toolchain{Language: configString(job.Config, "toolchainLanguage"), Version: configString(job.Config, "toolchainVersion")}}
					addValidation(pipeline.ValidateExecutionBuild(build))
				case typ == "deploy_ssh" || typ == "deploy_frontend":
					tasks++
					server(configString(job.Config, "serverId"), pipeline.ScopeCanvas)
					if configString(job.Config, "artifactType") == "command" && configString(job.Config, "restartCommand") == "" {
						add("script_incomplete", pipeline.ScopeCanvas)
					}
				case typ == "notify":
					ref := configString(job.Config, "channel")
					var n int
					err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_channels WHERE (id = ? OR name = ?) AND enabled = 1`, ref, ref).Scan(&n)
					if err != nil {
						unknown("notifications_unavailable", pipeline.ScopeCanvas)
					} else if ref == "" || n == 0 {
						add("notification_channel_missing", pipeline.ScopeCanvas)
					} else {
						tasks++
					}
				default:
					add("task_execution_unavailable", pipeline.ScopeCanvas)
				}
			}
			for _, post := range stage.Post {
				if !pipeline.PostConditionMatches(post.Condition, false) {
					continue
				}
				if remote {
					add("task_execution_unavailable", pipeline.ScopeCanvas)
					continue
				}
				tasks++
				addValidation(pipeline.ValidateExecutionScript(pipeline.PipelineStep{Image: post.Image, Commands: post.Commands}))
			}
			if remote {
				if len(stage.Services) > 0 {
					add("task_execution_unavailable", pipeline.ScopeCanvas)
				}
				for _, job := range stage.Jobs {
					if configString(job.Config, "testReport") == "junit" && configString(job.Config, "reportPath") != "" {
						add("task_execution_unavailable", pipeline.ScopeCanvas)
					}
				}
			}
		}
	}
	if tasks == 0 {
		add("no_tasks", pipeline.ScopeCanvas)
	} else {
		if needsSource {
			in.ProjectCredentialOK = credential(p.credentialID)
		}
		if usesBuildVars {
			in.BuildVars = vars(settings.Build.Vars)
		}
	}
	if usesImage {
		env, _, err := trigger.ResolveExistingEnv(ctx, s.db, p.ID, p.branch)
		if err != nil {
			unknown("triggers_unavailable", pipeline.ScopeTriggers)
		} else if strings.TrimSpace(env) != "" {
			found := false
			for _, e := range settings.Environments {
				if strings.TrimSpace(e.Name) != strings.TrimSpace(env) {
					continue
				}
				found = true
				reg := e.ImageRegistry
				// Incomplete optional bindings are not used by resolveRegistry.
				if strings.TrimSpace(reg.Type) != "" && strings.TrimSpace(reg.URL) != "" && strings.TrimSpace(reg.CredentialID) != "" && !credential(reg.CredentialID) && out.Pipeline.State != Unknown {
					add("credential_missing", pipeline.ScopeEnvs)
				}
			}
			if !found {
				add("environment_undefined", pipeline.ScopeTriggers)
			}
		}
	}
	for _, issue := range pipeline.Validate(in) {
		if issue.Severity == pipeline.SeverityError {
			scope := issue.Scope
			if issue.Code == "credential_missing" || issue.Code == "project_credential_missing" {
				scope = pipeline.ScopeEnvs
			}
			add(issue.Code, scope)
		}
	}
	if out.Runtime == RuntimeStub {
		add("runtime_stub", pipeline.ScopeVars)
	}
	if out.Pipeline.State == Unknown {
		return
	}
	if cfg.SavedAt == nil {
		return
	}
	if len(out.Pipeline.Issues) > 0 {
		out.Pipeline.State = "needs_configuration"
		return
	}
	if cfg.SavedAt != nil && out.Runtime == RuntimeAvailable && !uncertainTask {
		out.Pipeline.State = "ready"
	}
}

func (s *Service) exists(ctx context.Context, table, id string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id = ?`, strings.TrimSpace(id)).Scan(&count)
	return count > 0, err
}

func isScript(typ string) bool {
	switch strings.TrimSpace(typ) {
	case "script", "custom", "build_frontend", "build_backend", "templated":
		return true
	}
	return false
}
func configString(cfg map[string]any, key string) string {
	value, _ := cfg[key].(string)
	return strings.TrimSpace(value)
}

// Respect both explicit dependencies and the runner's implicit linear ordering.
func applicableStages(spec pipeline.Spec, branch string) ([]pipeline.Stage, error) {
	validated, err := pipeline.NormalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	// Inspect the same effective plan as the runner, including omitted matrix fields.
	spec.Stages = dagrun.ExpandMatrix(validated.Stages)
	graph, err := dagrun.BuildGraph(spec.Stages)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]pipeline.Stage, len(spec.Stages))
	linear := true
	for _, st := range spec.Stages {
		byID[st.ID] = st
		if len(st.Needs) > 0 {
			linear = false
		}
	}
	active := make(map[string]bool, len(spec.Stages))
	var result []pipeline.Stage
	previous := ""
	for _, id := range graph.TopoOrder() {
		st := byID[id]
		ok := st.When.Matches(branch, run.TriggerManual)
		for _, need := range st.Needs {
			ok = ok && active[need]
		}
		if linear && previous != "" {
			ok = ok && active[previous]
		}
		active[id] = ok
		previous = id
		if ok {
			result = append(result, st)
		}
	}
	return result, nil
}
