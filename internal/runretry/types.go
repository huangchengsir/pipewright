package runretry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"io"
	"time"
)

const SchemaVersion = 1

var (
	ErrNotFound    = errors.New("runretry: not found")
	ErrInvalid     = errors.New("runretry: invalid input")
	ErrConflict    = errors.New("runretry: revision or identity conflict")
	ErrBlocked     = errors.New("runretry: recovery blocked")
	ErrUnavailable = errors.New("runretry: evidence unavailable")
	ErrUnknown     = errors.New("runretry: outcome unknown")
	// ErrCheckpoint indicates lost recoverability, not failure of completed business work.
	ErrCheckpoint       = errors.New("runretry: checkpoint unavailable")
	ErrDispatchRejected = errors.New("runretry: dispatch definitely rejected")
)

// Sealer is implemented by vault.Vault. It retrieves no credential plaintext.
type Sealer interface {
	SealSecret([]byte) ([]byte, error)
	OpenSecret([]byte) ([]byte, error)
}
type BlobStore interface {
	Open(string) (io.ReadCloser, error)
}
type Trigger struct {
	Type             string            `json:"type"`
	Branch           string            `json:"branch"`
	Commit           string            `json:"commit"`
	Actor            string            `json:"actor"`
	Environment      string            `json:"environment,omitempty"`
	TargetServerIDs  []string          `json:"targetServerIds,omitempty"`
	Params           map[string]string `json:"params,omitempty"`
	ChainSourceRunID string            `json:"chainSourceRunId,omitempty"`
	ChainDepth       int               `json:"chainDepth,omitempty"`
}

// Snapshot is encrypted as a whole. Only credential references belong here,
// never resolved passwords/tokens. Arbitrary settings/params remain private.
type Snapshot struct {
	Compiled      *FrozenGraph      `json:"compiled,omitempty"`
	SchemaVersion int               `json:"schemaVersion"`
	ProjectID     string            `json:"projectId"`
	Spec          pipeline.Spec     `json:"spec"`
	Project       map[string]any    `json:"project,omitempty"`
	Settings      map[string]any    `json:"settings,omitempty"`
	Trigger       Trigger           `json:"trigger"`
	Source        ConfigSource      `json:"source"`
	RunnerRefs    map[string]string `json:"runnerRefs,omitempty"`
	RequestedRef  string            `json:"requestedRef"`
	CommitSHA     string            `json:"commitSha,omitempty"`
}
type FrozenGraph struct {
	Version       int                 `json:"version"`
	Nodes         []NodeRef           `json:"nodes"`
	Edges         []Edge              `json:"edges"`
	Stages        []pipeline.Stage    `json:"stages"`
	Dependencies  map[string][]string `json:"dependencies"`
	MatrixParents map[string]string   `json:"matrixParents,omitempty"`
}

func (s Snapshot) ExecutionStages() ([]pipeline.Stage, error) {
	if s.Compiled == nil || s.Compiled.Version != SchemaVersion {
		return nil, ErrUnavailable
	}
	b, err := json.Marshal(s.Compiled.Stages)
	if err != nil {
		return nil, ErrUnavailable
	}
	var out []pipeline.Stage
	if json.Unmarshal(b, &out) != nil {
		return nil, ErrUnavailable
	}
	for i := range out {
		out[i].MatrixParentID = s.Compiled.MatrixParents[out[i].ID]
		out[i].Needs = append([]string(nil), s.Compiled.Dependencies[out[i].ID]...)
	}
	return out, nil
}

