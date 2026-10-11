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
	Context   string   `json:"context,omitempty"`
	Title     string   `json:"title"`
	ServerIDs []string `json:"serverIds"`
}
type ActivateInput struct {
	Context string `json:"context,omitempty"`
}
type PatchInput struct {
	Revision  int64     `json:"revision"`
	Title     *string   `json:"title,omitempty"`
	Draft     *string   `json:"draft,omitempty"`
	ServerIDs *[]string `json:"serverIds,omitempty"`
}
type TurnInput struct {
	AnalyzeResults  bool            `json:"analyzeResults,omitempty"`
	Provider        ProviderInfo    `json:"provider,omitempty"`
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
	Phase           string    `json:"phase,omitempty"`
	ID              string    `json:"id"`
	SessionID       string    `json:"sessionId"`
	ClientRequestID string    `json:"clientRequestId"`
	Status          string    `json:"status"`
	CancelRequested bool      `json:"cancelRequested"`
	CreatedAt       time.Time `json:"createdAt"`
}
type ToolArgs struct {
	Argv      []string `json:"argv,omitempty"`
	Container string   `json:"container,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	Lines     int      `json:"lines,omitempty"`
	Action    string   `json:"action,omitempty"`
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
	Question         string                 `json:"question,omitempty"`
	Provider         ProviderInfo           `json:"provider,omitempty"`
	Messages         []Message              `json:"messages"`
	Targets          []string               `json:"targets"`
	Tools            []Tool                 `json:"tools"`
	MaxResponseBytes int                    `json:"maxResponseBytes"`
	ContainerFacts   []ContainerObservation `json:"containerFacts,omitempty"`
	Continuation     bool                   `json:"continuation,omitempty"`
}
type ContainerFact struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	State string `json:"state"`
}
type ContainerObservation struct {
	TargetAlias string          `json:"targetAlias"`
	CollectedAt *time.Time      `json:"collectedAt,omitempty"`
	Containers  []ContainerFact `json:"containers"`
	Truncated   bool            `json:"truncated"`
	Unavailable string          `json:"unavailable,omitempty"`
}
type Plan struct {
	Text           string   `json:"text"`
	ManualAdvice   string   `json:"manualAdvice,omitempty"`
	Actions        []Action `json:"actions"`
	NeedsDiscovery bool     `json:"needsDiscovery,omitempty"`
}
type AnalysisItem struct {
	Truncated   bool       `json:"truncated"`
	CollectedAt *time.Time `json:"collectedAt,omitempty"`
	ExitCode    *int       `json:"exitCode,omitempty"`
	CallID      string     `json:"callId"`
	TargetAlias string     `json:"targetAlias"`
	ToolID      string     `json:"toolId"`
	Output      string     `json:"output"`
	Error       string     `json:"error"`
	Status      string     `json:"status"`
}
type AnalysisRequest struct {
	Question         string         `json:"question"`
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

type StreamChunk struct {
	Text     string
	Fallback bool
}

// StreamModel publishes only natural text; adapters validate complete plans before returning actions.
type StreamModel interface {
	PlanStream(context.Context, PlanRequest, func(StreamChunk) error) (Plan, error)
	AnalyzeStream(context.Context, AnalysisRequest, func(StreamChunk) error) (string, error)
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
	RunID           string       `json:"runId,omitempty"`
	AnalyzeResults  bool         `json:"analyzeResults,omitempty"`
	ClientRequestID string       `json:"clientRequestId"`
	Revision        int64        `json:"revision"`
	CallIDs         []string     `json:"callIds"`
	PreviewHash     string       `json:"previewHash"`
	Provider        ProviderInfo `json:"provider"`
	Consent         bool         `json:"consent"`
}
type Entry struct {
	// TargetNames is reconstructed for local display, never persisted or sent to models.
	TargetNames  map[string]string `json:"targetNames,omitempty"`
	ReplaceSeq   int64             `json:"replaceSeq,omitempty"`
	RevisionSeq  int64             `json:"revisionSeq,omitempty"`
	RequestID    string            `json:"requestId,omitempty"`
	Call         *Call             `json:"call,omitempty"`
	Run          *Run              `json:"run,omitempty"`
	Session      *Session          `json:"session,omitempty"`
	Confirmation *Confirmation     `json:"confirmation,omitempty"`
	Summary      *RunSummary       `json:"summary,omitempty"`
	SessionID    string            `json:"sessionId"`
	Seq          int64             `json:"seq"`
	Kind         string            `json:"kind"`
	RunID        string            `json:"runId,omitempty"`
	CallID       string            `json:"callId,omitempty"`
	Text         string            `json:"text,omitempty"`
	Status       string            `json:"status,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
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
	Provider            ProviderInfo
	AnalysisAuthorized  bool
	AnalysisStarted     bool
	Question            string
	SummarySaved        bool
	ReadBytes           int
	RetryActions        []Action
	RetryCallIDs        []string
	Turn                TurnInput
	Targets             []target.ConnectionSnapshot
	Analysis            *AnalysisPreview
	ActiveMillis        int64
	ModelCalls          int
	NeedsDiscovery      bool
	ContinuationStarted bool
	OutputBytes         int
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
