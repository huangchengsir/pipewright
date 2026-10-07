// Package opschat owns local operation sessions; it never imports AI or HTTP adapters.
package opschat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
	"time"
)

var (
	ErrNotFound    = errors.New("opschat: not found")
	ErrConflict    = errors.New("opschat: version or task conflict")
	ErrInvalid     = errors.New("opschat: invalid input")
	ErrQuota       = errors.New("opschat: resource limit reached")
	ErrUnavailable = errors.New("opschat: service not configured")
	ErrDecrypt     = errors.New("opschat: content cannot be decrypted")
	ErrLease       = errors.New("opschat: executor lease unavailable")
	ErrApproval    = errors.New("opschat: confirmation invalid or expired")
	ErrConsent     = errors.New("opschat: analysis consent changed")
	ErrReset       = errors.New("opschat: replay requires snapshot")
	ErrStorage     = errors.New("opschat: local persistence failed")
	ErrVerifyFirst = errors.New("opschat: verify remote effect before retry")
)

const (
	Queued                  = "queued"
	Planning                = "planning"
	Running                 = "running"
	AwaitingConfirmation    = "awaiting_confirmation"
	AwaitingAnalysisConsent = "awaiting_analysis_consent"
	Succeeded               = "succeeded"
	PartialFailed           = "partial_failed"
	Failed                  = "failed"
	Interrupted             = "interrupted"
	Unknown                 = "unknown"
)

type Limits struct {
	Sessions        int `json:"sessions"`
	Entries         int `json:"entries"`
	SessionBytes    int `json:"sessionBytes"`
	MessageBytes    int `json:"messageBytes"`
	Targets         int `json:"targets"`
	Calls           int `json:"calls"`
	SSHBytes        int `json:"sshBytes"`
	SSHLines        int `json:"sshLines"`
	RunBytes        int `json:"runBytes"`
	ContextBytes    int `json:"contextBytes"`
	ContextMessages int `json:"contextMessages"`
	Replay          int `json:"replay"`
}

var resourceLimits = Limits{100, 2000, 8 << 20, 8 << 10, 8, 24, 64 << 10, 1000, 512 << 10, 32 << 10, 20, 4000}