type ConfigSource struct {
	Kind     string `json:"kind"`
	Ref      string `json:"ref,omitempty"`
	File     string `json:"file,omitempty"`
	BlobSHA  string `json:"blobSha,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// The original identity tuple is retained; sanitized matrix labels are not keys.
type NodeRef struct {
	Key              string            `json:"key"`
	StageID          string            `json:"stageId"`
	JobID            string            `json:"jobId,omitempty"`
	ControlKey       string            `json:"controlKey,omitempty"`
	Axes             map[string]string `json:"axes,omitempty"`
	ExecutionStageID string            `json:"executionStageId"`
	ExecutionJobID   string            `json:"executionJobId,omitempty"`
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	AllowFailure     bool              `json:"allowFailure,omitempty"`
}
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Order int    `json:"order,omitempty"`
}
type Graph struct {
	Version int              `json:"version"`
	Nodes   []NodeRef        `json:"nodes"`
	Edges   []Edge           `json:"edges"`
	Stages  []pipeline.Stage `json:"-"`
}
type Reason struct {
	Code    string   `json:"code"`
	NodeKey string   `json:"nodeKey,omitempty"`
	Related []string `json:"related,omitempty"`
}
type Decision struct {
	Action          string   `json:"action"` // execute | reuse | blocked | skip | control
	SourceAttemptID string   `json:"sourceAttemptId,omitempty"`
	SourceNodeKey   string   `json:"sourceNodeKey,omitempty"`
	Status          string   `json:"status,omitempty"`
	Reasons         []Reason `json:"reasons,omitempty"`
}
type PlannedNode struct {
	ReasonCode       string   `json:"reasonCode,omitempty"`
	ExecutionStarted bool     `json:"executionStarted"`
	Node             NodeRef  `json:"node"`
	Decision         Decision `json:"decision"`
	StepID           string   `json:"attemptStepId,omitempty"`
	Ordinal          int      `json:"ordinal"`
	SourceStepID     string   `json:"sourceStepId,omitempty"`
	SourceOrdinal    int      `json:"sourceOrdinal"`
}
type Plan struct {
	SchemaVersion       int            `json:"schemaVersion"`
	LogicalRunID        string         `json:"logicalRunId"`
	BaseAttemptID       string         `json:"baseAttemptId"`
	BaseRevision        int64          `json:"baseRevision"`
	ManifestFingerprint string         `json:"manifestFingerprint"`
	PlanFingerprint     string         `json:"planFingerprint"`
	TargetNodeIDs       []string       `json:"targetNodeIds"`
	CanSubmit           bool           `json:"canSubmit"`
	Nodes               []PlannedNode  `json:"nodes"`
	BlockingReasons     []Reason       `json:"blockingReasons"`
	Warnings            []Reason       `json:"warnings"`
	RebuildNodeIDs      []string       `json:"rebuildNodeIds,omitempty"`
	RebuildGroups       []RebuildGroup `json:"rebuildGroups,omitempty"`
}
type PlanOptions struct{ RebuildNodeIDs []string }
type RebuildGroup struct {
	NodeIDs     []string `json:"nodeIds"`
	ConsumerIDs []string `json:"consumerIds"`
	ReasonCode  string   `json:"reasonCode"`
}
type SubmitInput struct {
	Initiator       string   `json:"-"` // authenticated server identity, never client supplied
	Scope           string   `json:"-"`
	RequestID       string   `json:"requestId"`
	BaseAttemptID   string   `json:"baseAttemptId"`
	BaseRevision    int64    `json:"baseRevision"`
	PlanFingerprint string   `json:"planFingerprint"`
	TargetNodeIDs   []string `json:"targetNodeIds"`
	RebuildNodeIDs  []string `json:"rebuildNodeIds,omitempty"`
}
type Request struct {
	Initiator       string    `json:"initiator,omitempty"`
	RequestID       string    `json:"requestId"`
	LogicalRunID    string    `json:"logicalRunId"`
	SourceAttemptID string    `json:"sourceAttemptId"`
	AttemptRunID    string    `json:"attemptRunId"`
	State           string    `json:"state"`
	ReasonCode      string    `json:"reasonCode,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}
type Attempt struct {
	Initiator       string    `json:"initiator,omitempty"`
	RunID           string    `json:"runId"`
	ParentAttemptID string    `json:"parentAttemptId,omitempty"`
	Revision        int64     `json:"revision"`
	Status          string    `json:"status"`
	ExecutionKind   string    `json:"executionKind"`
	OutcomeUnknown  bool      `json:"outcomeUnknown"`
	CreatedAt       time.Time `json:"createdAt"`
}
type AttemptPage struct {
	Items []Attempt `json:"items"`
	Page  int       `json:"page"`
	Total int       `json:"total"`
}

// Context is safe for the API; no snapshot/config/env values are exposed.
type Context struct {
	LogicalRunID        string        `json:"logicalRunId"`
	AttemptRunID        string        `json:"attemptRunId"`
	HeadAttemptID       string        `json:"headAttemptId"`
	Revision            int64         `json:"revision"`
	ManifestFingerprint string        `json:"manifestFingerprint"`
	CommitSHA           string        `json:"commitSha,omitempty"`
	Graph               Graph         `json:"graph"`
	Nodes               []PlannedNode `json:"nodes"`
	OutcomeUnknown      bool          `json:"outcomeUnknown"`
}
type FileRef struct {
	Name              string         `json:"name,omitempty"`
	ArtifactType      string         `json:"artifactType,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	Key               string         `json:"key"`
	SHA256            string         `json:"sha256"`
	Size              int64          `json:"size"`
	Path              string         `json:"path"`
	Kind              string         `json:"kind"`
	ProducerAttemptID string         `json:"producerAttemptId"`
	ProducerNodeKey   string         `json:"producerNodeKey"`
}
type ImageRef struct {
	Name              string         `json:"name,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	Ref               string         `json:"ref"`
	Digest            string         `json:"digest,omitempty"`
	ImageID           string         `json:"imageId,omitempty"`
	RuntimeID         string         `json:"runtimeId,omitempty"`
	ConfigSHA         string         `json:"configSha,omitempty"`
	ProducerAttemptID string         `json:"producerAttemptId"`
	ProducerNodeKey   string         `json:"producerNodeKey"`
}
type Inputs struct {
	Files  []FileRef           `json:"files,omitempty"`
	Env    map[string]string   `json:"env,omitempty"`
	Images map[string]ImageRef `json:"images,omitempty"`
}
type Outcome struct {
	// DeploymentNotDispatched is private encrypted evidence, never an API DTO.
	// Finish validates the deploy-only shape and absence of owned operations.
	DeploymentNotDispatched bool            `json:"deploymentNotDispatched,omitempty"`
	Targets                 []TargetReceipt `json:"targets,omitempty"`
	WorkspaceIncomplete     bool            `json:"workspaceIncomplete,omitempty"`
	Status                  string          `json:"status"`
	ExecutionMode           string          `json:"executionMode"`
	ExternalOutcome         string          `json:"externalOutcome,omitempty"` // verified | unknown | none
	ReasonCode              string          `json:"reasonCode,omitempty"`
	Outputs                 Inputs          `json:"outputs"`
	Complete                bool            `json:"complete"`
	Reusable                bool            `json:"reusable"`
	ValidUntil              *time.Time      `json:"validUntil,omitempty"`
	ExternalID              string          `json:"externalId,omitempty"`
}
type TargetReceipt struct {
	ServerID        string `json:"serverId"`
	Status          string `json:"status"`
	OperationID     string `json:"operationId,omitempty"`
	SourceAttemptID string `json:"sourceAttemptId,omitempty"`
}

// ChildCreator writes only through q; dispatch/network work is forbidden here.
type ChildInput struct {
	ID, ProjectID, RootID, ParentID string
	Trigger                         Trigger
	CreatedAt                       time.Time
}
type ChildCreator func(context.Context, configstate.DBTX, ChildInput) error

// Dispatch is attempted once after commit. Any error is conservatively unknown.
type Dispatch func(context.Context, string) error
type SnapshotProvider func(context.Context, string) (Snapshot, error)
type AdmissionCheck func(context.Context, configstate.DBTX, Snapshot) error
type Option func(*Service)
type Service struct {
	db               *sql.DB
	sealer           Sealer
	blobs            BlobStore
	child            ChildCreator
	dispatch         Dispatch
	reject           func(context.Context, string) error
	provider         SnapshotProvider
	admission        AdmissionCheck
	owner            string
	maxPayload       int
	maxFileBytes     int64
	imageVerifier    func(context.Context, ImageRef) error
	preflight        func(context.Context, Snapshot, *Plan) ([]Reason, error)
	resourceVerifier ResourceVerifier
	targetVerifier   TargetVerifier
}

func WithImageVerifier(f func(context.Context, ImageRef) error) Option {
	return func(s *Service) { s.imageVerifier = f }
}

func WithChildCreator(f ChildCreator) Option { return func(s *Service) { s.child = f } }
func WithDispatch(f Dispatch) Option         { return func(s *Service) { s.dispatch = f } }
func WithRejectedChild(f func(context.Context, string) error) Option {
	return func(s *Service) { s.reject = f }
}
func NewDispatchRejected(cause error) error          { return errors.Join(ErrDispatchRejected, cause) }
func WithSnapshotProvider(f SnapshotProvider) Option { return func(s *Service) { s.provider = f } }
func WithAdmissionCheck(f AdmissionCheck) Option     { return func(s *Service) { s.admission = f } }
func WithBlobStore(b BlobStore) Option               { return func(s *Service) { s.blobs = b } }

// Execution is scoped to an actual worker attempt, never a front-end request.
type Execution interface {
	Snapshot() Snapshot
	Node(stageID, jobID string) (NodeRef, bool)
	ControlNode(stageID, controlKey string) (NodeRef, bool)
	Decision(NodeRef) Decision
	Inputs(context.Context, NodeRef) (Inputs, error)
	Begin(context.Context, NodeRef) error
	Finish(context.Context, NodeRef, Outcome) error
	BindCommit(context.Context, string) error
	Control(context.Context, NodeRef, Outcome) error
}
type executionContextKey struct{}

func WithExecution(ctx context.Context, e Execution) context.Context {
	return context.WithValue(ctx, executionContextKey{}, e)
}
func ExecutionFrom(ctx context.Context) (Execution, bool) {
	e, ok := ctx.Value(executionContextKey{}).(Execution)
	return e, ok
}

type currentNodeKey struct{}

func NodeOutcome(ctx context.Context, n NodeRef) (Outcome, error) {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return Outcome{}, ErrUnavailable
	}
	result, ok := ex.(interface {
		Result(context.Context, NodeRef) (Outcome, error)
	})
	if !ok {
		return Outcome{}, ErrUnavailable
	}
	return result.Result(ctx, n)
}

func WithNode(ctx context.Context, n NodeRef) (context.Context, error) {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return ctx, ErrUnavailable
	}
	actual, found := ex.Node(n.ExecutionStageID, n.ExecutionJobID)
	if n.ControlKey != "" {
		actual, found = ex.ControlNode(n.ExecutionStageID, n.ControlKey)
	}
	if !found || digest(actual) != digest(n) {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, currentNodeKey{}, n), nil
}
func NodeFrom(ctx context.Context) (NodeRef, bool) {
	n, ok := ctx.Value(currentNodeKey{}).(NodeRef)
	return n, ok
}
func PreviousOutcome(ctx context.Context, n NodeRef) (Outcome, string, error) {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return Outcome{}, "", ErrUnavailable
	}
	previous, ok := ex.(interface {
		Previous(context.Context, NodeRef) (Outcome, string, error)
	})
	if !ok {
		return Outcome{}, "", ErrUnavailable
	}
	return previous.Previous(ctx, n)
}

func WithPreflightCheck(f func(context.Context, Snapshot, *Plan) ([]Reason, error)) Option {
	return func(s *Service) { s.preflight = f }
}
