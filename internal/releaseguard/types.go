// Package releaseguard performs bounded, explicitly requested, read-only
// post-deployment observations. It has no deployment or remediation capability.
package releaseguard

import (
	"errors"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/target"
)

var (
	ErrInvalid     = errors.New("releaseguard: invalid request")
	ErrNotFound    = errors.New("releaseguard: not found")
	ErrConflict    = errors.New("releaseguard: plan or state changed")
	ErrCapacity    = errors.New("releaseguard: observation capacity reached")
	ErrUnavailable = errors.New("releaseguard: capability unavailable")
)

const EngineVersion = "releaseguard-v1"

type PrepareInput struct {
	Mode            string            `json:"mode"`
	AttemptID       string            `json:"attemptId"`
	RunID           string            `json:"runId"`
	ArtifactID      string            `json:"artifactId"`
	ServerIDs       []string          `json:"serverIds"`
	DeployConfig    map[string]string `json:"deployConfig"`
	Strategy        string            `json:"strategy"`
	HealthCheck     *HealthGateInput  `json:"healthCheck"`
	DurationSeconds int               `json:"durationSeconds"`
	IncludeLogs     bool              `json:"includeLogs"`
	ParentGuardID   string            `json:"parentGuardId"`
}

// These fields describe the existing deployment confirmation only. The
// observer hashes them for binding and never executes the health command.
type HealthGateInput struct {
	Type            string   `json:"type"`
	URL             string   `json:"url"`
	Command         []string `json:"command"`
	Retries         int      `json:"retries"`
	IntervalSeconds int      `json:"intervalSeconds"`
	TimeoutSeconds  int      `json:"timeoutSeconds"`
}
type DeploymentParameters struct {
	Strategy    string
	HealthCheck *HealthGateInput
}

type PlanTarget struct {
	ID                    string                      `json:"id"`
	ServerID              string                      `json:"serverId"`
	ServerName            string                      `json:"serverName"`
	ContainerName         string                      `json:"containerName"`
	ContainerID           string                      `json:"containerId"`
	WorkloadKey           string                      `json:"workloadKey"`
	ConnectionFingerprint string                      `json:"connectionFingerprint"`
	ExpectedImage         deployattempt.ExpectedImage `json:"expectedImage"`
	Availability          string                      `json:"availability"`
	Limitations           []string                    `json:"limitations"`
}

type Plan struct {
	ID              string       `json:"id"`
	Hash            string       `json:"hash"`
	ExpiresAt       time.Time    `json:"expiresAt"`
	Mode            string       `json:"mode"`
	ProjectID       string       `json:"projectId"`
	RunID           string       `json:"runId"`
	AttemptID       string       `json:"attemptId"`
	ArtifactID      string       `json:"artifactId"`
	Environment     string       `json:"environment"`
	DurationSeconds int          `json:"durationSeconds"`
	IncludeLogs     bool         `json:"includeLogs"`
	ParentGuardID   string       `json:"parentGuardId"`
	ConfigHash      string       `json:"configHash"`
	ConfigRevision  int64        `json:"configRevision"`
	Targets         []PlanTarget `json:"targets"`
	Limitations     []string     `json:"limitations"`
}

