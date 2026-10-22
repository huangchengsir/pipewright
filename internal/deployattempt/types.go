// Package deployattempt records immutable deployment identities and append-only
// execution receipts. It never executes, retries, or inspects remote workloads.
package deployattempt

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid  = errors.New("deployattempt: invalid input")
	ErrNotFound = errors.New("deployattempt: not found")
	ErrConflict = errors.New("deployattempt: receipt conflict")
)

const (
	DigestManifest          = "manifest"
	DigestIndex             = "index"
	DigestConfig            = "config_id"
	DigestRepositoryUnknown = "repository_digest_unknown"
	DigestUnavailable       = "unavailable"
	Started                 = "started"
	ActivationStarted       = "activation_started"
	Activated               = "activated"
	Succeeded               = "succeeded"
	DeployFailed            = "deploy_failed"
	RolledBack              = "rolled_back"
	OutcomeUnknown          = "outcome_unknown"
	Aborted                 = "aborted"
	IdentityObserved        = "identity_observed"
)

type Source struct {
	RunID           string `json:"runId"`
	StageID         string `json:"stageId"`
	ProjectID       string `json:"projectId"`
	Environment     string `json:"environment"`
	Initiator       string `json:"initiator"`
	ConfigRevision  int64  `json:"configRevision"`
	ResolvedFullSHA string `json:"resolvedFullSha"`
}

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

// Repository digests and config IDs are deliberately separate. Unknown media
// types and index-to-manifest mappings must remain unknown, never guessed.
type ExpectedImage struct {
	Reference  string   `json:"reference"`
	Repository string   `json:"repository"`
	Digest     string   `json:"digest"`
	DigestKind string   `json:"digestKind"`
	Platform   Platform `json:"platform"`
}

type ActualImage struct {
	ContainerID        string   `json:"containerId"`
	ContainerCreatedAt string   `json:"containerCreatedAt"`
	ContainerStartedAt string   `json:"containerStartedAt"`
	ConfigID           string   `json:"configId"`
	RepoDigests        []string `json:"repoDigests"`
	Platform           Platform `json:"platform"`
	// False unless a separately authorized observer proves the relationship.
	ManifestMappingVerified bool `json:"manifestMappingVerified"`
}

type BeginInput struct {
	RunID           string
	StageID         string
	Initiator       string
	ResolvedFullSHA string
}

type TargetInput struct {
	ArtifactID            string        `json:"artifactId"`
	ArtifactName          string        `json:"artifactName"`
	ServerID              string        `json:"serverId"`
	ServerName            string        `json:"serverName"`
	ContainerName         string        `json:"containerName"`
	ConnectionFingerprint string        `json:"connectionFingerprint"`
	ExpectedImage         ExpectedImage `json:"expectedImage"`
}

type Receipt struct {
	ID                     string      `json:"id"`
	TargetID               string      `json:"targetId"`
	Sequence               int64       `json:"sequence"`
	Kind                   string      `json:"kind"`
	At                     time.Time   `json:"at"`
	ActualImage            ActualImage `json:"actualImage"`
	PreviousImageReference string      `json:"previousImageReference"`
	RollbackContainerID    string      `json:"rollbackContainerId"`
	HealthGate             string      `json:"healthGate"`
	Reason                 string      `json:"reason"`
}

type Target struct {
	ID        string `json:"id"`
	AttemptID string `json:"attemptId"`
	Source    Source `json:"source"`
	TargetInput
	WorkloadKey   string    `json:"workloadKey"`
	IdentityTrust string    `json:"identityTrust"`
	CreatedAt     time.Time `json:"createdAt"`
	Outcome       string    `json:"outcome"`
	Receipts      []Receipt `json:"receipts"`
}

type Attempt struct {
	ID        string    `json:"id"`
	Source    Source    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
	Targets   []Target  `json:"targets"`
}

// Writer is implemented by Store and allows deployment tests to inject precise
// persistence failures without granting remote capabilities to the ledger.
type Writer interface {
	Begin(context.Context, BeginInput) (*Attempt, error)
	StartTarget(context.Context, string, TargetInput) (*Target, error)
	AppendReceipt(context.Context, string, Receipt) (*Receipt, error)
}