type ProviderInfo struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// ConfigHash changes whenever destination or configuration changes. No key or URL is sent to the model.
	ConfigHash string `json:"configHash"`
}
type Capabilities struct {
	Available      bool         `json:"available"`
	ModelAvailable bool         `json:"modelAvailable"`
	Provider       ProviderInfo `json:"provider"`
	Limits         Limits       `json:"limits"`
}
type Options struct {
	DB       *sql.DB
	Vault    vault.Vault
	Executor target.LimitedExecutor
	Model    Model
	// LocalRecorder must be audit.New(db, masker, nil), never the shared remote-sink recorder.
	LocalRecorder audit.Recorder
	Masker        *mask.Masker
}
type Session struct {
	ID          string    `json:"id"`
	Revision    int64     `json:"revision"`
	Title       string    `json:"title"`
	Draft       string    `json:"draft"`
	ServerIDs   []string  `json:"serverIds"`
	ActiveRunID string    `json:"activeRunId"`
	Watermark   int64     `json:"watermark"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type CreateInput struct {
	Title     string   `json:"title"`
	ServerIDs []string `json:"serverIds"`
}
type PatchInput struct {
	Revision  int64     `json:"revision"`
	Title     *string   `json:"title,omitempty"`
	Draft     *string   `json:"draft,omitempty"`
	ServerIDs *[]string `json:"serverIds,omitempty"`
}
type TurnInput struct {
	ClientRequestID string          `json:"clientRequestId"`
	Revision        int64           `json:"revision"`
	Text            string          `json:"text,omitempty"`
	ToolID          string          `json:"toolId,omitempty"`
	Args            json.RawMessage `json:"args,omitempty"`
}
type RetryInput struct {
	ClientRequestID string   `json:"clientRequestId"`
	Revision        int64    `json:"revision"`
	CallIDs         []string `json:"callIds"`
}
type Run struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"sessionId"`
	ClientRequestID string    `json:"clientRequestId"`
	Status          string    `json:"status"`
	CancelRequested bool      `json:"cancelRequested"`
	CreatedAt       time.Time `json:"createdAt"`
}
type ToolArgs struct {
	Container string `json:"container,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	Action    string `json:"action,omitempty"`
}
type Action struct {
	ToolID        string          `json:"toolId"`
	Args          json.RawMessage `json:"args"`
	TargetIndexes []int           `json:"targetIndexes"`
}
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}
type PlanRequest struct {
	Messages         []Message `json:"messages"`
	Targets          []string  `json:"targets"`
	Tools            []Tool    `json:"tools"`
	MaxResponseBytes int       `json:"maxResponseBytes"`
}
type Plan struct {
	Text         string   `json:"text"`
	ManualAdvice string   `json:"manualAdvice,omitempty"`
	Actions      []Action `json:"actions"`
}
type AnalysisItem struct {
	CallID      string `json:"callId"`
	TargetAlias string `json:"targetAlias"`
	ToolID      string `json:"toolId"`
	Output      string `json:"output"`
	Error       string `json:"error"`
	Status      string `json:"status"`
}
type AnalysisRequest struct {
	Provider         ProviderInfo   `json:"provider"`
	Items            []AnalysisItem `json:"items"`
	MaxResponseBytes int            `json:"maxResponseBytes"`
}

// Info is strictly local. Adapters MUST bound provider responses while reading, not after buffering.
type Model interface {
	Info() ProviderInfo
	Plan(context.Context, PlanRequest) (Plan, error)
	Analyze(context.Context, AnalysisRequest) (string, error)
}

// SecretRegistrar registers current (including disabled) provider keys with the
// shared Masker. It must perform only bounded local configuration reads.
type SecretRegistrar interface {
	RegisterSecrets(context.Context) error
}
type Call struct {
	Resources   *HostResources `json:"resources,omitempty"`
	ID          string         `json:"callId"`
	RunID       string         `json:"runId"`
	SessionID   string         `json:"sessionId"`
	ServerID    string         `json:"serverId"`
	ServerName  string         `json:"serverName"`
	ToolID      string         `json:"toolId"`
	Args        ToolArgs       `json:"args"`
	Object      string         `json:"object"`
	Status      string         `json:"status"`
	ArgsHash    string         `json:"argsHash"`
	TargetHash  string         `json:"targetHash"`
	CollectedAt *time.Time     `json:"collectedAt,omitempty"`
	ExitCode    *int           `json:"exitCode,omitempty"`
	Output      string         `json:"output"`
	Error       string         `json:"error"`
	Truncated   bool           `json:"truncated"`
}
type BoundCall struct {
	CallID     string `json:"callId"`
	ServerID   string `json:"serverId"`
	ToolID     string `json:"toolId"`
	Object     string `json:"object"`
	ArgsHash   string `json:"argsHash"`
	TargetHash string `json:"targetHash"`
}
type Confirmation struct {
	RunID     string      `json:"runId"`
	Nonce     string      `json:"nonce"`
	ExpiresAt time.Time   `json:"expiresAt"`
	Calls     []BoundCall `json:"calls"`
	Valid     bool        `json:"valid"`
}
type ConfirmInput struct {
	RunID string      `json:"runId"`
	Nonce string      `json:"nonce"`
	Calls []BoundCall `json:"calls"`
}
type AnalysisPreview struct {
	CallIDs  []string       `json:"callIds"`
	Items    []AnalysisItem `json:"items"`
	Hash     string         `json:"hash"`
	Provider ProviderInfo   `json:"provider"`
}
type AnalysisInput struct {
	ClientRequestID string       `json:"clientRequestId"`
	Revision        int64        `json:"revision"`
	CallIDs         []string     `json:"callIds"`
	PreviewHash     string       `json:"previewHash"`
	Provider        ProviderInfo `json:"provider"`
	Consent         bool         `json:"consent"`
}
type Entry struct {
	SessionID string    `json:"sessionId"`
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	RunID     string    `json:"runId,omitempty"`
	CallID    string    `json:"callId,omitempty"`
	Text      string    `json:"text,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
type EntryPage struct {
	Entries   []Entry `json:"entries"`
	Cursor    string  `json:"cursor"`
	Watermark int64   `json:"watermark"`
	Reset     bool    `json:"reset"`
}
type SessionPage struct {
	Sessions        []Session `json:"sessions"`
	Cursor          string    `json:"cursor"`
	ActiveSessionID string    `json:"activeSessionId"`
}
type Snapshot struct {
	Session       Session        `json:"session"`
	Runs          []Run          `json:"runs"`
	Calls         []Call         `json:"calls"`
	Confirmations []Confirmation `json:"confirmations"`
	Entries       EntryPage      `json:"entries"`
	Watermark     int64          `json:"watermark"`
}
type sessionBody struct {
	Title     string
	Draft     string
	ServerIDs []string
	Named     bool
}
type runBody struct {
	ReadBytes    int
	RetryActions []Action
	RetryCallIDs []string
	Turn         TurnInput
	Targets      []target.ConnectionSnapshot
	Analysis     *AnalysisPreview
	ActiveMillis int64
	ModelCalls   int
	OutputBytes  int
}
type callBody struct {
	ReadBytes     int
	ReadLines     int
	ReadTruncated bool
	Dispatched    bool
	View          Call
	Snapshot      target.ConnectionSnapshot
}
type approvalBody struct {
	Nonce string
	Calls []BoundCall
}