type StartInput struct {
	PlanID         string `json:"planId"`
	PlanHash       string `json:"planHash"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type Coverage struct {
	SampleSlots       []int      `json:"sampleSlots,omitempty"`
	ExpectedSamples   int        `json:"expectedSamples"`
	SuccessfulSamples int        `json:"successfulSamples"`
	MaxGapSeconds     int        `json:"maxGapSeconds"`
	Complete          bool       `json:"complete"`
	FirstSample       *time.Time `json:"firstSample,omitempty"`
	LastSample        *time.Time `json:"lastSample,omitempty"`
}

type Condition struct {
	Name        string   `json:"name"`
	Result      string   `json:"result"`
	Reason      string   `json:"reason"`
	EvidenceIDs []string `json:"evidenceIds"`
}

// RuntimeSnapshot contains only narrow, typed Docker facts, never Config,
// environment variables, mounts, health command output, or raw stderr.
type RuntimeSnapshot struct {
	Outcome        string                 `json:"outcome"`
	ContainerID    string                 `json:"containerId"`
	CreatedAt      string                 `json:"createdAt"`
	StartedAt      string                 `json:"startedAt"`
	FinishedAt     string                 `json:"finishedAt"`
	ConfigID       string                 `json:"configId"`
	DeclaredImage  string                 `json:"declaredImage"`
	RepoDigests    []string               `json:"repoDigests"`
	Platform       deployattempt.Platform `json:"platform"`
	Running        bool                   `json:"running"`
	OOMKilled      bool                   `json:"oomKilled"`
	ExitCode       int                    `json:"exitCode"`
	RestartCount   int                    `json:"restartCount"`
	Health         string                 `json:"health"`
	ImageAvailable bool                   `json:"imageAvailable"`
	TargetChanged  bool                   `json:"targetChanged,omitempty"`
	// RepoDigests alone cannot prove manifest/index media types.
	RepositoryDigestKind     string    `json:"repositoryDigestKind,omitempty"`
	VerifiedRepositoryDigest string    `json:"verifiedRepositoryDigest,omitempty"`
	RepositoryDigestSource   string    `json:"repositoryDigestSource,omitempty"`
	CollectedAt              time.Time `json:"collectedAt"`
	Reason                   string    `json:"reason"`
	Truncated                bool      `json:"truncated"`
}

type TargetObservation struct {
	PlanTarget
	VerificationResult  *string          `json:"verificationResult"`
	Coverage            Coverage         `json:"coverage"`
	Conditions          []Condition      `json:"conditions"`
	ReadyAt             *time.Time       `json:"readyAt,omitempty"`
	Deadline            time.Time        `json:"deadline"`
	Actual              *RuntimeSnapshot `json:"actual,omitempty"`
	InitialRestartCount *int             `json:"initialRestartCount,omitempty"`
	HealthRequired      *bool            `json:"healthRequired,omitempty"`
	Terminated          bool             `json:"terminated"`
	Completed           bool             `json:"completed"`
}

type Evidence struct {
	ID          string    `json:"id"`
	Sequence    int64     `json:"sequence"`
	TargetID    string    `json:"targetId"`
	Kind        string    `json:"kind"`
	EventAt     time.Time `json:"eventAt"`
	CollectedAt time.Time `json:"collectedAt"`
	Reason      string    `json:"reason"`
	Truncated   bool      `json:"truncated"`
	Summary     string    `json:"summary"`
	Value       any       `json:"value"`
}

type TimelineItem struct {
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	TargetID    string    `json:"targetId"`
	Summary     string    `json:"summary"`
	EvidenceIDs []string  `json:"evidenceIds"`
}

type Candidate struct {
	ID                    string   `json:"id"`
	Title                 string   `json:"title"`
	SupportingEvidenceIDs []string `json:"supportingEvidenceIds"`
	CounterEvidenceIDs    []string `json:"counterEvidenceIds"`
	MissingEvidence       []string `json:"missingEvidence"`
	NextReadOnlyCheck     string   `json:"nextReadOnlyCheck"`
}

type Investigation struct {
	SourceComparisons []SourceComparison `json:"sourceComparisons"`
	Baselines         []TargetBaseline   `json:"baselines"`
	InputHash         string             `json:"inputHash"`
	Status            string             `json:"status"`
	InvestigatedAt    *time.Time         `json:"investigatedAt,omitempty"`
	BaselineStatus    string             `json:"baselineStatus"`
	Candidates        []Candidate        `json:"candidates"`
	Limitations       []string           `json:"limitations"`
}

// SourceComparison compares the two recorded deployment endpoints directly.
// It contains no source text, patch, credentials or guessed commit expansion.
type SourceComparison struct {
	TargetID       string             `json:"targetId"`
	WorkloadKey    string             `json:"workloadKey"`
	Available      bool               `json:"available"`
	Reason         string             `json:"reason"`
	BaseSHA        string             `json:"baseSha"`
	CurrentSHA     string             `json:"currentSha"`
	ComparisonKind string             `json:"comparisonKind"`
	Files          []SourceFileChange `json:"files"`
	Truncated      bool               `json:"truncated"`
}

type SourceFileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type TargetBaseline struct {
	TargetID             string               `json:"targetId"`
	Status               string               `json:"status"`
	EvidenceID           string               `json:"evidenceId"`
	PriorAttemptID       string               `json:"priorAttemptId"`
	PriorRunID           string               `json:"priorRunId"`
	AgeSeconds           int64                `json:"ageSeconds"`
	MissingEvidence      []string             `json:"missingEvidence"`
	HistoricalCandidates []HistoricalBaseline `json:"historicalCandidates"`
}

type HistoricalBaseline struct {
	AttemptID  string    `json:"attemptId"`
	RunID      string    `json:"runId"`
	At         time.Time `json:"at"`
	AgeSeconds int64     `json:"ageSeconds"`
	Status     string    `json:"status"`
}

type Comparison struct {
	SameConditions bool     `json:"sameConditions"`
	Changes        []string `json:"changes"`
}

type Report struct {
	ID                 string              `json:"id"`
	ProjectID          string              `json:"projectId"`
	RunID              string              `json:"runId"`
	AttemptID          string              `json:"attemptId"`
	Plan               Plan                `json:"plan"`
	ExecutionStatus    string              `json:"executionStatus"`
	VerificationResult *string             `json:"verificationResult"`
	AttributionStatus  string              `json:"attributionStatus"`
	CreatedAt          time.Time           `json:"createdAt"`
	StartedAt          *time.Time          `json:"startedAt,omitempty"`
	FinishedAt         *time.Time          `json:"finishedAt,omitempty"`
	Reason             string              `json:"reason"`
	IdentityTrust      string              `json:"identityTrust"`
	Targets            []TargetObservation `json:"targets"`
	Timeline           []TimelineItem      `json:"timeline"`
	Investigation      Investigation       `json:"investigation"`
	EventsCursor       int64               `json:"eventsCursor"`
	EvidenceExpiresAt  time.Time           `json:"evidenceExpiresAt"`
	EvidenceExpired    bool                `json:"evidenceExpired"`
	ParentGuardID      string              `json:"parentGuardId"`
	Comparison         Comparison          `json:"comparison"`
	EngineVersion      string              `json:"engineVersion"`
	EvidenceSetHash    string              `json:"evidenceSetHash"`
}

// Private state is never returned by HTTP. A client cannot supply snapshots.
type privateState struct {
	Bindings      map[string]target.ConnectionSnapshot `json:"bindings"`
	LastLogs      map[string]time.Time                 `json:"lastLogs"`
	LogBytes      map[string]int                       `json:"logBytes"`
	EvidenceBytes int                                  `json:"evidenceBytes"`
	Schedule      map[string]time.Time                 `json:"schedule"`
	Revision      int64                                `json:"revision"`
}

type document struct {
	Report  Report       `json:"report"`
	Private privateState `json:"private"`
}

func terminal(status string) bool {
	switch status {
	case "completed", "stopped", "interrupted", "blocked", "tool_error", "expired":
		return true
	}
	return false
}

func strptr(s string) *string { return &s }
