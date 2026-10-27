// Package selfupdate owns durable, explicitly requested upgrade tasks.
package selfupdate

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/version"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	ErrInvalid     = errors.New("selfupdate: invalid request")
	ErrConflict    = errors.New("selfupdate: request or task conflict")
	ErrNotFound    = errors.New("selfupdate: task not found")
	ErrUnavailable = errors.New("selfupdate: unavailable")
)

const (
	MaxManifest = int64(1 << 20)
	MaxArchive  = int64(256 << 20)
	MaxBinary   = int64(256 << 20)
)

type Request struct {
	RequestID string `json:"requestId"`
	Target    string `json:"target"`
}
type Task struct {
	BaseSHA256   string                `json:"baseSha256"`
	Repository   string                `json:"repository"`
	Blockers     []maintenance.Blocker `json:"blockers,omitempty"`
	ID           string                `json:"id"`
	RequestID    string                `json:"requestId"`
	Target       string                `json:"target"`
	From         string                `json:"from"`
	Platform     string                `json:"platform"`
	Asset        string                `json:"asset"`
	SHA256       string                `json:"sha256"`
	BinarySHA256 string                `json:"binarySha256,omitempty"`
	State        string                `json:"state"`
	Reason       string                `json:"reason,omitempty"`
	Bytes        int64                 `json:"bytes"`
	Total        int64                 `json:"total"`
	CreatedAt    time.Time             `json:"createdAt"`
	UpdatedAt    time.Time             `json:"updatedAt"`
	ETag         string                `json:"-"`
	LastResumeID string                `json:"-"`
}
type storedTask struct {
	Task
	ETag     string `json:"etag"`
	ResumeID string `json:"resumeId"`
}
type Options struct {
	Capabilities                                func() (version.DeploymentMode, bool)
	Repository, Executable, Directory, Platform string
	Client                                      *http.Client
	// BaseURL is a test-only, code-injected endpoint. It is never API input.
	BaseURL                   string
	Current                   func() version.Info
	Install                   func(string, string) error
	RequestRestart            func() error
	TotalTimeout, IdleTimeout time.Duration
}
type Service struct {
	db       *sql.DB
	opts     Options
	instance string
	root     *os.Root
	lock     *os.File
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	running  map[string]bool
	closed   bool
}

func terminal(s string) bool  { return s == "completed" || s == "failed" }
func resumable(s string) bool { return s == "interrupted" || s == "ready_blocked" }
