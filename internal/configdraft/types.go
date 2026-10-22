// Package configdraft implements bounded, independently approved configuration changes.
// It never executes a pipeline, resolves a credential, or contacts a repository.
package configdraft

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrInvalid         Error = "invalid_argument"
	ErrForbidden       Error = "permission_denied"
	ErrUnavailable     Error = "service_unavailable"
	ErrStale           Error = "snapshot_stale"
	ErrConflict        Error = "revision_conflict"
	ErrPolicy          Error = "policy_changed"
	ErrUnsupported     Error = "unsupported_execution_source"
	ErrLarge           Error = "unsupported_large_config"
	ErrTemplate        Error = "template_coupled"
	ErrTopology        Error = "topology_changed"
	ErrSecret          Error = "secret_registration_unavailable"
	ErrCatalog         Error = "secret_catalog_changed"
	ErrExpired         Error = "draft_expired"
	ErrApproval        Error = "approval_invalid"
	ErrApprovalExpired Error = "approval_expired"
	ErrBusy            Error = "active_runs"
	ErrIdempotency     Error = "idempotency_conflict"
	ErrQuota           Error = "quota_exceeded"
	ErrDisabled        Error = "apply_disabled"
	ErrBudget          Error = "budget_exceeded"
)

// Code returns only a controlled code; raw SQL and parser diagnostics stay local.
func Code(err error) string {
	var e Error
	if errors.As(err, &e) {
		return string(e)
	}
	return string(ErrUnavailable)
}

type RuntimeInfo struct {
	RunnerMode   string `json:"runnerMode"`
	ExecutorKind string `json:"executorKind"`
	Available    bool   `json:"-"`
	PACOverride  bool   `json:"-"`
}
type Options struct {
	Runtime      RuntimeInfo
	ApplyEnabled bool
	Now          func() time.Time
	// PrepareSecrets registers existing secrets locally and returns a scrubber.
	// A missing or failed guard always fails closed for business data.
	PrepareSecrets func(context.Context) (func(string) string, error)
	// AuthorizeAdmin must verify the current browser session using this DBTX.
	AuthorizeAdmin func(context.Context, configstate.DBTX, string) error
	// RevalidateApprover checks the stored browser subject at apply time, without
	// requiring a browser session in the MCP request context.
	RevalidateApprover func(context.Context, configstate.DBTX, string) error
	AppendAudit        func(context.Context, configstate.DBTX, audit.Entry) error
	CursorKey          []byte
	MaxCPU             float64
	MaxMemoryBytes     int64
}
type Service struct {
	db        *sql.DB
	opts      Options
	cursorKey []byte
}

