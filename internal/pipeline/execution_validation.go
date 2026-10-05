package pipeline

// ValidateExecutionBuild reuses stored build rules, checking the toolchain only
// for file artifacts: image execution uses Dockerfile regardless of model.
func ValidateExecutionBuild(build BuildConfig) []Issue {
	build.Vars = nil
	normalized, err := (&settingsService{}).normalizeBuild(build)
	if err != nil {
		return []Issue{{Severity: SeverityError, Code: "build_invalid", Scope: ScopeVars}}
	}
	if normalized.ArtifactType == ArtifactImage {
		normalized.Model = BuildModelDockerfile
	} else {
		normalized.Model = BuildModelToolchain
	}
	return validateVars(ValidationInput{Build: normalized})
}

// ValidateExecutionScript checks the existing script rules without credential lookup.
// Credential references are checked separately by the read-only caller.
func ValidateExecutionScript(step PipelineStep) []Issue {
	step.Env = nil
	if step.Name == "" {
		step.Name = "script"
	}
	if _, err := (&settingsService{}).normalizeSteps([]PipelineStep{step}); err != nil {
		return []Issue{{Severity: SeverityError, Code: "script_incomplete", Scope: ScopeCanvas}}
	}
	return nil
}
