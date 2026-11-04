// Package kubernetes owns protected cluster credentials and deployment evidence.
// No method in this package implicitly contacts a cluster. Authentication and
// recent-password confirmation belong at the authenticated HTTP boundary.
package kubernetes

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrInvalid           = errors.New("kubernetes_invalid_input")
	ErrNotFound          = errors.New("kubernetes_not_found")
	ErrConflict          = errors.New("kubernetes_revision_conflict")
	ErrInUse             = errors.New("kubernetes_in_use")
	ErrDenied            = errors.New("kubernetes_not_authorized")
	ErrDecrypt           = errors.New("kubernetes_credential_unavailable")
	ErrRuntimeUnverified = errors.New("secret_runtime_unverified")
	ErrOwner             = errors.New("kubernetes_owner_mismatch")
	ErrHeld              = errors.New("kubernetes_workload_held")
)

const (
	MaxImportBytes          = 256 << 10
	MaxCredentialBytes      = 32 << 10
	MaxPrivateResponseBytes = 1 << 20
	MaxDiagnosticBytes      = 16 << 10
	CredentialPrefix        = "kcred_"
	ProfileProtocol         = "kubectl-sh-v1"
)

type Sealer interface {
	SealSecret([]byte) ([]byte, error)
	OpenSecret([]byte) ([]byte, error)
}
type Service struct {
	db     *sql.DB
	sealer Sealer
	now    func() time.Time
}

func New(db *sql.DB, sealer Sealer) *Service { return &Service{db: db, sealer: sealer, now: time.Now} }

// CredentialInput is a write-only import DTO. Never log or serialize an input.
type CredentialInput struct {
	Name          string `json:"name"`
	Mode          string `json:"mode"`
	Context       string `json:"context"`
	Kubeconfig    string `json:"kubeconfig,omitempty"`
	Server        string `json:"server,omitempty"`
	CA            string `json:"ca,omitempty"`
	Token         string `json:"token,omitempty"`
	TLSServerName string `json:"tlsServerName,omitempty"`
	Enabled       bool   `json:"enabled"`
}
type CredentialPreview struct {
	Mode          string `json:"mode"`
	Context       string `json:"context"`
	Server        string `json:"server"`
	TLSServerName string `json:"tlsServerName,omitempty"`
	CAFingerprint string `json:"caFingerprint"`
	ClusterKey    string `json:"clusterKey"`
}
type Credential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	Enabled  bool   `json:"enabled"`
	CredentialPreview
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// Executor is server-owned. SSH host-key fingerprints never come from a job.
type Executor struct {
	Kind          string `json:"kind"`
	ServerID      string `json:"serverId,omitempty"`
	HostKeySHA256 string `json:"hostKeySHA256,omitempty"`
}
type ToolProfile struct {
	Protocol             string `json:"protocol"`
	Image                string `json:"image"`
	Architecture         string `json:"architecture"`
	KubectlPath          string `json:"kubectlPath"`
	UID                  int64  `json:"uid"`
	Network              string `json:"network"`
	RegistryCredentialID string `json:"registryCredentialId,omitempty"`
}
type ConnectionInput struct {
	Name         string      `json:"name"`
	CredentialID string      `json:"credentialId"`
	Enabled      bool        `json:"enabled"`
	Profile      ToolProfile `json:"profile"`
	Executors    []Executor  `json:"executors"`
}
type Connection struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	ConnectionInput
	CredentialRevision int64  `json:"credentialRevision"`
	Context            string `json:"context"`
	Server             string `json:"server"`
	ClusterKey         string `json:"clusterKey"`
	ProfileFingerprint string `json:"profileFingerprint"`
	CreatedAt          int64  `json:"createdAt"`
	UpdatedAt          int64  `json:"updatedAt"`
}
type GrantInput struct {
	ProjectID       string   `json:"projectId"`
	Environment     string   `json:"environment"`
	Namespace       string   `json:"namespace"`
	Deployment      string   `json:"deployment"`
	Containers      []string `json:"containers"`
	Executor        Executor `json:"executor"`
	RequireApproval *bool    `json:"requireApproval,omitempty"`
	Enabled         bool     `json:"enabled"`
}
type Grant struct {
	ID              string   `json:"id"`
	ConnectionID    string   `json:"connectionId"`
	Revision        int64    `json:"revision"`
	ProjectID       string   `json:"projectId"`
	Environment     string   `json:"environment"`
	Namespace       string   `json:"namespace"`
	Deployment      string   `json:"deployment"`
	Containers      []string `json:"containers"`
	Executor        Executor `json:"executor"`
	RequireApproval bool     `json:"requireApproval"`
	Enabled         bool     `json:"enabled"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
}

// Target is an exact scope, never a wildcard or user-specified API endpoint.
type Target struct {
	ProjectID    string   `json:"projectId"`
	Environment  string   `json:"environment"`
	ConnectionID string   `json:"connectionId"`
	CredentialID string   `json:"credentialId"`
	Context      string   `json:"context"`
	Namespace    string   `json:"namespace"`
	Deployment   string   `json:"deployment"`
	Container    string   `json:"container"`
	Executor     Executor `json:"executor"`
}
type Authorization struct {
	Target             Target      `json:"target"`
	ConnectionRevision int64       `json:"connectionRevision"`
	CredentialRevision int64       `json:"credentialRevision"`
	GrantID            string      `json:"grantId"`
	GrantRevision      int64       `json:"grantRevision"`
	RequireApproval    bool        `json:"requireApproval"`
	ClusterKey         string      `json:"clusterKey"`
	Profile            ToolProfile `json:"profile"`
	ProfileFingerprint string      `json:"profileFingerprint"`
	Fingerprint        string      `json:"fingerprint"`
}

// RuntimeProof is produced by the private runner after actual capability checks;
// it is never accepted from an HTTP/job DTO. Each proof binds one authorized
// operation, runtime identity and the current tool profile.
type RuntimeProof struct {
	OperationID              string `json:"operationId"`
	OwnerEpoch               string `json:"ownerEpoch"`
	EngineID                 string `json:"engineId"`
	ContainerID              string `json:"containerId"`
	ProfileFingerprint       string `json:"profileFingerprint"`
	AuthorizationFingerprint string `json:"authorizationFingerprint"`
	Linux                    bool   `json:"linux"`
	PrivateTmpfs             bool   `json:"privateTmpfs"`
	SwapProtected            bool   `json:"swapProtected"`
	CoreDisabled             bool   `json:"coreDisabled"`
	NonRoot                  bool   `json:"nonRoot"`
	ReadOnlyRoot             bool   `json:"readOnlyRoot"`
	BootstrapVerified        bool   `json:"bootstrapVerified"`
	ClientVersionVerified    bool   `json:"clientVersionVerified"`
	HostIdentityVerified     bool   `json:"hostIdentityVerified"`
}

// SecretConsumer receives normalized config only for bounded stdin delivery.
// It must not retain, log, marshal, or put the bytes in arguments/environment.
type SecretConsumer func(context.Context, []byte) error
