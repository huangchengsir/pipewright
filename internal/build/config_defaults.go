package build

import (
	"strings"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

func selectedEnvironment(settings *pipeline.Settings, envName string) *pipeline.Environment {
	envName = strings.TrimSpace(envName)
	if settings == nil || envName == "" {
		return nil
	}
	for i := range settings.Environments {
		if settings.Environments[i].Name == envName {
			return &settings.Environments[i]
		}
	}
	return nil
}

func projectBuildVars(settings *pipeline.Settings, envName string) []pipeline.BuildVar {
	var vars []pipeline.BuildVar
	if settings != nil {
		vars = append(vars, settings.Build.Vars...)
	}
	if env := selectedEnvironment(settings, envName); env != nil {
		vars = append(vars, env.EnvVars...)
	}
	return vars
}

func scriptEnv(settings *pipeline.Settings, envName string, params map[string]string, upstream, node []pipeline.BuildVar) []pipeline.BuildVar {
	vars := append(runParamsAsEnv(params), upstream...)
	vars = append(vars, projectBuildVars(settings, envName)...)
	vars = append(vars, node...)
	return append(vars, pipewrightEnvVar())
}

func defaultBuildCache(settings *pipeline.Settings) pipeline.Cache {
	if settings == nil {
		return pipeline.Cache{}
	}
	return settings.Build.Cache
}

func deployServerIDs(jb pipeline.Job, settings *pipeline.Settings, envName string) []string {
	if serverID := cfgString(jb.Config, "serverId"); serverID != "" {
		return []string{serverID}
	}
	env := selectedEnvironment(settings, envName)
	if env == nil {
		return nil
	}
	var ids []string
	seen := make(map[string]bool, len(env.TargetServerIDs))
	for _, id := range env.TargetServerIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
}