type SnapshotRequest struct {
	ProjectID             string `json:"projectId"`
	Section               string `json:"section"`
	BeginSnapshot         bool   `json:"beginSnapshot,omitempty"`
	ExpectedBaseRevision  string `json:"expectedBaseRevision,omitempty"`
	ExpectedPolicyVersion string `json:"expectedPolicyVersion,omitempty"`
	Cursor                string `json:"cursor,omitempty"`
	PageSize              int    `json:"pageSize,omitempty"`
}
type OptionsRequest struct {
	ProjectID             string `json:"projectId"`
	Kind                  string `json:"kind"`
	ExpectedBaseRevision  string `json:"expectedBaseRevision"`
	ExpectedPolicyVersion string `json:"expectedPolicyVersion"`
	Cursor                string `json:"cursor,omitempty"`
	PageSize              int    `json:"pageSize,omitempty"`
}
type Snapshot struct {
	ProjectID              string           `json:"projectId"`
	Section                string           `json:"section"`
	BaseRevision           string           `json:"baseRevision"`
	PolicyVersion          string           `json:"policyVersion"`
	AuthzRevision          int64            `json:"authzRevision"`
	RunnerMode             string           `json:"runnerMode"`
	ExecutorKind           string           `json:"executorKind"`
	SourceMode             string           `json:"sourceMode"`
	ExecutionApplicability string           `json:"executionApplicability"`
	EditableOperations     []string         `json:"editableOperations"`
	ObservedAt             string           `json:"observedAt"`
	Items                  []map[string]any `json:"items"`
	NextCursor             string           `json:"nextCursor,omitempty"`
	Missing                bool             `json:"missing,omitempty"`
	UntrustedData          bool             `json:"untrustedData"`
}
type Limits struct {
	TimeoutSeconds *int    `json:"timeoutSeconds,omitempty"`
	Retries        *int    `json:"retries,omitempty"`
	CPU            *string `json:"cpu,omitempty"`
	Memory         *string `json:"memory,omitempty"`
}
type BuildFields struct {
	BuildModel        *string `json:"buildModel,omitempty"`
	ToolchainLanguage *string `json:"toolchainLanguage,omitempty"`
	ToolchainVersion  *string `json:"toolchainVersion,omitempty"`
	DockerfilePath    *string `json:"dockerfilePath,omitempty"`
	Context           *string `json:"context,omitempty"`
}
type Operation struct {
	Type    string       `json:"type"`
	StageID string       `json:"stageId,omitempty"`
	JobID   string       `json:"jobId,omitempty"`
	Value   *string      `json:"value,omitempty"`
	Limits  *Limits      `json:"limits,omitempty"`
	Build   *BuildFields `json:"build,omitempty"`
}
type CreateRequest struct {
	ProjectID     string      `json:"projectId"`
	BaseRevision  string      `json:"baseRevision"`
	PolicyVersion string      `json:"policyVersion"`
	RequestID     string      `json:"requestId"`
	Operations    []Operation `json:"operations"`
}
type Diff struct {
	Type    string `json:"type"`
	StageID string `json:"stageId,omitempty"`
	JobID   string `json:"jobId,omitempty"`
	Field   string `json:"field"`
	Before  any    `json:"before,omitempty"`
	After   any    `json:"after,omitempty"`
	Impact  string `json:"impact"`
}
type Check struct {
	Category  string `json:"category"`
	Status    string `json:"status"`
	IssueCode string `json:"issueCode,omitempty"`
}
type Issue struct {
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
}
type Validation struct {
	State        string  `json:"state"`
	ApplyAllowed bool    `json:"applyAllowed"`
	Checks       []Check `json:"checks"`
	Issues       []Issue `json:"issues"`
}
type Approval struct {
	ID        string `json:"id,omitempty"`
	State     string `json:"state"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Eligible  bool   `json:"eligible"`
	Reason    string `json:"reason,omitempty"`
}
type Draft struct {
	DraftID       string      `json:"draftId,omitempty"`
	ProjectID     string      `json:"projectId"`
	BaseRevision  string      `json:"baseRevision"`
	PolicyVersion string      `json:"policyVersion"`
	ReviewDigest  string      `json:"reviewDigest,omitempty"`
	State         string      `json:"state"`
	ExpiresAt     string      `json:"expiresAt,omitempty"`
	Operations    []Operation `json:"operations"`
	Diff          []Diff      `json:"diff"`
	Validation    Validation  `json:"validation"`
	Approval      Approval    `json:"approval"`
	ReviewURL     string      `json:"reviewUrl,omitempty"`
	Complete      bool        `json:"complete"`
	UntrustedData bool        `json:"untrustedData"`
	NextCursor    string      `json:"nextCursor,omitempty"`
}
type ApprovalRequest struct {
	ReviewDigest  string `json:"reviewDigest"`
	BaseRevision  string `json:"baseRevision"`
	PolicyVersion string `json:"policyVersion"`
}
type ApplyRequest struct {
	DraftID    string `json:"draftId"`
	ApprovalID string `json:"approvalId"`
	RequestID  string `json:"requestId"`
}
type OperationRequest struct {
	OperationID string `json:"operationId,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	RequestID   string `json:"requestId,omitempty"`
}
type OperationResult struct {
	OperationID       string   `json:"operationId"`
	ProjectID         string   `json:"projectId"`
	State             string   `json:"state"`
	Reason            string   `json:"reason,omitempty"`
	BaseRevision      string   `json:"baseRevision,omitempty"`
	NewRevision       string   `json:"newRevision,omitempty"`
	ChangedOperations []string `json:"changedOperations"`
}

const ValidatorVersion = "configdraft-v1"

var Sections = []string{"project", "pipeline", "build", "step_limits", "source_and_impact"}
var OperationTypes = []string{"project.set_name", "project.set_default_branch", "stage.rename", "job.rename", "step.set_limits", "build.set"}
