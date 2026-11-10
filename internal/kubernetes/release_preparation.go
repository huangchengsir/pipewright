package kubernetes

import (
	"context"
	"errors"
)

// releasePreparationError keeps the original cause for internal errors.Is checks,
// but never formats its text. Only closed phase/reason pairs may reach run logs.
type releasePreparationError struct {
	phase, reason string
	cause         error
}

func (e *releasePreparationError) Error() string {
	return "kubernetes preparation: phase=" + e.phase + " reason=" + e.reason
}
func (e *releasePreparationError) Unwrap() error { return e.cause }

func releasePreparationPhase(phase string) string {
	switch phase {
	case "configuration", "snapshot", "authorization", "release_record", "variables", "source", "images", "chart", "values", "manifest", "helm", "image", "progressive", "runtime", "transport", "engine", "pull", "inspect", "runtime_register", "container_create", "container_start", "capability", "helper_init", "version", "discovery", "plan", "dry_run", "baseline":
		return phase
	}
	return "prepare"
}
func releasePreparationReason(err error) string {
	var apiStatus *releaseAPIStatusError
	if errors.As(err, &apiStatus) {
		return apiStatus.reason()
	}
	if errors.Is(err, errReleaseAdmissionMismatch) {
		return "kubernetes_admission_mismatch"
	}
	for _, known := range []struct {
		cause error
		code  string
	}{
		{ErrInvalid, "kubernetes_invalid_input"}, {ErrNotFound, "kubernetes_not_found"},
		{ErrConflict, "kubernetes_revision_conflict"}, {ErrInUse, "kubernetes_in_use"},
		{ErrDenied, "kubernetes_not_authorized"}, {ErrDecrypt, "kubernetes_credential_unavailable"},
		{ErrRuntimeUnverified, "secret_runtime_unverified"}, {ErrRuntimeIdentity, "kubernetes_runtime_identity_changed"},
		{ErrTool, "kubernetes_tool_unavailable"}, {ErrVersion, "kubernetes_tool_version_incompatible"},
		{ErrTransportUnknown, "kubernetes_transport_outcome_unknown"}, {ErrPrivateLimit, "kubernetes_private_output_limit"},
		{ErrManifest, "kubernetes_manifest_invalid"}, {ErrVariable, "kubernetes_variable_unavailable"},
		{ErrDiscovery, "kubernetes_resource_not_discovered"}, {ErrOwner, "kubernetes_owner_mismatch"},
		{ErrHeld, "kubernetes_workload_held"}, {context.Canceled, "preparation_canceled"}, {context.DeadlineExceeded, "preparation_timeout"},
	} {
		if errors.Is(err, known.cause) {
			return known.code
		}
	}
	return "preparation_failed"
}

// WithReleasePreparationPhase preserves the most specific nested runtime phase.
func WithReleasePreparationPhase(err error, phase string) error {
	if err == nil {
		return nil
	}
	var existing *releasePreparationError
	if errors.As(err, &existing) {
		return &releasePreparationError{phase: existing.phase, reason: existing.reason, cause: err}
	}
	return &releasePreparationError{phase: releasePreparationPhase(phase), reason: releasePreparationReason(err), cause: err}
}
func ReleasePreparationDetails(err error) (phase, reason string, ok bool) {
	var e *releasePreparationError
	if !errors.As(err, &e) {
		return "prepare", "preparation_failed", false
	}
	return e.phase, e.reason, true
}
